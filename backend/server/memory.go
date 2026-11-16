package server

import (
	"context"
	"runtime"
	"runtime/debug"
	"time"
)

const memoryIdleDelay = 5 * time.Second
const memoryReclaimCooldown = 30 * time.Second

// idleMemoryPolicy is driven only by the background sampler. Admissions update
// the existing request lock's epoch, not a timer, clock read or goroutine.
type idleMemoryPolicy struct {
	epoch       uint64
	quietSince  time.Time
	lastReclaim time.Time
}

func (p *idleMemoryPolicy) ready(now time.Time, epoch uint64, idle bool) bool {
	if !idle {
		p.epoch, p.quietSince = epoch, time.Time{}
		return false
	}
	if epoch != p.epoch || p.quietSince.IsZero() {
		p.epoch, p.quietSince = epoch, now
		return false
	}
	return now.Sub(p.quietSince) >= memoryIdleDelay && (p.lastReclaim.IsZero() || now.Sub(p.lastReclaim) >= memoryReclaimCooldown)
}

func reclaimableMemory(m runtime.MemStats, lastAlloc uint64) bool {
	retained := m.HeapIdle - min(m.HeapIdle, m.HeapReleased)
	// A burst can leave garbage in in-use spans until the next collection, so
	// already-idle spans alone miss exactly the post-load recovery we need.
	return m.TotalAlloc-lastAlloc >= 32<<20 || retained >= 32<<20 && retained >= m.HeapInuse/2
}

func (r *httpRequests) memorySnapshot() (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.epoch, !r.stopped && len(r.active) == 0 && r.background == 0
}

// Detached job transport must not look idle between HTTP submission and polling.
func (r *httpRequests) beginBackground() func() {
	r.mu.Lock()
	r.epoch++
	r.background++
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		r.background--
		r.mu.Unlock()
	}
}

func (s *Server) memoryIdle() (uint64, bool) {
	epoch, idle := s.httpRequests.memorySnapshot()
	if !idle {
		return epoch, false
	}
	s.agentEngineMu.Lock()
	engine := s.agentEngineInst
	s.agentEngineMu.Unlock()
	if engine != nil && engine.HasActiveTurns() {
		return epoch, false
	}
	s.gatewaySessions.mu.Lock()
	idle = len(s.gatewaySessions.active) == 0
	s.gatewaySessions.mu.Unlock()
	s.refreshJobs.mu.Lock()
	idle = idle && len(s.refreshJobs.running) == 0
	s.refreshJobs.mu.Unlock()
	if s.catalog != nil {
		s.catalog.mu.RLock()
		idle = idle && !s.catalog.isRefreshing
		s.catalog.mu.RUnlock()
	}
	if s.usageRetention != nil {
		s.usageRetention.statsMu.Lock()
		idle = idle && s.usageRetention.stats.State != "running"
		s.usageRetention.statsMu.Unlock()
	}
	return epoch, idle
}

// Keep startup's one-time FreeOSMemory in initializeProtocolRuntime. This worker
// reclaims post-burst garbage and excess spans after a quiet window, never once per request.
// New admissions are not blocked by the collector; a request arriving just after
// the final idle snapshot can still overlap a GC pause (the limit is soft too).
func (s *Server) runMemoryReclaimer(ctx context.Context) {
	defer close(s.memoryDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var policy idleMemoryPolicy
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	lastAlloc := memory.TotalAlloc
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			epoch, idle := s.memoryIdle()
			if !policy.ready(now, epoch, idle) {
				continue
			}
			runtime.ReadMemStats(&memory)
			if !reclaimableMemory(memory, lastAlloc) {
				continue
			}
			latest, stillIdle := s.memoryIdle()
			if ctx.Err() != nil || !stillIdle || latest != epoch {
				continue
			}
			debug.FreeOSMemory()
			lastAlloc = memory.TotalAlloc
			policy.lastReclaim = time.Now()
		}
	}
}
