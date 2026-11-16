package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func encodeMigrationTargetEvents(t *testing.T, compiled *p.Compiled, events []p.Event, options p.EvaluationContext) ([]p.Value, error) {
	t.Helper()
	validation, err := compiled.NewWireStreamValidation(options.Scope)
	if err != nil {
		return nil, err
	}
	defer validation.Release()
	options.State = p.NewEvaluationState()
	options = fixtureOptions(options.WithOutputStream(validation))
	defer options.State.Release()
	var output []p.Value
	consume := func(frames []p.Value) error {
		for _, frame := range frames {
			if err := validation.Consume(t.Context(), frame); err != nil {
				return err
			}
		}
		if len(frames) > 0 {
			if err := validation.CommitDelivery(); err != nil {
				return err
			}
		}
		output = append(output, frames...)
		return nil
	}
	for index, event := range events {
		frames, err := compiled.EncodeFrames(t.Context(), event, options)
		if err != nil {
			return output, fmt.Errorf("event %d %s: %w", index, event.Type, err)
		}
		if err := consume(frames); err != nil {
			return output, err
		}
	}
	tail, err := compiled.FinishEvents(t.Context(), options)
	if err != nil {
		return output, err
	}
	if err := consume(tail); err != nil {
		return output, err
	}
	return output, validation.Finish()
}

func TestAnthropicPackageTargetStreamMigration(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, sample := range packageMapping(compiled).Samples {
		if sample.Direction != p.EncodeEvent {
			continue
		}
		t.Run(sample.ID, func(t *testing.T) {
			var events []p.Event
			if err := sample.Input.Decode(&events); err != nil {
				t.Fatal(err)
			}
			events[0].Response.Usage = nil
			if frames, err := encodeMigrationTargetEvents(t, compiled, events, p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}); err == nil || len(frames) != 0 {
				t.Fatal("unprepared start bypassed the required usage envelope", frames, err)
			}
			events = prepareDeclaredTargetUsage(t, compiled, events, sample.Scope)
			frames, err := encodeMigrationTargetEvents(t, compiled, events, p.EvaluationContext{Scope: sample.Scope, Values: sample.Context})
			if err != nil {
				t.Fatal(err)
			}
			actual, err := p.EncodeValue(frames)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, actual.Bytes(), sample.Expected.JSONString())
			if sample.ID == "declared-stream-text-encode" {
				events[1].Item.Kind = p.ReasoningNode
				frames, err := encodeMigrationTargetEvents(t, compiled, events, p.EvaluationContext{Scope: sample.Scope, Values: sample.Context})
				if err != nil {
					t.Fatal(err)
				}
				unsigned, err := p.EncodeValue(frames)
				if err != nil {
					t.Fatal(err)
				}
				expected := strings.NewReplacer(`"text_delta"`, `"thinking_delta"`, `"text"`, `"thinking"`).Replace(sample.Expected.JSONString())
				sameJSON(t, unsigned.Bytes(), expected)
			}
		})
	}
}

func TestAnthropicPackageTargetStreamContinuations(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	for _, fixture := range []struct {
		name, sample string
		transform    func([]p.Event) []p.Event
	}{
		{"late-tool-identity", "declared-stream-tool-encode", func(events []p.Event) []p.Event {
			ready := events[1]
			node := *ready.Item
			ready.Type, ready.Item = p.ItemSnapshot, &node
			ready.Item.Input = &p.ToolInput{Kind: p.JSONInput}
			events[1].Item.Name, events[1].Item.CallID = p.Value{}, p.Value{}
			return append(events[:3:3], append([]p.Event{ready}, events[3:]...)...)
		}},
		{"text-snapshots", "declared-stream-text-encode", func(events []p.Event) []p.Event {
			snapshot := events[2]
			snapshot.Type, snapshot.Delta = p.ItemSnapshot, p.Value{}
			snapshot.Item = &p.Node{Kind: p.TextNode, Payload: p.StringValue("hi")}
			growth := snapshot
			growth.Item = &p.Node{Kind: p.TextNode, Payload: p.StringValue("hi!")}
			return append(events[:3:3], append([]p.Event{snapshot, growth}, events[3:]...)...)
		}},
		{"automatic-tool-close", "declared-stream-tool-encode", func(events []p.Event) []p.Event {
			return append(events[:3:3], events[4:]...)
		}},
		{"inferred-tool-finish", "declared-stream-tool-encode", func(events []p.Event) []p.Event {
			events[len(events)-1].Response.Attributes = nil
			return events
		}},
		{"late-usage-metadata", "declared-stream-text-encode", func(events []p.Event) []p.Event {
			events[len(events)-1].Metadata = []p.ResponseMetadata{{Codec: Anthropic, Location: "usage", Name: "service_tier", Path: "/usage/service_tier", Value: p.StringValue("standard")}}
			return append(events, p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.MetadataUpdated, ResponseID: p.StringValue("r"), Metadata: []p.ResponseMetadata{{Codec: Anthropic, Location: "usage", Name: "service_tier", Path: "/usage/service_tier", Value: p.StringValue("priority")}}})
		}},
		{"frame-metadata", "declared-stream-text-encode", func(events []p.Event) []p.Event {
			events[2].Metadata = []p.ResponseMetadata{{Codec: Anthropic, Location: "event:content_block_delta", Name: "extension", Path: "/extension", Value: testValue(t, `{"n":9007199254740993,"label":"\u0078"}`)}}
			return events
		}},
		{"terminal-metadata", "declared-stream-text-encode", func(events []p.Event) []p.Event {
			events[4].Metadata = []p.ResponseMetadata{{Codec: Anthropic, Location: "event:message_delta", Name: "extension", Path: "/extension", Value: testValue(t, `{"n":9007199254740993}`)}}
			return events
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var sample p.Sample
			for _, entry := range packageMapping(compiled).Samples {
				if entry.ID == fixture.sample {
					sample = entry
					break
				}
			}
			var events []p.Event
			if err := sample.Input.Decode(&events); err != nil {
				t.Fatal(err)
			}
			events = fixture.transform(events)
			frames, err := encodeMigrationTargetEvents(t, compiled, events, p.EvaluationContext{Scope: sample.Scope, Values: sample.Context})
			if err != nil {
				t.Fatal(err)
			}
			checkAnthropicContinuationFrames(t, frames, fixture.name, fixture.sample)
		})
	}
}

func checkAnthropicContinuationFrames(t *testing.T, frames []p.Value, name, sample string) {
	t.Helper()
	var text, arguments strings.Builder
	starts, stops, terminals, extensions := 0, 0, 0, 0
	for _, frame := range frames {
		fields, _ := frame.ReadObject()
		if !fields["extension"].IsZero() {
			extensions++
			want := `{"n":9007199254740993}`
			kind := "message_delta"
			if name == "frame-metadata" {
				want = `{"n":9007199254740993,"label":"\u0078"}`
				kind = "content_block_delta"
			}
			if !fields["type"].IsString(kind) {
				t.Fatal("frame metadata moved to another event", fields)
			}
			sameJSON(t, fields["extension"].Bytes(), want)
		}
		switch {
		case fields["type"].IsString("content_block_start"):
			starts++
			block, _ := fields["content_block"].ReadObject()
			if sample == "declared-stream-tool-encode" && (!block["id"].IsString("call") || !block["name"].IsString("lookup")) {
				t.Fatal("deferred tool lost identity", block)
			}
		case fields["type"].IsString("content_block_delta"):
			delta, _ := fields["delta"].ReadObject()
			if !delta["text"].IsZero() {
				part, _ := stringValue(delta["text"])
				text.WriteString(part)
			}
			if !delta["partial_json"].IsZero() {
				part, _ := stringValue(delta["partial_json"])
				arguments.WriteString(part)
			}
		case fields["type"].IsString("content_block_stop"):
			stops++
		case fields["type"].IsString("message_delta"):
			terminals++
			delta, _ := fields["delta"].ReadObject()
			if sample == "declared-stream-tool-encode" && !delta["stop_reason"].IsString("tool_use") {
				t.Fatal("tool completion lost its stop reason", delta)
			}
			if name == "late-usage-metadata" {
				usage, _ := fields["usage"].ReadObject()
				if !usage["service_tier"].IsString("priority") {
					t.Fatal("late usage snapshot did not replace its owner", usage)
				}
			}
		}
	}
	if starts != 1 || stops != 1 || terminals != 1 {
		t.Fatal("continuation duplicated or omitted a lifecycle boundary", starts, stops, terminals)
	}
	if (name == "frame-metadata" || name == "terminal-metadata") && extensions != 1 {
		t.Fatal("owned frame metadata disappeared or duplicated", frames)
	}
	if sample == "declared-stream-tool-encode" {
		sameJSON(t, []byte(arguments.String()), `{"n":9007199254740993}`)
	} else {
		want := "hi"
		if name == "text-snapshots" {
			want = "hi!"
		}
		if text.String() != want {
			t.Fatal("snapshot repeated or lost text", text.String())
		}
	}
}

func TestChatPackageTargetStreamMigration(t *testing.T) {
	compiled := testCompiled(t, Chat)
	include, exclude := true, false
	for _, sample := range packageMapping(compiled).Samples {
		if sample.Direction != p.EncodeEvent {
			continue
		}
		for _, preference := range []struct {
			name   string
			output *p.ClientOutput
		}{{"unspecified", nil}, {"without-include", &p.ClientOutput{}}, {"include", &p.ClientOutput{IncludeUsage: &include}}, {"exclude", &p.ClientOutput{IncludeUsage: &exclude}}} {
			t.Run(sample.ID+"/"+preference.name, func(t *testing.T) {
				var events []p.Event
				if err := sample.Input.Decode(&events); err != nil {
					t.Fatal(err)
				}
				actual := encodeChatMigrationEvents(t, compiled, events, sample.Scope, preference.output)
				var expected []p.Value
				if err := sample.Expected.Decode(&expected); err != nil {
					t.Fatal(err)
				}
				if preference.name == "exclude" || preference.name == "without-include" {
					kept := expected[:0]
					for _, frame := range expected {
						fields, _ := frame.ReadObject()
						if fields["usage"].IsZero() {
							kept = append(kept, frame)
						}
					}
					expected = kept
				}
				want, err := p.EncodeValue(expected)
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, actual.Bytes(), want.JSONString())
			})
		}
	}
}

func TestChatPackageTargetMetadataMigration(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, fixture := range []struct {
		name     string
		updates  []p.Event
		terminal []p.ResponseMetadata
	}{
		{name: "annotation-raw-spelling", updates: chatAnnotationUpdates(t)},
		{name: "native-stop-and-usage", terminal: []p.ResponseMetadata{{Codec: Chat, Location: "choice", Name: "native_finish_reason", Path: "/choices/0/native_finish_reason", Value: p.StringValue("provider_stop")}}},
		{name: "metadata-only-annotation", updates: []p.Event{{Type: p.MetadataUpdated, ItemID: p.StringValue("text"), Metadata: []p.ResponseMetadata{{Codec: Chat, Location: "message", Name: "annotations", Path: "/choices/0/delta/annotations", Value: testValue(t, citationChat)}}}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			events := []p.Event{
				{Type: p.ResponseStarted, Response: &p.Response{ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}, Metadata: []p.ResponseMetadata{{Codec: Chat, Location: "response", Name: "service_tier", Path: "/service_tier", Value: p.StringValue("auto")}}}},
				{Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}},
				{Type: p.ItemDelta, ItemID: p.StringValue("text"), Delta: p.StringValue("OK")},
			}
			events = append(events, fixture.updates...)
			events = append(events,
				p.Event{Type: p.ItemFinished, ItemID: p.StringValue("text")},
				p.Event{Type: p.ResponseFinished, Metadata: fixture.terminal, Response: &p.Response{Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}, Metadata: []p.ResponseMetadata{{Codec: Chat, Location: "response", Name: "service_tier", Path: "/service_tier", Value: p.StringValue("default")}}}},
				p.Event{Type: p.UsageUpdated, Usage: &p.Usage{Input: &p.Counter{Count: 2, Origin: p.ObservedCount}, Output: &p.Counter{Count: 1, Origin: p.ObservedCount}}},
			)
			actual := encodeChatMigrationEvents(t, compiled, streamEventsAtVersion(events, p.ProtocolCoreVersion), p.Scope{}, nil)
			var frames []p.Value
			if err := actual.Decode(&frames); err != nil {
				t.Fatal(err)
			}
			var tiers []p.Value
			var text strings.Builder
			annotations, terminals, usages := 0, 0, 0
			for _, frame := range frames {
				fields, _ := frame.ReadObject()
				if !fields["service_tier"].IsZero() {
					tiers = append(tiers, fields["service_tier"])
				}
				if !fields["usage"].IsZero() {
					usages++
					sameJSON(t, fields["usage"].Bytes(), `{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}`)
				}
				var choices []p.Object
				if err := fields["choices"].Decode(&choices); err != nil {
					t.Fatal(err)
				}
				for _, choice := range choices {
					delta, _ := choice["delta"].ReadObject()
					if !delta["content"].IsZero() {
						part, err := stringValue(delta["content"])
						if err != nil {
							t.Fatal(err)
						}
						text.WriteString(part)
					}
					if !delta["annotations"].IsZero() {
						annotations++
						sameJSON(t, delta["annotations"].Bytes(), citationChat)
					}
					if choice["finish_reason"].IsString("stop") {
						terminals++
						if fixture.name == "native-stop-and-usage" && !choice["native_finish_reason"].IsString("provider_stop") {
							t.Fatal("native terminal metadata disappeared", choice)
						}
					}
				}
			}
			wantAnnotations := 1
			if fixture.name == "native-stop-and-usage" {
				wantAnnotations = 0
			}
			if text.String() != "OK" || annotations != wantAnnotations || terminals != 1 || usages != 1 || len(tiers) < 2 || !tiers[0].IsString("auto") || !tiers[len(tiers)-1].IsString("default") {
				t.Fatal("target metadata lost its owner, snapshot or terminal", actual)
			}
		})
	}
}
