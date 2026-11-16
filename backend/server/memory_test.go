package server

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

func TestIdleMemoryPolicy(t *testing.T) {
	var p idleMemoryPolicy
	now := time.Unix(1000, 0)
	if p.ready(now, 1, true) || p.ready(now.Add(memoryIdleDelay-time.Nanosecond), 1, true) {
		t.Fatal("reclaimed before quiet window")
	}
	if !p.ready(now.Add(memoryIdleDelay), 1, true) {
		t.Fatal("idle window not eligible")
	}
	p.lastReclaim = now.Add(memoryIdleDelay)
	if p.ready(now.Add(memoryIdleDelay+time.Second), 1, true) {
		t.Fatal("cooldown ignored")
	}
	if !p.ready(p.lastReclaim.Add(memoryReclaimCooldown), 1, true) {
		t.Fatal("cooldown never ends")
	}
	if p.ready(now.Add(time.Minute), 2, true) {
		t.Fatal("admission did not reset quiet window")
	}
	if p.ready(now.Add(time.Minute+memoryIdleDelay), 2, false) {
		t.Fatal("active request appeared idle")
	}
	if p.ready(now.Add(time.Minute+memoryIdleDelay+time.Second), 2, true) {
		t.Fatal("active window counted toward idleness")
	}
}

func TestIdleMemoryTracksRequestAndDetachedWork(t *testing.T) {
	s := &Server{}
	first, idle := s.memoryIdle()
	if !idle {
		t.Fatal("empty server not idle")
	}
	c := &app.RequestContext{}
	if !s.httpRequests.enter(c, nil) {
		t.Fatal("request rejected")
	}
	if _, idle := s.memoryIdle(); idle {
		t.Fatal("active HTTP appeared idle")
	}
	s.httpRequests.leave(c)
	if epoch, idle := s.memoryIdle(); !idle || epoch == first {
		t.Fatal("completed traffic not recorded")
	}
	done := s.httpRequests.beginBackground()
	if _, idle := s.memoryIdle(); idle {
		t.Fatal("detached job appeared idle")
	}
	done()
	_, release, err := s.gatewaySessions.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, idle := s.memoryIdle(); idle {
		t.Fatal("WebSocket appeared idle")
	}
	release()
	s.refreshJobs.running = map[string]bool{"source": true}
	if _, idle := s.memoryIdle(); idle {
		t.Fatal("refresh appeared idle")
	}
	delete(s.refreshJobs.running, "source")
	if _, idle := s.memoryIdle(); !idle {
		t.Fatal("finished work not idle")
	}
	s.catalog = &modelCatalog{isRefreshing: true}
	if _, idle := s.memoryIdle(); idle {
		t.Fatal("catalog refresh appeared idle")
	}
	s.catalog.isRefreshing = false
	s.usageRetention = &usageRetention{stats: retentionStats{State: "running"}}
	if _, idle := s.memoryIdle(); idle {
		t.Fatal("storage maintenance appeared idle")
	}
	s.usageRetention.stats.State = "idle"
	if _, idle := s.memoryIdle(); !idle {
		t.Fatal("completed maintenance not idle")
	}
	s.httpRequests.stop()
	if _, idle := s.memoryIdle(); idle {
		t.Fatal("shutdown appeared idle")
	}
}

func TestIdleMemoryReclaimThreshold(t *testing.T) {
	if !reclaimableMemory(runtime.MemStats{TotalAlloc: 96 << 20, HeapInuse: 128 << 20}, 64<<20) {
		t.Fatal("post-burst garbage in in-use spans was not collected")
	}
	if reclaimableMemory(runtime.MemStats{TotalAlloc: 96 << 20}, 96<<20) {
		t.Fatal("unchanged allocation epoch repeatedly reclaimed")
	}
	for _, test := range []struct {
		name                  string
		idle, released, inuse uint64
		want                  bool
	}{
		{"small", 31 << 20, 0, 16 << 20, false},
		{"already-released", 128 << 20, 100 << 20, 32 << 20, false},
		{"live-dominated", 32 << 20, 0, 128 << 20, false},
		{"excess", 96 << 20, 32 << 20, 64 << 20, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := reclaimableMemory(runtime.MemStats{HeapIdle: test.idle, HeapReleased: test.released, HeapInuse: test.inuse}, 0); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestIdleMemoryWorkerStops(t *testing.T) {
	s := &Server{memoryDone: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	go s.runMemoryReclaimer(ctx)
	cancel()
	select {
	case <-s.memoryDone:
	case <-time.After(time.Second):
		t.Fatal("idle worker survived cancellation")
	}
}
