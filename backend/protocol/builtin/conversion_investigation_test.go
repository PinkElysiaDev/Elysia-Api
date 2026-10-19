package builtin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	p "github.com/elysia-api/backend/protocol"
)

type investigationPhase struct {
	Workload, Phase string
	SizeMiB, Repeat int
	ElapsedMS       float64
	Allocated, Heap uint64
}

func investigationBody(size int, nodes bool) []byte {
	if !nodes {
		return largeAnthropicRequest(size<<20, 0)
	}
	blocks := make([]map[string]string, 1800)
	for index := range blocks {
		blocks[index] = map[string]string{"type": "text", "text": strings.Repeat("x", (size<<20)/1800)}
	}
	body, _ := json.Marshal(map[string]any{"model": "m", "max_tokens": 64, "messages": []any{map[string]any{"role": "user", "content": blocks}}})
	return body
}

// Diagnostic snapshots with forced GC run separately from phase timings.
func TestConversionInvestigationPhases(t *testing.T) {
	mode := os.Getenv("ELYSIA_INVESTIGATE")
	if mode == "" {
		t.Skip("opt-in conversion phase investigation")
	}
	if mode != "phases" && mode != "retention" {
		t.Fatal("invalid mode")
	}
	directory := os.Getenv("ELYSIA_INVESTIGATE_OUTPUT")
	if directory == "" {
		t.Fatal("output directory required")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	compiled := shippedProjectionProtocol(t, Anthropic)
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	var records []investigationPhase
	for _, workload := range []string{"image", "nodes"} {
		for _, size := range []int{2, 8, 16, 31} {
			if selected := os.Getenv("ELYSIA_INVESTIGATE_SIZES"); selected != "" && !strings.Contains(","+selected+",", fmt.Sprintf(",%d,", size)) {
				continue
			}
			if workload == "nodes" && size != 2 {
				continue
			}
			if mode == "retention" && size != 2 && size != 31 {
				continue
			}
			repeats := 5
			if mode == "retention" {
				repeats = 1
			}
			for repeat := 0; repeat < repeats; repeat++ {
				body := investigationBody(size, workload == "nodes")
				runtime.GC()
				var previous runtime.MemStats
				runtime.ReadMemStats(&previous)
				started := time.Now()
				snapshot := func(phase string) {
					elapsed := time.Since(started)
					var current runtime.MemStats
					runtime.ReadMemStats(&current)
					records = append(records, investigationPhase{Workload: workload, Phase: phase, SizeMiB: size, Repeat: repeat, ElapsedMS: float64(elapsed) / float64(time.Millisecond), Allocated: current.TotalAlloc - previous.TotalAlloc, Heap: current.HeapAlloc})
					if mode == "retention" {
						runtime.GC()
						file, err := os.Create(filepath.Join(directory, fmt.Sprintf("%s-%d-%s.pprof", workload, size, phase)))
						if err != nil {
							t.Fatal(err)
						}
						if err := pprof.Lookup("heap").WriteTo(file, 0); err != nil {
							t.Fatal(err)
						}
						file.Close()
					}
					runtime.ReadMemStats(&previous)
					started = time.Now()
				}
				request, err := compiled.DecodeRequest(t.Context(), body, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				snapshot("decode")
				request, err = conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
				if err != nil {
					t.Fatal(err)
				}
				snapshot("convert")
				request.Model = p.StringValue("actual-model")
				wire, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				snapshot("encode")
				if !bytes.Contains(wire, []byte("actual-model")) || len(wire) < size<<20 {
					t.Fatal("payload lost")
				}
				runtime.KeepAlive(body)
				runtime.KeepAlive(request)
				runtime.KeepAlive(wire)
				if mode == "retention" {
					body = nil
					request = nil
					wire = nil
					snapshot("released")
				}
			}
		}
	}
	raw, _ := json.MarshalIndent(records, "", "  ")
	if err := os.WriteFile(filepath.Join(directory, mode+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

// These full request pipelines compare typed defaults with explicit authored
// JSON boundaries. Four protocols use text/tool-compatible semantic requests.
func TestConversionInvestigationBoundaries(t *testing.T) {
	if os.Getenv("ELYSIA_INVESTIGATE") != "boundaries" {
		t.Skip("opt-in boundary investigation")
	}
	var records []investigationPhase
	for _, sourceCodec := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, targetCodec := range []string{Chat, Responses, Anthropic, Gemini} {
			source := shippedProjectionProtocol(t, sourceCodec)
			target := shippedProjectionProtocol(t, targetCodec)
			for _, boundary := range []string{"typed", "set-same", "set-changed", "json-match"} {
				policy := p.DefaultConversionPolicy(source, target)
				if boundary != "typed" {
					rule := p.ConversionRule{ID: "investigation-model", Order: 155, Enabled: true, Phase: p.ConversionRequest, Action: "set", Path: "/model", Value: p.StringValue("m")}
					if boundary == "set-changed" {
						rule.Value = p.StringValue("actual-model")
					}
					if boundary == "json-match" {
						present := true
						rule.Match = p.ConversionMatch{Path: "/model", Present: &present}
					}
					policy.Rules = append(policy.Rules[:len(policy.Rules):len(policy.Rules)], rule)
				}
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				body := &p.Request{SchemaVersion: 1, Model: p.StringValue("m"), Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue(strings.Repeat("hello ", 4096))}}}}, Parameters: p.Object{"max_output_tokens": p.StringValue("128")}}
				// Parameters use a numeric JSON value, preserving its wire type.
				body.Parameters["max_output_tokens"], _ = p.ParseValue([]byte("128"))
				options := p.EvaluationContext{Scope: p.Scope{Model: "m"}}
				wire, err := source.EncodeRequest(t.Context(), body, options)
				if err != nil {
					t.Fatal(err)
				}
				for repeat := 0; repeat < 5; repeat++ {
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					started := time.Now()
					for range 10 {
						request, err := source.DecodeRequest(t.Context(), wire, options)
						if err != nil {
							t.Fatal(err)
						}
						request, err = conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Model: "m"}, nil)
						if err != nil {
							t.Fatal(err)
						}
						output, err := target.EncodeRequest(t.Context(), request, options)
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Contains(output, []byte("hello")) {
							t.Fatal("text lost")
						}
					}
					elapsed := time.Since(started)
					runtime.ReadMemStats(&after)
					records = append(records, investigationPhase{Workload: fmt.Sprintf("%s-to-%s/%s", sourceCodec, targetCodec, boundary), Phase: "pipeline-10-requests", Repeat: repeat, ElapsedMS: float64(elapsed) / float64(time.Millisecond), Allocated: after.TotalAlloc - before.TotalAlloc, Heap: after.HeapAlloc})
				}
			}
		}
	}
	raw, _ := json.MarshalIndent(records, "", "  ")
	if err := os.WriteFile(filepath.Join(os.Getenv("ELYSIA_INVESTIGATE_OUTPUT"), "boundaries.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
