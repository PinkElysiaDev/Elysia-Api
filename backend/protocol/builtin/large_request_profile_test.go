package builtin

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	p "github.com/elysia-api/backend/protocol"
)

// TestLargeRequestResourceProfile is an opt-in local workload, not a provider
// benchmark. It measures Go heap and allocations, not an operating-system cap.
func TestLargeRequestResourceProfile(t *testing.T) {
	if os.Getenv("ELYSIA_RESOURCE_PROFILE") != "1" {
		t.Skip("opt-in allocation profile")
	}
	compiled := shippedProjectionProtocol(t, Anthropic)
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	for _, parallel := range []int{1, 4, 32} {
		sizes := []int{2, 8, 16, 31}
		if parallel == 32 {
			sizes = []int{2}
		}
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%dMiB/workers%d", size, parallel), func(t *testing.T) {
				body := largeAnthropicRequest(size<<20, 0)
				runtime.GC()
				var before runtime.MemStats
				runtime.ReadMemStats(&before)
				var peak atomic.Uint64
				peak.Store(before.HeapAlloc)
				stop, done := make(chan struct{}), make(chan struct{})
				go func() {
					defer close(done)
					ticker := time.NewTicker(20 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-stop:
							return
						case <-ticker.C:
							var current runtime.MemStats
							runtime.ReadMemStats(&current)
							if current.HeapAlloc > peak.Load() {
								peak.Store(current.HeapAlloc)
							}
						}
					}
				}()
				start := time.Now()
				var workers sync.WaitGroup
				for range parallel {
					workers.Add(1)
					go func() {
						defer workers.Done()
						request, e := compiled.DecodeRequest(t.Context(), body, p.EvaluationContext{})
						if e != nil {
							t.Error(e)
							return
						}
						request, e = conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
						if e != nil {
							t.Error(e)
							return
						}
						wire, e := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
						if e != nil {
							t.Error(e)
							return
						}
						if len(wire) < size<<20 {
							t.Error("image lost")
						}
					}()
				}
				workers.Wait()
				elapsed := time.Since(start)
				close(stop)
				<-done
				var after runtime.MemStats
				runtime.ReadMemStats(&after)
				ctx, cancel := context.WithCancel(t.Context())
				timer := time.AfterFunc(time.Millisecond, cancel)
				if _, e := compiled.DecodeRequest(ctx, body, p.EvaluationContext{}); e == nil {
					t.Error("cancel ignored")
				}
				timer.Stop()
				cancel()
				body = nil
				runtime.GC()
				var reclaimed runtime.MemStats
				runtime.ReadMemStats(&reclaimed)
				t.Logf("bytes=%d workers=%d elapsed=%s total_alloc_MiB=%.1f peak_heap_MiB=%.1f after_gc_MiB=%.1f", size<<20, parallel, elapsed, float64(after.TotalAlloc-before.TotalAlloc)/(1<<20), float64(peak.Load())/(1<<20), float64(reclaimed.HeapAlloc)/(1<<20))
			})
		}
	}
}
