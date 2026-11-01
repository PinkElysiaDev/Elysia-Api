package builtin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

const (
	progressStart = `{"type":"response.created","sequence_number":0,"response":{"id":"r","model":"m","status":"in_progress","output":[]}}`
	progressEnd   = `{"type":"response.completed","sequence_number":10,"response":{"id":"r","model":"m","status":"completed","output":[]}}`
)

func TestResponsesPackageProgressSourceStream(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	for _, fixture := range []struct {
		name   string
		frames []string
	}{
		{"hosted-usage", hostedUsageStreamFrames()},
		{"partial-model-usage", []string{progressStart,
			`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","usage":{"input_tokens":3,"output_tokens":4}}}`,
			`{"type":"response.in_progress","sequence_number":2,"response":{"id":"r","usage":{"output_tokens_details":{"reasoning_tokens":2},"input_tokens_details":{"cached_tokens":1}}}}`, progressEnd}},
		{"created-does-not-seed-progress", []string{
			`{"type":"response.created","sequence_number":0,"response":{"id":"r","model":"m","status":"in_progress","output":[],"usage":{"output_tokens":1}}}`,
			`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","usage":{"output_tokens_details":{"reasoning_tokens":2}}}}`, progressEnd}},
		{"usage-tails", []string{progressStart, progressEnd,
			`{"type":"response.in_progress","sequence_number":11,"response":{"id":"r","usage":{"output_tokens":4}}}`,
			`{"type":"response.in_progress","sequence_number":12,"response":{"id":"r","usage":{"output_tokens_details":{"reasoning_tokens":2}}}}`}},
		{"empty-control", []string{progressStart, `{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","usage":null}}`, progressEnd}},
		{"empty-tail", []string{progressStart, progressEnd, `{"type":"response.in_progress","sequence_number":11,"response":{"id":"r"}}`}},
		{"metadata-only", []string{progressStart, `{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","tool_usage":{}}}`, progressEnd}},
		{"empty-tool-containers", []string{progressStart,
			`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","tool_usage":null}}`,
			`{"type":"response.in_progress","sequence_number":2,"response":{"id":"r","tool_usage":{"image_gen":{"input_tokens_details":{}},"web_search":{}}}}`, progressEnd}},
		{"metadata-before-created", []string{`{"type":"response.in_progress","response":{"metadata":{"label":"pending"}}}`, progressStart, progressEnd}},
		{"usage-metadata-tail", []string{progressStart, progressEnd, `{"type":"response.in_progress","sequence_number":11,"response":{"id":"r","usage":{},"service_tier":"default"}}`}},
		{"ordinary-update-does-not-decode-output", []string{progressStart,
			`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","output":[],"error":{"message":"not a failure event"},"usage":{"input_tokens":3}}}`, progressEnd}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if fixture.name == "hosted-usage" {
				_, response := decodePackageStream(t, candidate, fixture.frames, p.Scope{})
				if response.Usage == nil || len(response.Usage.Details) != 8 || response.Usage.Total == nil || response.Usage.Total.Count != 4 || response.Usage.Details["tools.web_search_calls"].Count != 2 || response.Usage.Details["tools.image_generation.total_tokens"].Count != 12 {
					t.Fatal("hosted progress counted snapshots twice or changed model totals", response.Usage)
				}
				if len(response.Content) != 1 || response.Content[0].Kind != p.MessageNode || len(response.Content[0].Children) != 1 || !response.Content[0].Children[0].Payload.IsString("OK") {
					t.Fatal("hosted progress lost generated output", response.Content)
				}
				return
			}
			oldOptions := p.EvaluationContext{State: p.NewEvaluationState()}
			newOptions := p.EvaluationContext{State: p.NewEvaluationState()}
			defer oldOptions.State.Release()
			defer newOptions.State.Release()
			wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			target := candidate.Target(p.DecodeEvent, p.Scope{})
			target.Direction = p.EncodeEvent
			collector, err := p.NewResponseCollector(target, candidate.ResourceLimits())
			if err != nil {
				t.Fatal(err)
			}
			for index, raw := range fixture.frames {
				value := testValue(t, raw)
				before, err := legacy.DecodeFrame(t.Context(), value, oldOptions)
				if err != nil {
					t.Fatal("legacy progress", index, err)
				}
				after, err := wire.DecodeFrame(t.Context(), value, newOptions)
				if err != nil {
					t.Fatal("declared progress", index, err)
				}
				assertResponsesControlObservations(t, candidate, before.Events, after.Events)
				for _, event := range after.Events {
					if _, _, err := collector.Consume(event); err != nil {
						t.Fatal("progress semantic collection", index, err)
					}
				}
			}
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
			response, err := collector.Finish()
			if err != nil || len(response.Content) != 0 || !response.ID.IsString("r") || !response.Model.IsString("m") || !response.Status.IsString("completed") {
				t.Fatal("progress changed the empty completed response", response, err)
			}
			switch fixture.name {
			case "partial-model-usage":
				if response.Usage == nil || response.Usage.Input == nil || response.Usage.Input.Count != 3 || response.Usage.Output == nil || response.Usage.Output.Count != 4 || response.Usage.CacheRead == nil || response.Usage.CacheRead.Count != 1 || response.Usage.Details["output.reasoning_tokens"].Count != 2 {
					t.Fatal("partial usage fields were lost", response.Usage)
				}
			case "usage-tails":
				if response.Usage == nil || response.Usage.Input != nil || response.Usage.Total != nil || response.Usage.Output == nil || response.Usage.Output.Count != 4 || response.Usage.Details["output.reasoning_tokens"].Count != 2 {
					t.Fatal("usage tail was lost or invented input/total counters", response.Usage)
				}
			case "usage-metadata-tail":
				usage := response.Usage
				if usage != nil && (usage.Input != nil || usage.Output != nil || usage.Total != nil || usage.CacheRead != nil || usage.CacheCreation != nil || len(usage.Details) != 0) {
					t.Fatal("usage metadata tail invented token counters", usage)
				}
				hasServiceTier := false
				for _, metadata := range response.Metadata {
					if metadata.Codec == Responses && metadata.Location == "response" && metadata.Name == "service_tier" {
						hasServiceTier = metadata.Value.IsString("default")
					}
				}
				if !hasServiceTier {
					t.Fatal("usage metadata tail lost its observed service tier", response.Metadata)
				}
			case "created-does-not-seed-progress", "ordinary-update-does-not-decode-output":
				if response.Usage == nil {
					t.Fatal("an observed partial usage value disappeared")
				}
			default:
				if response.Usage != nil {
					t.Fatal("empty metadata invented usage", response.Usage)
				}
			}
		})
	}
}

func TestResponsesPackageProgressReplacesLargeUsageField(t *testing.T) {
	const fieldBytes = 4 << 20
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	oldOptions := p.EvaluationContext{State: p.NewEvaluationState()}
	newOptions := p.EvaluationContext{State: p.NewEvaluationState()}
	defer oldOptions.State.Release()
	defer newOptions.State.Release()
	replay, err := p.NewEventReplay(legacy.Target(p.EncodeEvent, p.Scope{}), legacy.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	// Each update fits the 8 MiB wire/state budget. Replacing the same field
	// must not charge an intermediate array containing both complete values.
	for index, raw := range []string{
		progressStart,
		`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","usage":{"vendor":"` + strings.Repeat("x", fieldBytes) + `"}}}`,
		`{"type":"response.in_progress","sequence_number":2,"response":{"id":"r","usage":{"vendor":"` + strings.Repeat("y", fieldBytes) + `"}}}`,
		progressEnd,
	} {
		value := testValue(t, raw)
		before, err := legacy.DecodeFrame(t.Context(), value, oldOptions)
		if err != nil {
			t.Fatal("legacy source", index, err)
		}
		for _, event := range before.Events {
			if _, err := replay.Consume(event); err != nil {
				t.Fatal("legacy lifecycle", index, err)
			}
		}
		if err := wire.Consume(t.Context(), value); err != nil {
			t.Fatal("independent source contract", index, err)
		}
		after, err := candidate.DecodeFrame(t.Context(), value, newOptions)
		if err != nil {
			t.Fatal("declared source", index, err)
		}
		if index == 1 || index == 2 {
			expected, err := p.EncodeValue(value)
			if err != nil {
				t.Fatal(err)
			}
			if len(before.Events) != 1 || len(after.Events) != 1 || after.Events[0].Type != before.Events[0].Type || after.Events[0].Unmapped == nil || after.Events[0].Unmapped.Value != expected {
				t.Fatal("usage replacement lost its current unknown field", index)
			}
		}
	}
	if err := replay.Finish(); err != nil {
		t.Fatal("legacy EOF", err)
	}
	if err := wire.Finish(); err != nil {
		t.Fatal("declared EOF", err)
	}
}

func TestResponsesPackageProgressRejectsInvalidUpdates(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	for _, fixture := range []struct {
		name   string
		prefix []string
		frame  string
	}{
		{"identity-and-usage", nil, `{"type":"response.in_progress","sequence_number":0,"response":{"id":"other","usage":{"input_tokens":null}}}`},
		{"sequence-and-usage", nil, `{"type":"response.in_progress","sequence_number":0,"response":{"id":"r","usage":{"input_tokens":null}}}`},
		{"model-and-hosted-usage", nil, `{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","usage":{"input_tokens":null},"tool_usage":{"web_search":{"num_requests":null}}}}`},
		{"cumulative-and-hosted-usage", []string{`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","usage":{"output_tokens":2}}}`},
			`{"type":"response.in_progress","sequence_number":2,"response":{"id":"r","usage":{"output_tokens_details":{"reasoning_tokens":3}},"tool_usage":{"web_search":{"num_requests":null}}}}`},
		{"hosted-counter", nil, `{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","tool_usage":{"web_search":{"num_requests":-1}}}}`},
		{"nonarray-output", nil, `{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","output":42}}`},
		{"invalid-usage-tail", []string{progressEnd}, `{"type":"response.in_progress","sequence_number":11,"response":{"id":"r","usage":{"output_tokens":null}}}`},
		{"metadata-after-terminal", []string{progressEnd}, `{"type":"response.in_progress","sequence_number":11,"response":{"id":"r","metadata":{"label":"late"}}}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			wire, err := compiled.NewWireInputStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range append([]string{progressStart}, fixture.prefix...) {
				if err := wire.Consume(t.Context(), testValue(t, raw)); err != nil {
					t.Fatal("valid progress prefix was rejected", err)
				}
			}
			value := testValue(t, fixture.frame)
			issue := requireResponsesWireRejection(t, compiled, wire.Consume(t.Context(), value))
			// Several independent faults are present. The reported location must
			// belong to the original frame; their rejection order is not a contract.
			if issue.Path != "/" && (!strings.HasPrefix(issue.Path, "/") || migrationPointerValue(t, value.Bytes(), issue.Path) == nil) {
				t.Fatal("progress rejection points outside the original frame", issue)
			}
		})
	}
}

func TestResponsesPackageProgressRequiresRealTerminal(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	wire, err := compiled.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{progressStart, `{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","status":"completed","usage":{"input_tokens":1}}}`} {
		if err := wire.Consume(t.Context(), testValue(t, raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err := wire.Finish(); err == nil {
		t.Fatal("an in_progress status or EOF invented the response terminal")
	}
}

func TestResponsesPackageProgressPreservesExtensions(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	for _, raw := range []string{
		`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","created":9007199254740993}}`,
		`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","usage":{"prompt_cache_hit_tokens":9007199254740993}}}`,
		`{"type":"response.in_progress","sequence_number":1,"response":{"id":"r","tool_usage":{"vendor":{"unit":9007199254740993}}}}`,
	} {
		beforeOptions := p.EvaluationContext{State: p.NewEvaluationState()}
		afterOptions := p.EvaluationContext{State: p.NewEvaluationState()}
		defer beforeOptions.State.Release()
		defer afterOptions.State.Release()
		wire, err := candidate.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		start := testValue(t, progressStart)
		if _, err := legacy.DecodeFrame(t.Context(), start, beforeOptions); err != nil {
			t.Fatal(err)
		}
		if err := wire.Consume(t.Context(), start); err != nil {
			t.Fatal(err)
		}
		if _, err := candidate.DecodeFrame(t.Context(), start, afterOptions); err != nil {
			t.Fatal(err)
		}
		value := testValue(t, raw)
		before, err := legacy.DecodeFrame(t.Context(), value, beforeOptions)
		if err != nil || len(before.Events) != 1 || before.Events[0].Unmapped == nil {
			t.Fatal("legacy fixture must expose the extension", before, err)
		}
		if err := wire.Consume(t.Context(), value); err != nil {
			t.Fatal(err)
		}
		after, err := candidate.DecodeFrame(t.Context(), value, afterOptions)
		if err != nil || len(after.Events) != 1 || after.Events[0].Type != before.Events[0].Type || after.Events[0].Unmapped == nil {
			t.Fatal("progress discarded an unknown field", after, err)
		}
		expected, err := sonic.ConfigStd.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		unmapped := after.Events[0].Unmapped
		if unmapped.Source.Path != "" || unmapped.Source.Protocol != candidate.Identity() || unmapped.Source.Direction != p.DecodeEvent || !bytes.Equal(unmapped.Value.Bytes(), expected) {
			t.Fatal("progress extension lost its original frame location or bytes", unmapped)
		}
	}
}
