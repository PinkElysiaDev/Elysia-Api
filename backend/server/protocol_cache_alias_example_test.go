package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func cacheAliasExampleDefinition(t *testing.T) protocol.Definition {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "cache-usage-alias.mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mappings map[protocol.Direction]protocol.Mapping
	if err := json.Unmarshal(raw, &mappings); err != nil {
		t.Fatal(err)
	}
	definition := presetDefinition(t, "chat-completions-api")
	definition.ID = "independent-usage-alias-example"
	sampleBytes, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "cache-usage-alias.sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sample protocol.Sample
	if err := json.Unmarshal(sampleBytes, &sample); err != nil {
		t.Fatal(err)
	}
	definition.Samples = append(definition.Samples, sample)
	for direction, patch := range mappings {
		mapping := definition.Directions[direction]
		mapping.After = patch.After
		definition.Directions[direction] = mapping
	}
	return definition
}

func TestCacheAliasExamplePreservesStandardZeroAndUnrelatedExtensions(t *testing.T) {
	definition := cacheAliasExampleDefinition(t)
	// Identical ref names with different bodies must not authorize replay.
	if definition.Expressions == nil {
		definition.Expressions = map[string]protocol.Expression{}
	}
	eventMapping := definition.Directions[protocol.DecodeEvent]
	definition.Expressions["cache_usage"] = *eventMapping.After
	eventMapping.After = &protocol.Expression{Op: "ref", Ref: "cache_usage"}
	definition.Directions[protocol.DecodeEvent] = eventMapping
	compiled := compileFixtureDefinition(t, definition)
	standard := compileFixtureDefinition(t, presetDefinition(t, "chat-completions-api"))
	different := compiled.Definition()
	different.ID = "different-usage-decoder-same-ref"
	different.Expressions["cache_usage"] = protocol.Expression{Op: "read"}
	differentCompiled := compileFixtureDefinition(t, different)
	if report := protocol.Verify(t.Context(), compiled); !report.Passed {
		t.Fatal(report.Issues)
	}
	for _, fixture := range []struct {
		usage string
		want  int64
	}{
		{`{"prompt_tokens":100,"provider_metrics":{"write_tokens":30,"keep":"extension"}}`, 30},
		{`{"prompt_tokens":100,"prompt_tokens_details":{"cache_write_tokens":0},"provider_metrics":{"write_tokens":30,"keep":"extension"}}`, 0},
		{`{"prompt_tokens":100}`, -1},
	} {
		body := []byte(`{"id":"r","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":` + fixture.usage + `}`)
		response, err := compiled.DecodeResponse(t.Context(), body, protocol.EvaluationContext{})
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
		wire, err := compiled.EncodeResponse(t.Context(), response, protocol.EvaluationContext{})
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
		frame := mustProtocolValue(t, `{"choices":[],"usage":`+fixture.usage+`}`)
		events, err := compiled.DecodeFrame(t.Context(), frame, protocol.EvaluationContext{State: protocol.NewEvaluationState()})
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
		if _, err := standard.EncodeFrame(t.Context(), events, protocol.EvaluationContext{State: protocol.NewEvaluationState()}); err == nil {
			t.Fatal("native replay bypassed the source's declared usage decoder")
		}
		if _, err := differentCompiled.EncodeFrame(t.Context(), events, protocol.EvaluationContext{State: protocol.NewEvaluationState()}); err == nil {
			t.Fatal("native replay compared ref names instead of their implementations")
		}
	}
}
