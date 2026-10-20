// Package routing owns route snapshots and request admission independently of HTTP.
package routing

import (
	"context"
	"strconv"
	"sync"

	"github.com/elysia-api/backend/config"
	"golang.org/x/sync/singleflight"
)

// Snapshot is immutable after publication. Callers must not mutate its slices,
// maps, or nested model/key data.
type Snapshot struct {
	Groups []config.ModelGroupConfig
	Tokens map[string]config.AccessToken
}

// Cache coalesces loads within a generation. Invalidation prevents in-flight
// loads from publishing stale credentials or routes. Its zero value is ready.
type Cache struct {
	mu         sync.RWMutex
	generation uint64
	snapshot   *Snapshot
	flights    singleflight.Group
}

func (c *Cache) Load(ctx context.Context, load func(context.Context) (*Snapshot, error)) (*Snapshot, error) {
	c.mu.RLock()
	generation, snapshot := c.generation, c.snapshot
	c.mu.RUnlock()
	if snapshot != nil {
		return snapshot, nil
	}
	result, err, _ := c.flights.Do(strconv.FormatUint(generation, 10), func() (any, error) {
		c.mu.RLock()
		current, snapshot := c.generation == generation, c.snapshot
		c.mu.RUnlock()
		if !current || snapshot != nil {
			return snapshot, nil
		}
		snapshot, err := load(ctx)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.generation != generation {
			return (*Snapshot)(nil), nil
		}
		c.snapshot = snapshot
		return snapshot, nil
	})
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.generation != generation {
		return nil, nil
	}
	return result.(*Snapshot), nil
}

func (c *Cache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.snapshot = nil
}
