package routing

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheInvalidationSeparatesInflightGenerations(t *testing.T) {
	t.Parallel()
	var cache Cache
	started, release := make(chan struct{}), make(chan struct{}, 1)
	defer close(release)
	oldResult := make(chan *Snapshot, 1)
	go func() {
		view, _ := cache.Load(t.Context(), func(context.Context) (*Snapshot, error) {
			close(started)
			<-release
			return &Snapshot{}, nil
		})
		oldResult <- view
	}()
	<-started
	cache.Invalidate()
	fresh := &Snapshot{}
	newResult := make(chan *Snapshot, 1)
	go func() {
		view, _ := cache.Load(t.Context(), func(context.Context) (*Snapshot, error) { return fresh, nil })
		newResult <- view
	}()
	select {
	case view := <-newResult:
		if view != fresh {
			t.Fatal("new generation did not load its own snapshot")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("new generation waited for an obsolete flight")
	}
	// Release the old load only after the new snapshot has been published.
	release <- struct{}{}
	if view := <-oldResult; view != nil {
		t.Fatal("invalidated load returned stale authorization")
	}
	view, err := cache.Load(t.Context(), func(context.Context) (*Snapshot, error) {
		t.Error("old load replaced the current snapshot")
		return nil, errors.New("unexpected reload")
	})
	if err != nil || view != fresh {
		t.Fatalf("current snapshot lost: view=%p err=%v", view, err)
	}
}

func TestCacheCoalescesLoadsAndRetriesFailures(t *testing.T) {
	t.Parallel()
	var cache Cache
	failed := errors.New("repository unavailable")
	if view, err := cache.Load(t.Context(), func(context.Context) (*Snapshot, error) { return nil, failed }); view != nil || !errors.Is(err, failed) {
		t.Fatalf("repository failure not propagated: view=%p err=%v", view, err)
	}
	var calls atomic.Int32
	want := &Snapshot{}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			<-start
			view, err := cache.Load(t.Context(), func(context.Context) (*Snapshot, error) {
				calls.Add(1)
				return want, nil
			})
			if err != nil || view != want {
				t.Errorf("view=%p err=%v", view, err)
			}
		})
	}
	close(start)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent cache reads performed %d loads", calls.Load())
	}
}
