package protocol

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestOwnedRulesDoNotCopyUnrelatedPayload(t *testing.T) {
	for _, count := range []int{1, 8, 32} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			policy := ConversionPolicy{SchemaVersion: 1, ID: "allocation", Rules: []ConversionRule{}}
			for index := range count {
				policy.Rules = append(policy.Rules, ConversionRule{ID: fmt.Sprintf("rule-%d", index), Order: index, Enabled: true, Phase: ConversionRequest, Action: "stream_options", Value: StringValue("anthropic")})
			}
			conversion, err := CompileConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			measure := func(size int) uint64 {
				request := &Request{Content: []Node{{Kind: ImageNode, Payload: StringValue(strings.Repeat("A", size))}}}
				if _, err := conversion.Request(t.Context(), request, ConversionContext{}, nil); err != nil {
					t.Fatal(err)
				}
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				for range 4 {
					if _, err := conversion.Request(t.Context(), request, ConversionContext{}, nil); err != nil {
						t.Fatal(err)
					}
				}
				runtime.ReadMemStats(&after)
				runtime.KeepAlive(request)
				return (after.TotalAlloc - before.TotalAlloc) / 4
			}
			small, large := measure(64), measure(1<<20)
			// A JSON round trip allocates multiple MiB; cache bookkeeping and runtime
			// sampling may vary by KiB, so this guards the mechanism, not a microtime.
			if large > small+(512<<10) {
				t.Fatalf("%d rules copied payload: small=%d large=%d", count, small, large)
			}
		})
	}
}
