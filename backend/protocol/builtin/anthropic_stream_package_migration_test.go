package builtin

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicPackageSourceStreamMigration(t *testing.T) {
	raw, err := os.ReadFile("definitions/anthropic-messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var definition p.Definition
	if err := testValue(t, string(raw)).Decode(&definition); err != nil {
		t.Fatal(err)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy, issues := compiler.Compile(raw)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	candidate := compileBuiltinPackage(t, "anthropic")
	for _, sample := range definition.Samples {
		if sample.ID != "stream-text-decode" && sample.ID != "stream-tool-decode" && sample.ID != "stream-reasoning-decode" && sample.ID != "stream-encrypted-decode" {
			continue
		}
		t.Run(sample.ID, func(t *testing.T) {
			var frames []p.Value
			if err := sample.Input.Decode(&frames); err != nil {
				t.Fatal(err)
			}
			oldOptions := p.EvaluationContext{Scope: sample.Scope, State: p.NewEvaluationState()}
			newOptions := p.EvaluationContext{Scope: sample.Scope, State: p.NewEvaluationState()}
			defer oldOptions.State.Release()
			defer newOptions.State.Release()
			wire, err := candidate.NewWireInputStreamValidation(sample.Scope)
			if err != nil {
				t.Fatal(err)
			}
			var expected, actual []p.Event
			for _, frame := range frames {
				if err := wire.Consume(t.Context(), frame); err != nil {
					t.Fatal("new independent source contract", err)
				}
				before, err := legacy.DecodeFrame(t.Context(), frame, oldOptions)
				if err != nil {
					t.Fatal("existing source sample", err)
				}
				after, err := candidate.DecodeFrame(t.Context(), frame, newOptions)
				if err != nil {
					t.Fatal("declared source sample", err)
				}
				expected = append(expected, before.Events...)
				actual = append(actual, after.Events...)
			}
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
			before, err := p.EncodeValue(streamEventsAtVersion(expected, p.ProtocolCoreVersion))
			if err != nil {
				t.Fatal(err)
			}
			after, err := p.EncodeValue(actual)
			if err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatalf("stream events changed: %v\nold: %s\nnew: %s", err, before.JSONString(), after.JSONString())
			}
			if sample.ID == "stream-text-decode" || sample.ID == "stream-tool-decode" {
				want := encodeMigratedStream(t, Chat, expected, sample.Scope)
				got := encodeMigratedStream(t, Chat, streamEventsAtVersion(actual, p.SemanticSchemaVersion), sample.Scope)
				if !bytes.Equal(want.Bytes(), got.Bytes()) {
					t.Fatal("source migration changed the existing cross-protocol output", want.JSONString(), got.JSONString())
				}
			}
		})
	}
}

func TestAnthropicPackageSparseStreamIdentities(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Anthropic), compileBuiltinPackage(t, "anthropic")
	for _, fixture := range []struct{ name, message, index string }{
		{"missing-index", `"id":"r","model":"m"`, ""},
		{"null-index", `"id":"r","model":"m"`, `,"index":null`},
		{"missing-identity", "", `,"index":0`},
		{"null-identity", `"id":null,"model":null`, `,"index":0`},
		{"empty-identity", `"id":"","model":""`, `,"index":0`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			oldOptions := p.EvaluationContext{State: p.NewEvaluationState()}
			newOptions := p.EvaluationContext{State: p.NewEvaluationState()}
			defer oldOptions.State.Release()
			defer newOptions.State.Release()
			wire, err := candidate.NewWireInputStreamValidation(newOptions.Scope)
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range []string{
				fmt.Sprintf(`{"type":"message_start","message":{%s}}`, fixture.message),
				fmt.Sprintf(`{"type":"content_block_start"%s,"content_block":{"type":"text","text":""}}`, fixture.index),
				fmt.Sprintf(`{"type":"content_block_delta"%s,"delta":{"type":"text_delta","text":"hello"}}`, fixture.index),
				fmt.Sprintf(`{"type":"content_block_stop"%s}`, fixture.index),
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
				`{"type":"message_stop"}`,
			} {
				frame := testValue(t, raw)
				before, err := legacy.DecodeFrame(t.Context(), frame, oldOptions)
				if err != nil {
					t.Fatal("legacy boundary", err)
				}
				if err := wire.Consume(t.Context(), frame); err != nil {
					t.Fatal("declared boundary", err)
				}
				after, err := candidate.DecodeFrame(t.Context(), frame, newOptions)
				if err != nil {
					t.Fatal("declared mapping", err)
				}
				if len(before.Events) != len(after.Events) {
					t.Fatalf("event count changed: %d -> %d", len(before.Events), len(after.Events))
				}
				if len(before.Events) == 0 {
					continue
				}
				want, err := p.EncodeValue(streamEventsAtVersion(before.Events, candidate.CoreVersion()))
				if err != nil {
					t.Fatal(err)
				}
				got, err := p.EncodeValue(after.Events)
				if err != nil || !bytes.Equal(want.Bytes(), got.Bytes()) {
					t.Fatalf("identity normalization changed core events: %v\nold: %s\nnew: %s", err, want.JSONString(), got.JSONString())
				}
			}
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAnthropicPackageSignatureSource(t *testing.T) {
	legacy := shippedProjectionProtocol(t, Anthropic)
	candidate := compileBuiltinPackage(t, "anthropic")
	scope := p.Scope{Provider: "provider", Account: "account", Model: "m"}
	before := p.EvaluationContext{State: p.NewEvaluationState(), Scope: scope}
	after := p.EvaluationContext{State: p.NewEvaluationState(), Scope: scope}
	defer before.State.Release()
	defer after.State.Release()
	validation, err := candidate.NewWireInputStreamValidation(scope)
	if err != nil {
		t.Fatal(err)
	}
	var oldEvents, newEvents []p.Event
	for _, raw := range anthropicSignatureFrames() {
		frame := testValue(t, raw)
		oldFrame, err := legacy.DecodeFrame(t.Context(), frame, before)
		if err != nil {
			t.Fatal(err)
		}
		if err := validation.Consume(t.Context(), frame); err != nil {
			t.Fatal(err)
		}
		newFrame, err := candidate.DecodeFrame(t.Context(), frame, after)
		if err != nil || len(oldFrame.Events) != len(newFrame.Events) {
			t.Fatal("signature event boundary changed", newFrame, err)
		}
		oldEvents = append(oldEvents, oldFrame.Events...)
		newEvents = append(newEvents, newFrame.Events...)
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	want, _ := p.EncodeValue(streamEventsAtVersion(oldEvents, candidate.CoreVersion()))
	got, err := p.EncodeValue(newEvents)
	if err != nil {
		t.Fatal(err)
	}
	checkPackageMigration(t, want.Bytes(), got.Bytes(), nil, nil, packageMigrationGap{})
	wantFrames := encodeMigratedStream(t, Anthropic, oldEvents, scope)
	gotFrames := encodeMigratedStream(t, Anthropic, streamEventsAtVersion(newEvents, legacy.CoreVersion()), scope)
	if !bytes.Equal(wantFrames.Bytes(), gotFrames.Bytes()) {
		t.Fatal("source retention changed emitted signature deltas", wantFrames.JSONString(), gotFrames.JSONString())
	}
}

// Version is the reviewed core contract difference; payload and provenance
// remain in the comparison. Nested responses are copied before adjustment.
func streamEventsAtVersion(events []p.Event, version int) []p.Event {
	output := append([]p.Event(nil), events...)
	for index := range output {
		output[index].SchemaVersion = version
		if output[index].Response != nil {
			response := *output[index].Response
			response.SchemaVersion = version
			output[index].Response = &response
		}
	}
	return output
}

func encodeMigratedStream(t *testing.T, name string, events []p.Event, scope p.Scope) p.Value {
	t.Helper()
	target := testCompiled(t, name)
	options := p.EvaluationContext{Scope: scope, State: p.NewEvaluationState()}
	defer options.State.Release()
	var frames []p.Value
	for _, event := range events {
		encoded, err := target.EncodeFrames(t.Context(), event, options)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, encoded...)
	}
	tail, err := target.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	output, err := p.EncodeValue(append(frames, tail...))
	if err != nil {
		t.Fatal(err)
	}
	return output
}
