package server

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

// Run with -cpu=1,8 to compare uncontended operations and shared-cache contention.
func BenchmarkAffinityCache(b *testing.B) {
	for _, size := range []int{64, 4096} {
		for _, writes := range []bool{false, true} {
			workload := "ReadHit"
			if writes {
				workload = "Read90Write10"
			}
			b.Run(fmt.Sprintf("%s/keys=%d", workload, size), func(b *testing.B) {
				cache := newAffinityCache()
				b.Cleanup(cache.close)
				keys := make([]string, size)
				for i := range keys {
					keys[i] = fmt.Sprintf("key-%d", i)
					cache.set(keys[i], "group", "model")
				}
				var workers atomic.Uint64
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					index := int(workers.Add(1))
					for pb.Next() {
						key := keys[index%len(keys)]
						if writes && index%10 == 0 {
							cache.set(key, "group", "model")
						} else if cache.get(key, "group") != "model" {
							b.Error("unexpected affinity miss")
						}
						index++
					}
				})
			})
		}
	}
}

// Include Hertz routing, cache-key normalization and response writing. The handler
// serves a fixed 1 KiB body so SQL latency cannot hide cache overhead.
func BenchmarkUsageResponseCache(b *testing.B) {
	for _, workload := range []struct {
		name string
		keys int
	}{{"Hit", 128}, {"Churn", 1024}} {
		b.Run(workload.name, func(b *testing.B) {
			cache := &usageResponseCache{}
			b.Cleanup(cache.close)
			body := []byte(strings.Repeat("x", 1024))
			var loads atomic.Uint64
			router := newTestEngine(b)
			router.GET("/usage", cache.middleware(), func(ctx context.Context, c *app.RequestContext) {
				loads.Add(1)
				c.Data(http.StatusOK, "application/json", body)
			})
			requests := make([]*http.Request, workload.keys)
			for i := range requests {
				requests[i] = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/usage?model=%d&window=day", i), nil)
				if i < usageCacheMaxEntries {
					serveTestHTTP(router, httptest.NewRecorder(), requests[i])
				}
			}
			var workers atomic.Uint64
			loads.Store(0)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				random := rand.New(rand.NewPCG(workers.Add(1), 1))
				for pb.Next() {
					recorder := httptest.NewRecorder()
					serveTestHTTP(router, recorder, requests[random.IntN(len(requests))])
					if recorder.Code != http.StatusOK || recorder.Body.Len() != len(body) {
						b.Error("unexpected usage response")
					}
				}
			})
			b.StopTimer()
			b.ReportMetric(float64(loads.Load())/float64(b.N), "loads/op")
			entries := cache.entries.Load()
			b.ReportMetric(float64(entries.EstimatedSize()), "entries")
			entries.CleanUp()
			b.ReportMetric(float64(entries.EstimatedSize()), "retained-entries")
		})
	}
}
