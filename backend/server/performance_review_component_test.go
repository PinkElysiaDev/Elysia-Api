package server

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
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

// Explanatory lower-bound experiment, not a production redactor replacement.
// The fallback regexp requires a literal quote. Nested JSON/SSE dispatch and
// security differential coverage still belong to a future implementation.
func BenchmarkReviewTurnStateScan(b *testing.B) {
	for _, size := range []int{2, 31} {
		text := strings.Repeat("A", size<<20)
		for _, prefilter := range []bool{false, true} {
			b.Run(fmt.Sprintf("MiB=%d/prefilter=%t", size, prefilter), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(text)))
				for b.Loop() {
					if prefilter && strings.IndexByte(text, '"') < 0 {
						continue
					}
					if partialTurnStateKey.FindStringIndex(text) != nil {
						b.Fatal("synthetic base64 unexpectedly matched")
					}
				}
			})
		}
	}
}

// Current-only explanatory experiment. Each variant runs in its own benchmark
// process; never toggle Cobra's process-global hook in a production request.
func BenchmarkReviewCLIWindowsHook(b *testing.B) {
	for _, disabled := range []bool{false, true} {
		b.Run(fmt.Sprintf("disabled=%t", disabled), func(b *testing.B) {
			original := cobra.MousetrapHelpText
			b.Cleanup(func() { cobra.MousetrapHelpText = original })
			if disabled {
				cobra.MousetrapHelpText = ""
			} else {
				// Explicitly restore the dependency hook for this isolated control;
				// production initialization now disables it before any command runs.
				cobra.MousetrapHelpText = "synthetic benchmark console hook"
			}
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
}
