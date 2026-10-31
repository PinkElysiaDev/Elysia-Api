package builtin

import (
	"bytes"
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
	legacy, candidate := shippedProjectionProtocol(t, Responses), compileBuiltinPackage(t, "openai-responses")
	sample := responsesToolStreamSample(t)
	var original []p.Value
	if err := sample.Input.Decode(&original); err != nil {
		t.Fatal(err)
	}
	for _, hasSequence := range []bool{true, false} {
		t.Run(fmt.Sprintf("sequence=%t", hasSequence), func(t *testing.T) {
			oldOptions := p.EvaluationContext{Scope: sample.Scope, State: p.NewEvaluationState()}
			newOptions := p.EvaluationContext{Scope: sample.Scope, State: p.NewEvaluationState()}
			defer oldOptions.State.Release()
			defer newOptions.State.Release()
			wire, err := candidate.NewWireInputStreamValidation(sample.Scope)
			if err != nil {
				t.Fatal(err)
			}
			var expected, actual []p.Event
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
				before, err := legacy.DecodeFrame(t.Context(), frame, oldOptions)
				if err != nil {
					t.Fatal("legacy source sample", err)
				}
				if err := wire.Consume(t.Context(), frame); err != nil {
					t.Fatal("independent source contract", err)
				}
				after, err := candidate.DecodeFrame(t.Context(), frame, newOptions)
				if err != nil {
					t.Fatal("declared source sample", err)
				}
				assertResponsesToolTerminalOrigins(t, before.Events, frame, false)
				assertResponsesToolTerminalOrigins(t, after.Events, frame, true)
				expected = append(expected, before.Events...)
				actual = append(actual, after.Events...)
			}
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
			want, err := p.EncodeValue(responsesToolEventsAtOriginalLocations(t, expected))
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.EncodeValue(actual)
			if err != nil || !bytes.Equal(want.Bytes(), got.Bytes()) {
				t.Fatalf("tool source events changed: %v\nold: %s\nnew: %s", err, want.JSONString(), got.JSONString())
			}
			oldWire := encodeMigratedStream(t, Chat, expected, sample.Scope)
			newWire := encodeMigratedStream(t, Chat, streamEventsAtVersion(actual, p.SemanticSchemaVersion), sample.Scope)
			if !bytes.Equal(oldWire.Bytes(), newWire.Bytes()) {
				t.Fatal("Responses JSON tool migration changed Chat output", oldWire.JSONString(), newWire.JSONString())
			}
		})
	}
}

// The reviewed representation changes are core v2 and true /response envelope
// locations. Every payload, event and remaining provenance field stays compared.
func responsesToolEventsAtOriginalLocations(t *testing.T, events []p.Event) []p.Event {
	t.Helper()
	result := streamEventsAtVersion(events, p.ProtocolCoreVersion)
	for index := range result {
		response := result[index].Response
		if result[index].Type != p.ResponseFinished || response == nil {
			continue
		}
		if response.Native != nil {
			t.Fatal("legacy stream response unexpectedly owns a second native envelope")
		}
		response.Content = append([]p.Node(nil), response.Content...)
		for position := range response.Content {
			node := &response.Content[position]
			path := fmt.Sprintf("/output/%d", position)
			if node.Kind != p.ToolCallNode || node.Native == nil || node.Native.Source.Path != path || node.Source == nil || node.Source.Path != path {
				t.Fatal("unexpected legacy tool provenance", node)
			}
			origin := *node.Native
			origin.Source.Path = "/response" + origin.Source.Path
			node.Native = &origin
			source := *node.Source
			source.Path = "/response" + source.Path
			node.Source = &source
		}
	}
	return result
}

func assertResponsesToolTerminalOrigins(t *testing.T, events []p.Event, frame p.Value, isPackage bool) {
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
		prefix := ""
		if isPackage {
			prefix = "/response"
		}
		response := event.Response
		if response == nil || response.Native != nil || len(response.Content) != len(items) {
			t.Fatal("terminal response changed frame ownership or output shape", isPackage, response)
		}
		for index, node := range response.Content {
			path := fmt.Sprintf("%s/output/%d", prefix, index)
			expected := items[index]
			if isPackage {
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
			}
			if node.Native == nil || node.Native.Source.Path != path || node.Native.Value != expected || node.Source == nil || node.Source.Path != path {
				sourcePath, nativePath, captured := "<absent>", "<absent>", "<absent>"
				if node.Source != nil {
					sourcePath = node.Source.Path
				}
				if node.Native != nil {
					nativePath, captured = node.Native.Source.Path, node.Native.Value.JSONString()
				}
				t.Fatalf("tool provenance changed: package=%t expectedPath=%q sourcePath=%q nativePath=%q\ncaptured=%s\nexpected=%s", isPackage, path, sourcePath, nativePath, captured, expected.JSONString())
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
	for _, raw := range []string{
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"different","delta":"{}","sequence_number":2}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"r_item_\u0030","delta":"{}","sequence_number":2}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"r_item_0","type":"function_call","name":"other","call_id":"other","arguments":""},"sequence_number":2}`,
		`{"type":"response.output_item.added","output_index":1,"item_id":"frame","item":{"id":"item","type":"function_call","name":"other","call_id":"other","arguments":""},"sequence_number":2}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":null,"delta":"{}","sequence_number":2}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"r_item_0","delta":"{}","sequence_number":1}`,
	} {
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
		var before, after *p.ConversionError
		if !errors.As(oldErr, &before) || !errors.As(newErr, &after) || len(before.Issues) == 0 || len(after.Issues) == 0 {
			t.Fatal("identity boundary was accepted or lost its diagnostic", oldErr, newErr)
		}
		a, b := before.Issues[0], after.Issues[0]
		if a.Code != b.Code || a.Stage != b.Stage || a.Path != b.Path || a.Protocol != b.Protocol || a.Direction != b.Direction {
			t.Fatal("identity rejection classification changed", a, b)
		}
	}
}
