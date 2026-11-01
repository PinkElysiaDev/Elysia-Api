package builtin

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

func TestChatLegacyMetadataRetentionBoundaries(t *testing.T) {
	const bufferLimit, deltaCount = 512, 8
	limits := p.DefaultLimits()
	limits.BufferBytes = bufferLimit
	probabilities := testValue(t, `{"content":[{"token":"`+strings.Repeat("x", 96)+`","logprob":-0.1,"bytes":[120]}]}`)
	for _, shouldCheckpoint := range []bool{false, true} {
		name := "finish-without-item"
		if shouldCheckpoint {
			name = "later-item-checkpoint"
		}
		t.Run(name, func(t *testing.T) {
			instance, err := (module{name: Chat}).NewStream(p.EncodeEvent, limits)
			if err != nil {
				t.Fatal(err)
			}
			stream := instance.(*streamModule)
			encode := func(event p.Event) {
				t.Helper()
				if _, err := stream.EncodeEventFrames(t.Context(), event, p.EvaluationContext{}); err != nil {
					t.Fatal(event.Type, err)
				}
			}
			encode(p.Event{Type: p.ResponseStarted, Response: &p.Response{ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}}})
			encode(p.Event{Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}})
			for range deltaCount {
				encode(p.Event{Type: p.ItemDelta, ItemID: p.StringValue("text"), Delta: p.StringValue("x"), Metadata: []p.ResponseMetadata{{Codec: Chat, Location: "choice", Name: "logprobs", Path: "/choices/0/logprobs", Value: probabilities}}})
			}
			if shouldCheckpoint {
				_, err := stream.EncodeEventFrames(t.Context(), p.Event{Type: p.ItemSnapshot, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}}, p.EvaluationContext{})
				var failure *p.ConversionError
				if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != p.UnsupportedCapability || failure.Issues[0].Path != "/items" {
					t.Fatal("legacy accumulated metadata checkpoint changed", err)
				}
				t.Log("later Item snapshot rejects accumulated logprobs at /items")
				return
			}
			encode(p.Event{Type: p.ItemFinished, ItemID: p.StringValue("text")})
			encode(p.Event{Type: p.ResponseFinished, Response: &p.Response{Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}})
			tail, err := stream.Finish(t.Context(), p.EvaluationContext{})
			if err != nil || len(tail) != 1 || strings.Contains(tail[0].JSONString(), "logprobs") {
				t.Fatal("Chat finish unexpectedly checks or emits a cumulative probability snapshot", err, tail)
			}
			for _, item := range stream.items {
				metadata, err := p.EncodeValue(item.node.Metadata)
				if err != nil || metadata.Len() <= bufferLimit {
					t.Fatal("fixture did not cross the retained metadata boundary", metadata.Len(), err)
				}
				t.Logf("finish accepted %d bytes of accumulated metadata with BufferBytes=%d; no probability tail", metadata.Len(), bufferLimit)
			}
		})
	}
}

func TestChatLegacyAnnotationsCompareRawJSON(t *testing.T) {
	instance, err := (module{name: Chat}).NewStream(p.EncodeEvent, p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	stream := instance.(*streamModule)
	for _, event := range []p.Event{
		{Type: p.ResponseStarted, Response: &p.Response{ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}}},
		{Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}},
	} {
		if _, err := stream.EncodeEventFrames(t.Context(), event, p.EvaluationContext{}); err != nil {
			t.Fatal(err)
		}
	}
	for index, event := range chatAnnotationUpdates(t) {
		frames, err := stream.EncodeEventFrames(t.Context(), event, p.EvaluationContext{})
		want := 0
		if index == 0 || index == 2 {
			want = 1
		}
		if err != nil || len(frames) != want {
			t.Fatal("raw annotation delivery changed", index, len(frames), err)
		}
		t.Logf("annotation update %d emitted %d frame(s)", index, len(frames))
	}
}

func TestChatPackageSourceStreamMigration(t *testing.T) {
	candidate := compileBuiltinPackage(t, "openai-chat")
	document, _ := candidate.Package()
	samples := append([]p.Sample(nil), document.Mapping.Samples...)
	for _, fixture := range []struct{ name, frames string }{
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
			var input []p.Value
			if err := sample.Input.Decode(&input); err != nil {
				t.Fatal(err)
			}
			options := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context, State: p.NewEvaluationState()}
			defer options.State.Release()
			wire, err := candidate.NewWireInputStreamValidation(sample.Scope)
			if err != nil {
				t.Fatal(err)
			}
			target := candidate.Target(p.DecodeEvent, sample.Scope)
			target.Direction = p.EncodeEvent
			collector, err := p.NewResponseCollector(target, candidate.ResourceLimits())
			if err != nil {
				t.Fatal(err)
			}
			var frames []*p.EventFrame
			parents, closed, children := 0, 0, 0
			for index, original := range input {
				frame, err := wire.DecodeFrame(t.Context(), original, options)
				if err != nil {
					t.Fatal("source frame", index, err)
				}
				frames = append(frames, frame)
				for _, event := range frame.Events {
					if event.ItemID.IsString("choice:0") {
						if !event.ParentID.IsZero() || event.Index == nil || *event.Index != 0 {
							t.Fatal("choice lost its top-level identity", event)
						}
						if event.Type == p.ItemStarted {
							parents++
							if event.Item == nil || event.Item.Kind != p.MessageNode || !event.Item.Role.IsString("assistant") || event.Item.Source == nil || event.Item.Source.Path != "/choices/0" {
								t.Fatal("choice message lacks actual wire provenance", event)
							}
						}
						if event.Type == p.ItemFinished {
							closed++
						}
					} else if event.Type == p.ItemStarted || event.Type == p.ItemDelta || event.Type == p.ItemSnapshot || event.Type == p.ItemFinished {
						if !event.ParentID.IsString("choice:0") || event.Index == nil || *event.Index <= 0 {
							t.Fatal("choice child lost its parent or position", event)
						}
						if event.Type == p.ItemStarted {
							children++
						}
					}
					if event.Unmapped != nil {
						compact, err := sonic.ConfigStd.Marshal(original)
						if err != nil {
							t.Fatal(err)
						}
						if event.Unmapped.Source.Path != "" || !bytes.Equal(event.Unmapped.Value.Bytes(), compact) || event.Unmapped.Source.Protocol != candidate.Identity() || event.Unmapped.Source.Direction != p.DecodeEvent || event.Unmapped.Source.Scope != sample.Scope {
							t.Fatal("extension evidence lost its original frame", event.Unmapped)
						}
					}
					if _, _, err := collector.Consume(event); err != nil {
						t.Fatal("source lifecycle", index, err)
					}
				}
			}
			if err := wire.Finish(); err != nil {
				t.Fatal(err)
			}
			response, err := collector.Finish()
			if err != nil {
				t.Fatal(err)
			}
			if parents != 1 || closed != 1 || len(response.Content) != 1 || response.Content[0].Kind != p.MessageNode || len(response.Content[0].Children) != children || children != 1 || !response.Status.IsString("completed") {
				t.Fatal("choice lifecycle or content grouping changed", parents, closed, response)
			}
			node := response.Content[0].Children[0]
			switch sample.ID {
			case "declared-stream-tool-decode", "late-tool-identity":
				expected := `{"n":9007199254740993}`
				if sample.ID == "late-tool-identity" {
					expected = `{"a":1}`
				}
				if node.Kind != p.ToolCallNode || !node.Name.IsString("lookup") || !node.CallID.IsString("call") || node.Input == nil || node.Input.Kind != p.JSONInput {
					t.Fatal("tool history lost its identity", node)
				}
				sameJSON(t, node.Input.Value.Bytes(), expected)
			default:
				expected, kind := "hi", p.TextNode
				if sample.ID == "late-usage-details" {
					expected = "x"
				}
				if sample.ID == "declared-stream-reasoning-decode" {
					kind = p.ReasoningNode
				}
				if node.Kind != kind || !node.Payload.IsString(expected) {
					t.Fatal("generated content changed", node)
				}
				if response.Usage == nil || response.Usage.Input == nil || response.Usage.Input.Count != 20 || response.Usage.Output == nil || response.Usage.Output.Count != 2 || response.Usage.CacheRead == nil || response.Usage.CacheRead.Count != 15 {
					t.Fatal("late usage lost its reported counters", response.Usage)
				}
			}
			replayed := encodePackageStream(t, candidate, frames, sample.Scope, true)
			if len(replayed) != len(input) {
				t.Fatal("source frame replay changed frame count", len(replayed), len(input))
			}
			for index, original := range input {
				if replayed[index] != original.JSONString() {
					t.Fatal("native frame bytes changed", index)
				}
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

func TestChatPackageTargetMetadataMigration(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Chat), compileBuiltinPackage(t, "openai-chat")
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
				// Final wire validation associates annotations with text already delivered.
				{Type: p.ItemDelta, ItemID: p.StringValue("text"), Delta: p.StringValue("OK")},
			}
			events = append(events, fixture.updates...)
			events = append(events,
				p.Event{Type: p.ItemFinished, ItemID: p.StringValue("text")},
				p.Event{Type: p.ResponseFinished, Metadata: fixture.terminal, Response: &p.Response{Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": testValue(t, `"stop"`)}, Metadata: []p.ResponseMetadata{{Codec: Chat, Location: "response", Name: "service_tier", Path: "/service_tier", Value: p.StringValue("default")}}}},
				p.Event{Type: p.UsageUpdated, Usage: &p.Usage{Input: &p.Counter{Count: 2, Origin: p.ObservedCount}, Output: &p.Counter{Count: 1, Origin: p.ObservedCount}}},
			)
			want := encodeChatMigrationEvents(t, legacy, streamEventsAtVersion(events, p.SemanticSchemaVersion), p.Scope{}, nil)
			got := encodeChatMigrationEvents(t, candidate, streamEventsAtVersion(events, p.ProtocolCoreVersion), p.Scope{}, nil)
			if !bytes.Equal(want.Bytes(), got.Bytes()) {
				t.Fatal("metadata delivery or ordering changed", want.JSONString(), got.JSONString())
			}
		})
	}
}

func chatAnnotationUpdates(t *testing.T) []p.Event {
	t.Helper()
	first := testValue(t, citationChat)
	spaced := testValue(t, strings.Replace(citationChat, ":", ": ", 1))
	if first == spaced {
		t.Fatal("fixture requires different raw JSON spellings")
	}
	updates := make([]p.Event, 0, 4)
	for _, value := range []p.Value{first, first, spaced, spaced} {
		updates = append(updates, p.Event{Type: p.ItemDelta, ItemID: p.StringValue("text"), Delta: p.StringValue(""), Metadata: []p.ResponseMetadata{{Codec: Chat, Location: "message", Name: "annotations", Path: "/choices/0/delta/annotations", Value: value}}})
	}
	return updates
}

func TestChatPackageTargetMetadataFailureOrder(t *testing.T) {
	legacy, candidate := shippedProjectionProtocol(t, Chat), compileBuiltinPackage(t, "openai-chat")
	for _, fixture := range []struct {
		name, codec string
		hasItem     bool
		value       p.Value
	}{
		{"codec-before-missing-value", Gemini, true, p.Value{}},
		{"association-before-owner", Chat, false, p.StringValue("v")},
		{"unsupported-owner", Chat, true, p.StringValue("v")},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			execute := func(compiled *p.Compiled) error {
				options := p.EvaluationContext{State: p.NewEvaluationState()}
				defer options.State.Release()
				start := p.Event{SchemaVersion: compiled.CoreVersion(), Type: p.ResponseStarted, Response: &p.Response{SchemaVersion: compiled.CoreVersion(), ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}}}
				current := start
				if fixture.hasItem {
					for _, event := range []p.Event{start, {SchemaVersion: compiled.CoreVersion(), Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}}} {
						if _, err := compiled.EncodeFrames(t.Context(), event, options); err != nil {
							t.Fatal("fixture setup", err)
						}
					}
					current = p.Event{SchemaVersion: compiled.CoreVersion(), Type: p.ItemDelta, ItemID: p.StringValue("text"), Delta: p.StringValue("x")}
				}
				current.Metadata = []p.ResponseMetadata{{Codec: fixture.codec, Location: "future", Name: "unknown", Path: "/metadata/first", Value: fixture.value}}
				_, err := compiled.EncodeFrames(t.Context(), current, options)
				return err
			}
			wantErr, gotErr := execute(legacy), execute(candidate)
			var want, got *p.ConversionError
			if !errors.As(wantErr, &want) || !errors.As(gotErr, &got) || len(want.Issues) != 1 || len(got.Issues) != 1 {
				t.Fatal("metadata rejection changed", wantErr, gotErr)
			}
			before, after := want.Issues[0], got.Issues[0]
			if before.Code != after.Code || before.Direction != after.Direction || before.Stage != after.Stage || before.Path != after.Path || before.Reason != after.Reason || before.Suggestion != after.Suggestion {
				t.Fatal("metadata first failure changed", before, after)
			}
		})
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
