package server

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// These wrappers are the only affinity API adaptation installed in the pinned
// release snapshot: its get/set calls receive time.Now explicitly. Production
// implementations and cache capacity/expiry behavior are left unchanged.
func reviewAffinityGet(cache *affinityCache, key string) string {
	return cache.get(key, "group")
}

func reviewAffinitySet(cache *affinityCache, key string) {
	cache.set(key, "group", "model")
}

func BenchmarkReviewCacheAffinity(b *testing.B) {
	for _, size := range []int{64, 4096} {
		for _, mixed := range []bool{false, true} {
			b.Run(fmt.Sprintf("keys=%d/write10=%t", size, mixed), func(b *testing.B) {
				cache := newAffinityCache()
				if closer, ok := any(cache).(interface{ close() }); ok {
					b.Cleanup(closer.close)
				}
				keys := make([]string, size)
				for i := range keys {
					keys[i] = fmt.Sprintf("synthetic-key-%d", i)
					reviewAffinitySet(cache, keys[i])
				}
				var workers atomic.Uint64
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					index := int(workers.Add(1))
					for pb.Next() {
						key := keys[index%size]
						if mixed && index%10 == 0 {
							reviewAffinitySet(cache, key)
						} else if reviewAffinityGet(cache, key) != "model" {
							b.Error("unexpected affinity miss")
						}
						index++
					}
				})
			})
		}
	}
}

// No command execution or remote calls. Library migration deliberately changed
// rejection rules, so only shared successful literal-command syntax is paired.
func BenchmarkReviewCLI(b *testing.B) {
	b.Run("parse", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			statement, err := cliParseStatement("elysia usage logs --status failed | grep timeout | head 5")
			if err != nil || len(statement.pipes) != 2 || len(statement.args) == 0 {
				b.Fatal("invalid parsed statement", err)
			}
		}
	})
	b.Run("resolve", func(b *testing.B) {
		args := []string{"source", "ls"}
		b.ReportAllocs()
		for b.Loop() {
			invocation, err := cliResolve(args)
			if err != nil || invocation == nil {
				b.Fatal("invalid invocation", err)
			}
		}
	})
}
