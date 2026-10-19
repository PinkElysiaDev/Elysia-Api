package builtin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	p "github.com/elysia-api/backend/protocol"
)

type requestMemoryObservation struct {
	Semantic, Converted, Wire string
	Error                     string
	Issues                    []p.ConversionIssue
	Causes                    []string
}

// This file is also run in the isolated 8bc0cea checkout. The committed oracle
// stores bytes as strings, not RawMessage, so whitespace/escaping cannot be
// silently normalized by the comparison itself.
func TestRequestMemoryDifferential(t *testing.T) {
	definitions, err := Definitions()
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	observations := map[string]requestMemoryObservation{}
	for _, definition := range definitions {
		compiled, issues := compiler.Compile(definition.Bytes())
		if compiled == nil {
			t.Fatal(issues)
		}
		for _, sample := range compiled.Definition().Samples {
			if sample.Direction != p.DecodeRequest {
				continue
			}
			for _, changeModel := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/model=%t", compiled.Identity().DefinitionID, sample.ID, changeModel)
				observations[name] = observeMemoryRequest(t, compiled, sample.Input.Bytes(), p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}, changeModel)
			}
		}
		for index, body := range []string{`{}`, `null`, `{"messages":null}`, `{"model":"m","max_tokens":64,"messages":[{"role":"invalid","content":"hello"}]}`} {
			observations[fmt.Sprintf("%s/invalid-%d", compiled.Identity().DefinitionID, index)] = observeMemoryRequest(t, compiled, []byte(body), p.EvaluationContext{}, false)
		}
	}
	compiled := shippedProjectionProtocol(t, Anthropic)
	observations["anthropic/image-tool"] = observeMemoryRequest(t, compiled, largeAnthropicRequest(128, 3), p.EvaluationContext{}, true)
	observations["anthropic/lexemes"] = observeMemoryRequest(t, compiled, []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"<>&\u2028","extra":{"integer":9007199254740993,"decimal":1.00,"null":null,"empty":[]}}]}],"extension":{"a":1,"a":2}}`), p.EvaluationContext{}, true)
	actual, err := json.MarshalIndent(observations, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "request_memory_baseline.json")
	if destination := os.Getenv("ELYSIA_MEMORY_RECORD_BASELINE"); destination != "" {
		if err := os.WriteFile(destination, actual, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, actual) {
		var baseline map[string]requestMemoryObservation
		if err := json.Unmarshal(expected, &baseline); err != nil {
			t.Fatal(err)
		}
		for name, result := range observations {
			before, _ := json.Marshal(baseline[name])
			after, _ := json.Marshal(result)
			if !bytes.Equal(before, after) {
				t.Errorf("baseline differs at %s\nbefore: %s\nafter: %s", name, before, after)
			}
		}
		if len(baseline) != len(observations) {
			t.Error("baseline case count changed")
		}
	}
	t.Logf("compared %d byte-exact request observations against 8bc0cea", len(observations))
}

func observeMemoryRequest(t *testing.T, compiled *p.Compiled, body []byte, options p.EvaluationContext, changeModel bool) (result requestMemoryObservation) {
	t.Helper()
	request, err := compiled.DecodeRequest(t.Context(), body, options)
	if err == nil {
		semantic, encodeErr := json.Marshal(request)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		result.Semantic = string(semantic)
		conversion, compileErr := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
		if compileErr != nil {
			t.Fatal(compileErr)
		}
		request, err = conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
		if err == nil {
			if changeModel {
				request.Model = p.StringValue("actual-model")
			}
			semantic, encodeErr = json.Marshal(request)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			result.Converted = string(semantic)
			wire, encodeErr := compiled.EncodeRequest(t.Context(), request, options)
			result.Wire, err = string(wire), encodeErr
		}
	}
	if err != nil {
		result.Error = err.Error()
		var conversion *p.ConversionError
		if errors.As(err, &conversion) {
			result.Issues = conversion.Issues
		}
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			result.Causes = append(result.Causes, fmt.Sprintf("%T: %s", cause, cause.Error()))
		}
	}
	return result
}

// Phase snapshots distinguish allocated bytes from heap currently allocated.
// Retention runs are separate: forced GC is never mixed into formal timing.
func TestRequestMemoryPhases(t *testing.T) {
	text := os.Getenv("ELYSIA_MEMORY_MIB")
	if text == "" {
		t.Skip("opt-in request phase memory profile")
	}
	size, err := strconv.Atoi(text)
	if err != nil || size < 1 || size > 31 {
		t.Fatal("ELYSIA_MEMORY_MIB must be between 1 and 31")
	}
	compiled := shippedProjectionProtocol(t, Anthropic)
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	body := largeAnthropicRequest(size<<20, 0)
	runtime.GC()
	var previous runtime.MemStats
	runtime.ReadMemStats(&previous)
	started := time.Now()
	snapshot := func(phase string) {
		elapsed := time.Since(started)
		var current runtime.MemStats
		runtime.ReadMemStats(&current)
		t.Logf("phase=%s elapsed_ms=%.3f allocated_MiB=%.3f heap_MiB=%.3f heap_inuse_MiB=%.3f heap_sys_MiB=%.3f", phase, float64(elapsed)/float64(time.Millisecond), float64(current.TotalAlloc-previous.TotalAlloc)/(1<<20), float64(current.HeapAlloc)/(1<<20), float64(current.HeapInuse)/(1<<20), float64(current.HeapSys)/(1<<20))
		if os.Getenv("ELYSIA_MEMORY_RETENTION") == "1" {
			runtime.GC()
			runtime.ReadMemStats(&current)
			t.Logf("phase=%s retained_heap_MiB=%.3f", phase, float64(current.HeapAlloc)/(1<<20))
		}
		previous, started = current, time.Now()
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
	request.Model = p.StringValue("upstream-model")
	wire, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot("encode")
	if !bytes.Contains(wire, []byte("upstream-model")) || len(wire) < size<<20 {
		t.Fatal("model or history lost")
	}
	runtime.KeepAlive(body)
	runtime.KeepAlive(request)
	runtime.KeepAlive(wire)
}
