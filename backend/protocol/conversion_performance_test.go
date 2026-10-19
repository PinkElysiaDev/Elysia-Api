package protocol_test

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	p "github.com/elysia-api/backend/protocol"
)

func largeConversionRequest(size int) []byte {
	return []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + strings.Repeat("A", size) + `"}}]}]}]}`)
}

func TestConversionPerformancePairs(t *testing.T) {
	if os.Getenv("ELYSIA_CONVERSION_PROFILE") != "1" {
		t.Skip("opt-in paired local performance workload")
	}
	var compiled *p.Compiled
	for _, candidate := range performanceProtocols(t) {
		if candidate.Codec(p.EncodeRequest) == "anthropic" {
			compiled = candidate
		}
	}
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
	for _, workers := range []int{1, 4, 32} {
		for _, size := range []int{2, 8, 16, 31} {
			if workers == 32 && size != 2 {
				continue
			}
			t.Run(fmt.Sprintf("%dMiB/workers%d", size, workers), func(t *testing.T) {
				requests := make([]*p.Request, workers)
				body := largeConversionRequest(size << 20)
				for i := range workers {
					requests[i], err = compiled.DecodeRequest(t.Context(), body, p.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
				}
				body = nil
				for pair := range 5 {
					modes := []string{"baseline", "optimized"}
					if pair%2 == 1 {
						modes[0], modes[1] = modes[1], modes[0]
					}
					for _, mode := range modes {
						runtime.GC()
						runtime.GC()
						var before, after runtime.MemStats
						runtime.ReadMemStats(&before)
						peak := before.HeapAlloc
						stop, done := make(chan struct{}), make(chan struct{})
						go func() {
							defer close(done)
							timer := time.NewTicker(20 * time.Millisecond)
							defer timer.Stop()
							for {
								select {
								case <-stop:
									return
								case <-timer.C:
									var sample runtime.MemStats
									runtime.ReadMemStats(&sample)
									peak = max(peak, sample.HeapAlloc)
								}
							}
						}()
						started := time.Now()
						var wait sync.WaitGroup
						for i := range workers {
							wait.Add(1)
							go func() {
								defer wait.Done()
								var out *p.Request
								var failure error
								if mode == "baseline" {
									out, failure = conversion.RequestBaseline(t.Context(), requests[i], route, nil)
								} else {
									out, failure = conversion.Request(t.Context(), requests[i], route, nil)
								}
								if failure != nil {
									t.Error(failure)
									return
								}
								if len(out.Content) != 2 || out.Content[1].Children[0].Payload != requests[i].Content[1].Children[0].Payload {
									t.Error("tool/image payload changed")
								}
							}()
						}
						wait.Wait()
						elapsed := time.Since(started)
						close(stop)
						<-done
						runtime.ReadMemStats(&after)
						peak = max(peak, after.HeapAlloc)
						t.Logf("pair=%d mode=%s size_MiB=%d workers=%d elapsed_ms=%.3f allocated_MiB=%.3f heap_start_MiB=%.3f peak_heap_MiB=%.3f", pair, mode, size, workers, float64(elapsed)/float64(time.Millisecond), float64(after.TotalAlloc-before.TotalAlloc)/(1<<20), float64(before.HeapAlloc)/(1<<20), float64(peak)/(1<<20))
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := conversion.Request(ctx, requests[0], route, nil); err != context.Canceled {
					t.Fatalf("cancel: %v", err)
				}
				requests = nil
				runtime.GC()
				runtime.GC()
				var after runtime.MemStats
				runtime.ReadMemStats(&after)
				t.Logf("reclaimed_heap_MiB=%.3f", float64(after.HeapAlloc)/(1<<20))
			})
		}
	}
}

func BenchmarkConversionExecution(b *testing.B) {
	protocols := performanceProtocols(b)
	var source, target *p.Compiled
	for _, compiled := range protocols {
		switch compiled.Codec(p.EncodeRequest) {
		case "anthropic":
			target = compiled
		case "responses":
			source = compiled
		}
	}
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(target, source))
	if err != nil {
		b.Fatal(err)
	}
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Model: "m", Delivery: p.NewDeliveryState()}
	for _, size := range []int{64, 2 << 20} {
		for _, phase := range []p.ConversionPhase{p.ConversionRequest, p.ConversionResponse, p.ConversionEvent} {
			var document any
			switch phase {
			case p.ConversionRequest:
				document = &p.Request{SchemaVersion: 1, Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue(strings.Repeat("x", size))}}}}}
			case p.ConversionResponse:
				document = &p.Response{SchemaVersion: 1, ID: p.StringValue("r"), Model: p.StringValue("m"), Content: []p.Node{{Kind: p.TextNode, Payload: p.StringValue(strings.Repeat("x", size))}}}
			case p.ConversionEvent:
				document = &p.Event{SchemaVersion: 1, Type: p.ItemDelta, Delta: p.StringValue(strings.Repeat("x", size))}
			}
			input, err := p.EncodeValue(document)
			if err != nil {
				b.Fatal(err)
			}
			for _, mode := range []string{"baseline", "optimized"} {
				b.Run(fmt.Sprintf("%s/%d/%s", phase, size, mode), func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						var failure error
						if mode == "baseline" {
							_, failure = conversion.ApplyValueBaseline(b.Context(), phase, input, route, nil)
						} else {
							_, failure = conversion.ApplyValue(b.Context(), phase, input, route, nil)
						}
						if failure != nil {
							b.Fatal(failure)
						}
					}
				})
			}
		}
	}
}
