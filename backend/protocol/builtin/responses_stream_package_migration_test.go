package builtin

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

func responsesToolStreamSample(t *testing.T) p.Sample {
	t.Helper()
	raw, err := os.ReadFile("definitions/openai-responses.json")
	if err != nil {
		t.Fatal(err)
	}
	var definition p.Definition
	if err := testValue(t, string(raw)).Decode(&definition); err != nil {
		t.Fatal(err)
	}
	for _, sample := range definition.Samples {
		if sample.ID == "stream-tool-decode" {
			return sample
		}
	}
	t.Fatal("existing Responses JSON tool stream sample is missing")
	return p.Sample{}
}

func TestResponsesPackageToolSourceStream(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	sample := responsesToolStreamSample(t)
	var original []p.Value
	if err := sample.Input.Decode(&original); err != nil {
		t.Fatal(err)
	}
	for _, hasSequence := range []bool{true, false} {
		t.Run(fmt.Sprintf("sequence=%t", hasSequence), func(t *testing.T) {
			options := p.EvaluationContext{Scope: sample.Scope, State: p.NewEvaluationState()}
			defer options.State.Release()
			wire, err := compiled.NewWireInputStreamValidation(sample.Scope)
			if err != nil {
				t.Fatal(err)
			}
			target := compiled.Target(p.DecodeEvent, sample.Scope)
			target.Direction = p.EncodeEvent
			collector, err := p.NewResponseCollector(target, compiled.ResourceLimits())
			if err != nil {
				t.Fatal(err)
			}
			var events []p.Event
			for _, frame := range original {
				if !hasSequence {
					fields, err := frame.ReadObject()
					if err != nil {
						t.Fatal(err)
					}
					delete(fields, "sequence_number")
					frame, err = p.EncodeValue(fields)
					if err != nil {
						t.Fatal(err)
					}
				}
				decoded, err := wire.DecodeFrame(t.Context(), frame, options)
				if err != nil {
					t.Fatal("source contract and mapping", err)
				}
				assertResponsesToolTerminalOrigins(t, decoded.Events, frame)
				for _, event := range decoded.Events {
					if _, _, err := collector.Consume(event); err != nil {
						t.Fatal(err)
					}
				}
				events = append(events, decoded.Events...)
			}
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
			response, err := collector.Finish()
			if err != nil {
				t.Fatal(err)
			}
			kinds := []p.EventType{p.ResponseStarted, p.ItemStarted, p.ItemDelta, p.ItemSnapshot, p.ItemFinished, p.ResponseFinished}
			if len(events) != len(kinds) {
				t.Fatal("tool lifecycle duplicated or lost", len(events))
			}
			for index, event := range events {
				if event.Type != kinds[index] || event.Source != compiled.Identity() || event.SchemaVersion != compiled.CoreVersion() || !event.ResponseID.IsString("r") {
					t.Fatal("event identity or order changed", index, event)
				}
				if index > 0 && index < len(events)-1 && (!event.ItemID.IsString("output:0") || event.Index == nil || *event.Index != 0) {
					t.Fatal("tool association changed", event)
				}
			}
			for _, index := range []int{1, 3, 4} {
				node := events[index].Item
				if node == nil || node.Kind != p.ToolCallNode || !node.ID.IsString("r_item_0") || !node.CallID.IsString("call") || !node.Name.IsString("lookup") || node.Input == nil || node.Input.Kind != p.JSONInput {
					t.Fatal("tool item changed", node)
				}
				if node.Source == nil || node.Source.Path != "/item" || node.Source.Scope != sample.Scope {
					t.Fatal("tool lost its original source location", node.Source)
				}
			}
			if !events[2].Delta.IsString(`{"n":9007199254740993}`) || events[3].Item.Input.Value.JSONString() != `{"n":9007199254740993}` {
				t.Fatal("tool argument precision or spelling changed")
			}
			if len(response.Content) != 1 || response.Usage != nil || !response.Status.IsString("completed") || !response.Attributes["finishReason"].IsString("tool_calls") {
				t.Fatal("tool terminal or absent usage changed", response)
			}
			call := response.Content[0]
			if !call.CallID.IsString("call") || !call.Name.IsString("lookup") || call.Input == nil || call.Input.Value.JSONString() != `{"n":9007199254740993}` {
				t.Fatal("collected tool changed", call)
			}
		})
	}
}

func assertResponsesToolTerminalOrigins(t *testing.T, events []p.Event, frame p.Value) {
	t.Helper()
	for _, event := range events {
		if event.Type != p.ResponseFinished {
			continue
		}
		fields, err := frame.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		responseWire := fields["response"]
		responseFields, err := responseWire.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		var items []p.Value
		if err := responseFields["output"].Decode(&items); err != nil {
			t.Fatal(err)
		}
		response := event.Response
		if response == nil || response.Native != nil || len(response.Content) != len(items) {
			t.Fatal("terminal response changed frame ownership or output shape", response)
		}
		for index, node := range response.Content {
			path := fmt.Sprintf("/response/output/%d", index)
			expected := items[index]
			// capture_native keeps the selected fragment with ConfigStd's
			// container spelling, without decoding its numbers or fields.
			encoded, err := sonic.ConfigStd.Marshal(expected)
			if err != nil {
				t.Fatal(err)
			}
			expected, err = p.ParseValue(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if node.Native == nil || node.Native.Source.Path != path || node.Native.Value != expected || node.Source == nil || node.Source.Path != path {
				sourcePath, nativePath, captured := "<absent>", "<absent>", "<absent>"
				if node.Source != nil {
					sourcePath = node.Source.Path
				}
				if node.Native != nil {
					nativePath, captured = node.Native.Source.Path, node.Native.Value.JSONString()
				}
				t.Fatalf("tool provenance changed: expectedPath=%q sourcePath=%q nativePath=%q\ncaptured=%s\nexpected=%s", path, sourcePath, nativePath, captured, expected.JSONString())
			}
		}
	}
}

func TestResponsesPackageToolWireIdentityBoundaries(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	sample := responsesToolStreamSample(t)
	var original []p.Value
	if err := sample.Input.Decode(&original); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name, raw    string
		isEquivalent bool
	}{
		{"changed-id", `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"different","delta":"{}","sequence_number":2}`, false},
		{"equivalent-escape", `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"r_item_\u0030","delta":"{}","sequence_number":2}`, true},
		{"reused-id", `{"type":"response.output_item.added","output_index":1,"item":{"id":"r_item_0","type":"function_call","name":"other","call_id":"other","arguments":""},"sequence_number":2}`, false},
		{"mixed-id", `{"type":"response.output_item.added","output_index":1,"item_id":"frame","item":{"id":"item","type":"function_call","name":"other","call_id":"other","arguments":""},"sequence_number":2}`, false},
		{"null-id", `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":null,"delta":"{}","sequence_number":2}`, false},
		{"stale-sequence", `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"r_item_0","delta":"{}","sequence_number":1}`, false},
	} {
		raw := fixture.raw
		options := p.EvaluationContext{Scope: sample.Scope, State: p.NewEvaluationState()}
		wire, err := candidate.NewWireInputStreamValidation(sample.Scope)
		if err != nil {
			t.Fatal(err)
		}
		for _, frame := range original[:2] {
			if _, err := legacy.DecodeFrame(t.Context(), frame, options); err != nil {
				t.Fatal(err)
			}
			if err := wire.Consume(t.Context(), frame); err != nil {
				t.Fatal(err)
			}
		}
		_, oldErr := legacy.DecodeFrame(t.Context(), testValue(t, raw), options)
		newErr := wire.Consume(t.Context(), testValue(t, raw))
		options.State.Release()
		if fixture.isEquivalent {
			if oldErr == nil || newErr != nil {
				t.Fatal("typed wire identities must accept equivalent escapes while recording the legacy byte-comparison difference", oldErr, newErr)
			}
			continue
		}
		var before, after *p.ConversionError
		if !errors.As(oldErr, &before) || !errors.As(newErr, &after) || len(before.Issues) == 0 || len(after.Issues) == 0 {
			t.Fatal("identity boundary was accepted or lost its diagnostic", fixture.name, oldErr, newErr)
		}
		a, b := before.Issues[0], after.Issues[0]
		expectedCode, expectedStage := p.UpstreamContractViolation, "wire.stream"
		if fixture.name == "mixed-id" {
			expectedStage = "wire"
		}
		if b.Code != expectedCode || b.Stage != expectedStage || a.Path != b.Path || a.Protocol != b.Protocol || a.Direction != b.Direction {
			t.Fatal("identity rejection lost its declared boundary", fixture.name, a, b)
		}
	}
}
