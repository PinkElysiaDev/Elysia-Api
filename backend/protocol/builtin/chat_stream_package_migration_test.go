package builtin

import (
	"bytes"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

func TestChatPackageSourceStreamMigration(t *testing.T) {
	legacy := shippedProjectionProtocol(t, Chat)
	candidate := compileBuiltinPackage(t, "openai-chat")
	samples := legacy.Definition().Samples
	for _, fixture := range []struct{ name, frames string }{
		{"metadata-and-probabilities", `[
			{"id":"r","model":"m","created":1,"service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":"x","annotations":[{"type":"url_citation","url_citation":{"url":"https://example.invalid","title":"fixture","start_index":0,"end_index":1}}]},"logprobs":{"content":[{"token":"x","logprob":-0.1,"bytes":[120]}]},"native_finish_reason":"provider_stop"}]},
			{"choices":[{"index":0,"delta":{"annotations":[]},"logprobs":{"content":[]},"finish_reason":"stop"}]}
		]`},
		{"late-usage-details", `[
			{"id":"r","model":"m","created":1,"choices":[{"index":0,"delta":{"role":"assistant","content":"x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":2,"total_tokens":22}},
			{"choices":[],"usage":{"prompt_tokens_details":{"cached_tokens":15}}},
			{"choices":[],"usage":{"prompt_cache_miss_tokens":5}},
			{"choices":[],"usage":{}}
		]`},
		{"late-tool-identity", `[
			{"id":"r","model":"m","created":1,"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"function":{"arguments":"{\"a\":"}}]}}]},
			{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call","function":{"name":"lookup","arguments":"1}"}}]},"finish_reason":"tool_calls"}]}
		]`},
	} {
		samples = append(samples, p.Sample{ID: fixture.name, Direction: p.DecodeEvent, Input: testValue(t, fixture.frames)})
	}
	for _, sample := range samples {
		if sample.Direction != p.DecodeEvent {
			continue
		}
		t.Run(sample.ID, func(t *testing.T) {
			var frames []p.Value
			if err := sample.Input.Decode(&frames); err != nil {
				t.Fatal(err)
			}
			oldOptions := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: p.NewEvaluationState()}
			newOptions := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: p.NewEvaluationState()}
			defer oldOptions.State.Release()
			defer newOptions.State.Release()
			wire, err := candidate.NewWireInputStreamValidation(sample.Scope)
			if err != nil {
				t.Fatal(err)
			}
			for index, frame := range frames {
				if err := wire.Consume(t.Context(), frame); err != nil {
					t.Fatal("independent source contract", index, err)
				}
				before, wantErr := legacy.DecodeFrame(t.Context(), frame, oldOptions)
				after, gotErr := candidate.DecodeFrame(t.Context(), frame, newOptions)
				if wantErr != nil || gotErr != nil {
					t.Fatal("source frame", index, wantErr, gotErr)
				}
				expected := streamEventsAtVersion(before.Events, p.ProtocolCoreVersion)
				if len(expected) != len(after.Events) {
					t.Fatal("source event count changed", index, expected, after.Events)
				}
				for eventIndex, event := range after.Events {
					if expected[eventIndex].Unmapped == nil {
						continue
					}
					// The approved provenance contract records an actual source
					// fragment instead of the old synthetic extensions object.
					// Nested snapshots use the existing ConfigStd compaction;
					// the transport frame has a separate byte-preserving owner.
					compact, err := sonic.ConfigStd.Marshal(frame)
					if err != nil {
						t.Fatal(err)
					}
					if event.Unmapped == nil || event.Unmapped.Source.Path != "" || !bytes.Equal(event.Unmapped.Value.Bytes(), compact) || event.Unmapped.Source.Protocol != candidate.Identity() || event.Unmapped.Source.Direction != p.DecodeEvent || event.Unmapped.Source.Scope != sample.Scope {
						t.Fatal("extension evidence lost its original frame", event.Unmapped)
					}
					expected[eventIndex].Unmapped = event.Unmapped
				}
				want, err := p.EncodeValue(expected)
				if err != nil {
					t.Fatal(err)
				}
				got, err := p.EncodeValue(after.Events)
				if err != nil || !bytes.Equal(want.Bytes(), got.Bytes()) {
					t.Fatalf("frame %d changed events: %v\nold: %s\nnew: %s", index, err, want.JSONString(), got.JSONString())
				}
			}
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChatPackageTargetStreamMigration(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Chat), compileBuiltinPackage(t, "openai-chat")
	include, exclude := true, false
	for _, sample := range legacy.Definition().Samples {
		if sample.Direction != p.EncodeEvent {
			continue
		}
		var events []p.Event
		if err := sample.Input.Decode(&events); err != nil {
			t.Fatal(err)
		}
		for _, preference := range []struct {
			name   string
			output *p.ClientOutput
		}{{"unspecified", nil}, {"without-include", &p.ClientOutput{}}, {"include", &p.ClientOutput{IncludeUsage: &include}}, {"exclude", &p.ClientOutput{IncludeUsage: &exclude}}} {
			t.Run(sample.ID+"/"+preference.name, func(t *testing.T) {
				before := encodeChatMigrationEvents(t, legacy, events, sample.Scope, preference.output)
				after := encodeChatMigrationEvents(t, candidate, streamEventsAtVersion(events, p.ProtocolCoreVersion), sample.Scope, preference.output)
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("target frames changed", before.JSONString(), after.JSONString())
				}
			})
		}
	}
}

func encodeChatMigrationEvents(t *testing.T, compiled *p.Compiled, events []p.Event, scope p.Scope, preference *p.ClientOutput) p.Value {
	t.Helper()
	options := p.EvaluationContext{Scope: scope, ClientOutput: preference, State: p.NewEvaluationState()}
	defer options.State.Release()
	validation, err := compiled.NewWireStreamValidation(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, isPackage := compiled.Package()
	if isPackage {
		options = options.WithOutputStream(validation)
	}
	var frames []p.Value
	consume := func(batch []p.Value) {
		t.Helper()
		for _, frame := range batch {
			if err := validation.Consume(t.Context(), frame); err != nil {
				t.Fatal("target wire rejected", err, frame.JSONString())
			}
		}
		if isPackage && len(batch) > 0 {
			if err := validation.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
		}
		frames = append(frames, batch...)
	}
	for _, event := range events {
		batch, err := compiled.EncodeFrames(t.Context(), event, options)
		if err != nil {
			t.Fatal(event.Type, err)
		}
		consume(batch)
	}
	tail, err := compiled.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	consume(tail)
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	encoded, err := p.EncodeValue(frames)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestChatPackageStreamReplaysOriginalFramesOnce(t *testing.T) {
	legacy := shippedProjectionProtocol(t, Chat)
	candidate := compileBuiltinPackage(t, "openai-chat")
	for _, sample := range legacy.Definition().Samples {
		if sample.Direction != p.DecodeEvent {
			continue
		}
		t.Run(sample.ID, func(t *testing.T) {
			var frames []p.Value
			if err := sample.Input.Decode(&frames); err != nil {
				t.Fatal(err)
			}
			decode := p.EvaluationContext{Scope: sample.Scope, State: p.NewEvaluationState()}
			encode := p.EvaluationContext{Scope: sample.Scope, State: p.NewEvaluationState()}
			defer decode.State.Release()
			defer encode.State.Release()
			for _, original := range frames {
				frame, err := candidate.DecodeFrame(t.Context(), original, decode)
				if err != nil {
					t.Fatal(err)
				}
				replayed, err := candidate.EncodeFrame(t.Context(), frame, encode)
				if err != nil || len(replayed) != 1 || replayed[0] != original {
					t.Fatal("native frame was not replayed exactly once", err, replayed)
				}
			}
			if tail, err := candidate.FinishEvents(t.Context(), encode); err != nil || len(tail) != 0 {
				t.Fatal("native replay fabricated a terminal tail", err, tail)
			}
		})
	}
}
