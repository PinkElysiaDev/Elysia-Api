package routing

import (
	"fmt"
	"sync"
	"time"

	"github.com/elysia-api/backend/config"
)

type QuotaUsage struct {
	Date     string
	Requests int
	Tokens   int
	Active   int
}

// Limiter owns daily quotas and concurrent request reservations. Its zero value
// uses local calendar days, matching usage accounting. All access is synchronized.
type Limiter struct {
	mu     sync.Mutex
	states map[string]*QuotaUsage
	now    func() time.Time
}

func (l *Limiter) state(id string) *QuotaUsage {
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	today := now.Format("2006-01-02")
	if l.states == nil {
		l.states = make(map[string]*QuotaUsage)
	}
	state := l.states[id]
	if state == nil {
		state = &QuotaUsage{}
		l.states[id] = state
	}
	if state.Date != today {
		// In-flight requests survive midnight; only daily counters roll over.
		state.Date, state.Requests, state.Tokens = today, 0, 0
	}
	return state
}

func (l *Limiter) Acquire(group *config.ModelGroupConfig, estimatedTokens int) (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.state(group.ID)
	if group.MaxConcurrency > 0 && state.Active >= group.MaxConcurrency {
		return nil, fmt.Errorf("max concurrency exceeded for group '%s'", group.Name)
	}
	if group.DailyLimitMaxRequests > 0 && state.Requests >= group.DailyLimitMaxRequests {
		return nil, fmt.Errorf("daily request limit exceeded for group '%s'", group.Name)
	}
	if group.DailyLimitMaxTokens > 0 && estimatedTokens > 0 && state.Tokens+estimatedTokens > group.DailyLimitMaxTokens {
		return nil, fmt.Errorf("daily token limit exceeded for group '%s'", group.Name)
	}
	state.Active++
	state.Requests++
	if estimatedTokens > 0 {
		state.Tokens += estimatedTokens
	}
	id, acquiredDate := group.ID, state.Date
	released := false
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if released {
			return
		}
		released = true
		current := l.states[id]
		// A deleted/recreated group has a different reservation owner.
		if current != state {
			return
		}
		current = l.state(id)
		if current.Active > 0 {
			current.Active--
		}
		if estimatedTokens > 0 && current.Date == acquiredDate {
			current.Tokens = max(0, current.Tokens-estimatedTokens)
		}
	}, nil
}

// Adjust adds actual usage. Acquire's release independently refunds estimates,
// including failed requests. Old-day settlements never charge the new day.
func (l *Limiter) Adjust(groupID string, actualTokens int, settledOn string) {
	if actualTokens <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.state(groupID)
	if settledOn != "" && state.Date != settledOn {
		return
	}
	state.Tokens = max(0, state.Tokens+actualTokens)
}

func (l *Limiter) Forget(groupID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.states, groupID)
}

func (l *Limiter) Usage(groupID string) QuotaUsage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return *l.state(groupID)
}
