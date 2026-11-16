package server

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
)

func cacheAliasExamplePackage(t *testing.T) protocol.ProtocolPackage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "cache-usage-alias.mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	var patch struct {
		Expressions map[string]protocol.Expression `json:"expressions"`
	}
	if err := sonic.ConfigStd.Unmarshal(raw, &patch); err != nil {
		t.Fatal(err)
	}
	definition := presetPackage(t, "openai-chat-completions")
	definition.Spec.ID = "independent-usage-alias-example"
	sampleBytes, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "cache-usage-alias.sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sample protocol.Sample
	if err := sonic.ConfigStd.Unmarshal(sampleBytes, &sample); err != nil {
		t.Fatal(err)
	}
	definition.Mapping.Samples = append(definition.Mapping.Samples, sample)
	for _, name := range []string{"decode-usage", "stream-decode-usage"} {
		definition.Mapping.Expressions["standard-"+name] = definition.Mapping.Expressions[name]
	}
	for name, expression := range patch.Expressions {
		definition.Mapping.Expressions[name] = expression
	}
	return definition
}

func TestCacheAliasExamplePreservesStandardZeroAndUnrelatedExtensions(t *testing.T) {
	definition := cacheAliasExamplePackage(t)
	compiled := compileFixturePackage(t, definition)
	standard := compileFixturePackage(t, presetPackage(t, "openai-chat-completions"))
	different := compiledPackage(compiled)
	different.Spec.ID = "different-usage-decoder-same-ref"
	// Identical ref names with different bodies must not authorize replay.
	different.Mapping.Expressions["cache-usage"] = protocol.Expression{Op: "read", From: "binding", Name: "decoded-usage"}
	differentCompiled := compileFixturePackage(t, different)
	if report := protocol.Verify(t.Context(), compiled); !report.Passed {
		t.Fatal(report.Issues)
	}
	options, err := (protocol.EvaluationContext{}).WithOperation("cache-alias-fixture", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		usage string
		want  int64
	}{
		{`{"prompt_tokens":100,"provider_metrics":{"write_tokens":30,"keep":"extension"}}`, 30},
		{`{"prompt_tokens":100,"prompt_tokens_details":{"cache_write_tokens":0},"provider_metrics":{"write_tokens":30,"keep":"extension"}}`, 0},
		{`{"prompt_tokens":100}`, -1},
	} {
		body := []byte(`{"id":"r","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":` + fixture.usage + `}`)
		response, err := compiled.DecodeResponse(t.Context(), body, options)
		if err != nil {
			t.Fatal(err)
		}
		check := func(counter *protocol.Counter) {
			t.Helper()
			if fixture.want < 0 {
				if counter != nil {
					t.Fatal("missing became zero")
				}
				return
			}
			if counter == nil || counter.Count != fixture.want {
				t.Fatal("declared precedence changed", counter)
			}
		}
		check(response.Usage.CacheCreation)
		wire, err := compiled.EncodeResponse(t.Context(), response, options)
		if err != nil {
			t.Fatal(err)
		}
		original, _ := protocol.ParseValue(body)
		encoded, _ := protocol.ParseValue(wire)
		var before, after any
		if err := original.Decode(&before); err != nil {
			t.Fatal(err)
		}
		if err := encoded.Decode(&after); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("native extension or explicit zero changed")
		}
		frame := mustProtocolValue(t, `{"id":"r","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":`+fixture.usage+`}`)
		streamOptions := options
		streamOptions.State = protocol.NewEvaluationState()
		defer streamOptions.State.Release()
		events, err := compiled.DecodeFrame(t.Context(), frame, streamOptions)
		if err != nil {
			t.Fatal(err)
		}
		hasUsage := false
		for _, event := range events.Events {
			if event.Usage != nil {
				hasUsage = true
				check(event.Usage.CacheCreation)
			}
		}
		if !hasUsage {
			t.Fatal("usage frame omitted")
		}
		for _, target := range []*protocol.Compiled{standard, differentCompiled} {
			validation, err := target.NewWireStreamValidation(options.Scope)
			if err != nil {
				t.Fatal(err)
			}
			defer validation.Release()
			targetOptions := options.WithOutputStream(validation)
			targetOptions.State = protocol.NewEvaluationState()
			defer targetOptions.State.Release()
			_, err = target.EncodeFrame(t.Context(), events, targetOptions)
			var failure *protocol.ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != protocol.UnsupportedNative || failure.Issues[0].Path != "/frame" {
				t.Fatal("native replay did not enforce declared decoder equivalence", target.Identity().DefinitionID, err)
			}
		}
	}
}
