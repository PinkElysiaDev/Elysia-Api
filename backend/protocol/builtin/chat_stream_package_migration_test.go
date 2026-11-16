package builtin

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

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

func TestChatLegacyAnnotationsCompareRawJSON(t *testing.T) {
	compiled := testCompiled(t, Chat)
	for _, hasText := range []bool{false, true} {
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
		defer options.State.Release()
		options, consume := bindFixtureOutput(t, compiled, options)
		prefix := []p.Event{
			{Type: p.ResponseStarted, Response: &p.Response{ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}}},
			{Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}},
		}
		if hasText {
			prefix = append(prefix, p.Event{Type: p.ItemDelta, ItemID: p.StringValue("text"), Delta: p.StringValue("OK")})
		}
		for _, event := range streamEventsAtVersion(prefix, compiled.CoreVersion()) {
			frames, err := compiled.EncodeFrames(t.Context(), event, options)
			if err != nil {
				t.Fatal(err)
			}
			consume(frames)
		}
		for index, event := range streamEventsAtVersion(chatAnnotationUpdates(t), compiled.CoreVersion()) {
			frames, err := compiled.EncodeFrames(t.Context(), event, options)
			if !hasText {
				if err == nil || len(frames) != 0 {
					t.Fatal("citation preceded the text range it references", frames, err)
				}
				break
			}
			if err != nil {
				t.Fatal(index, err)
			}
			consume(frames)
			want := 0
			if index == 0 {
				want = 1
			}
			if len(frames) != want {
				t.Fatal("equivalent citation snapshot was emitted again", index, len(frames))
			}
		}
	}
}

func TestChatLegacyMetadataRetentionBoundaries(t *testing.T) {
	const bufferLimit, deltaCount = 512, 8
	limits := p.DefaultLimits()
	limits.BufferBytes = bufferLimit
	compiled := compileLogprobBudget(t, Chat, limits)
	probabilities := testValue(t, `[{"token":"`+strings.Repeat("x", 96)+`","logprob":-0.1,"bytes":[120]}]`)
	for _, shouldCheckpoint := range []bool{false, true} {
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
		defer options.State.Release()
		budget := p.NewStreamBudget(limits)
		if err := budget.Bind(options.State); err != nil {
			t.Fatal(err)
		}
		events := []p.Event{
			{Type: p.ResponseStarted, Response: &p.Response{ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}}},
			{Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}},
		}
		for range deltaCount {
			events = append(events, p.Event{Type: p.ItemDelta, ItemID: p.StringValue("text"), Delta: p.StringValue("x"), Metadata: []p.ResponseMetadata{{Codec: Chat, SourceCodec: Chat, Location: "content", Name: "logprobs", Path: "/choices/0/logprobs", Value: probabilities, Update: "append"}}})
		}
		if shouldCheckpoint {
			events = append(events, p.Event{Type: p.ItemSnapshot, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}})
		}
		events = append(events,
			p.Event{Type: p.ItemFinished, ItemID: p.StringValue("text")},
			p.Event{Type: p.ResponseFinished, Response: &p.Response{Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}},
		)
		probabilityFrames := 0
		for index, event := range streamEventsAtVersion(events, compiled.CoreVersion()) {
			frames, err := compiled.EncodeFrames(t.Context(), event, options)
			if err != nil {
				t.Fatal(index, err)
			}
			if err := budget.Check(); err != nil {
				t.Fatal("delivered probabilities grew retained stream state", err)
			}
			for _, frame := range frames {
				if strings.Contains(frame.JSONString(), `"logprobs"`) {
					probabilityFrames++
				}
			}
		}
		tail, err := compiled.FinishEvents(t.Context(), options)
		if err != nil || len(tail) != 1 || strings.Contains(tail[0].JSONString(), "logprobs") || probabilityFrames != deltaCount {
			t.Fatal("digest tracking duplicated or dropped probability frames", probabilityFrames, tail, err)
		}
	}
}

func TestChatPackageTargetMetadataRejection(t *testing.T) {
	candidate := compileBuiltinPackage(t, "openai-chat")
	for _, fixture := range []struct {
		name, codec string
		hasItem     bool
		value       p.Value
	}{
		{"missing-value", Gemini, true, p.Value{}},
		{"missing-association", Chat, false, p.StringValue("v")},
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
			err := execute(candidate)
			var failure *p.ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) == 0 || failure.Issues[0].Direction != p.EncodeEvent {
				t.Fatal("invalid metadata requires a structured target rejection", err)
			}
		})
	}
}

func encodeChatMigrationEvents(t *testing.T, compiled *p.Compiled, events []p.Event, scope p.Scope, preference *p.ClientOutput) p.Value {
	t.Helper()
	frames, err := encodeMigrationTargetEvents(t, compiled, events, p.EvaluationContext{Scope: scope, ClientOutput: preference})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := p.EncodeValue(frames)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestChatPackageStreamReplaysOriginalFramesOnce(t *testing.T) {
	compiled := testCompiled(t, Chat)
	document, _ := compiled.Package()
	hasSequences := false
	for _, sample := range document.Mapping.Samples {
		if sample.Direction != p.DecodeEvent || !sample.Sequence || sample.ExpectedIssue != "" {
			continue
		}
		hasSequences = true
		t.Run(sample.ID, func(t *testing.T) {
			var frames []p.Value
			if err := sample.Input.Decode(&frames); err != nil {
				t.Fatal(err)
			}
			decode := fixtureOptions(p.EvaluationContext{Scope: sample.Scope, State: p.NewEvaluationState()})
			encode := fixtureOptions(p.EvaluationContext{Scope: sample.Scope, State: p.NewEvaluationState()})
			defer decode.State.Release()
			defer encode.State.Release()
			encode, consume := bindFixtureOutput(t, compiled, encode)
			for _, original := range frames {
				frame, err := compiled.DecodeFrame(t.Context(), original, decode)
				if err != nil {
					t.Fatal(err)
				}
				replayed, err := compiled.EncodeFrame(t.Context(), frame, encode)
				if err != nil || len(replayed) != 1 || replayed[0] != original {
					t.Fatal("native frame was not replayed exactly once", err, replayed)
				}
				consume(replayed)
			}
			if tail, err := compiled.FinishEvents(t.Context(), encode); err != nil || len(tail) != 0 {
				t.Fatal("native replay fabricated a terminal tail", err, tail)
			}
		})
	}
	if !hasSequences {
		t.Fatal("native stream evidence disappeared")
	}
}

func TestChatPackageNativeUsageRespectsClientPreference(t *testing.T) {
	candidate := compileBuiltinPackage(t, "openai-chat")
	include, exclude := true, false
	const first = `{"id":"r","object":"chat.completion.chunk","created":9007199254740993,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"A"},"finish_reason":null}],"usage":null}`
	const content = `{"id":"r","object":"chat.completion.chunk","created":9007199254740993,"model":"m","choices":[{"index":0,"delta":{"content":"B"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`
	for _, fixture := range []struct {
		name           string
		output         *p.ClientOutput
		replay         bool
		extension      bool
		nativeTerminal bool
	}{
		{name: "no-delivery-owner", replay: true},
		{name: "include", output: &p.ClientOutput{IncludeUsage: &include}, replay: true},
		{name: "exclude", output: &p.ClientOutput{IncludeUsage: &exclude}},
		{name: "owner-without-include", output: &p.ClientOutput{}},
		{name: "native-terminal-after-filtered-usage", output: &p.ClientOutput{IncludeUsage: &exclude}, nativeTerminal: true},
		{name: "unmapped-usage-extension", output: &p.ClientOutput{IncludeUsage: &exclude}, extension: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			decode := p.EvaluationContext{State: p.NewEvaluationState()}
			encode := p.EvaluationContext{State: p.NewEvaluationState(), ClientOutput: fixture.output}
			defer decode.State.Release()
			defer encode.State.Release()
			encode, consume := bindFixtureOutput(t, candidate, fixtureOptions(encode))
			second := content
			if fixture.extension {
				second = strings.TrimSuffix(second, "}") + `,"future":{"n":9007199254740993}}`
			}
			inputs := []string{first, second}
			if fixture.nativeTerminal {
				inputs[1] = strings.Replace(second, `"finish_reason":"stop"`, `"finish_reason":null`, 1)
				inputs = append(inputs, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
			}
			inputs = append(inputs, `{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`, `{"choices":[],"usage":{}}`)
			var output []p.Value
			for index, raw := range inputs {
				original := testValue(t, raw)
				frame, err := candidate.DecodeFrame(t.Context(), original, decode)
				if err != nil {
					t.Fatal("decode", index, err)
				}
				batch, err := candidate.EncodeFrame(t.Context(), frame, encode)
				if fixture.extension && index == 1 {
					var failure *p.ConversionError
					if !errors.As(err, &failure) || len(failure.Issues) == 0 || failure.Issues[0].Code != p.UnsupportedCapability || failure.Issues[0].Path != "/unmapped" {
						t.Fatal("unmapped extension must not disappear when usage prevents replay", err, batch)
					}
					return
				}
				if err != nil {
					t.Fatal("encode", index, err)
				}
				if (fixture.replay || index == 0) && (len(batch) != 1 || batch[0] != original) {
					t.Fatal("allowed native frame changed", index, batch)
				}
				output = append(output, batch...)
				consume(batch)
			}
			// Null usage remains native; even an empty usage object must obey
			// the delivery preference when a caller owns ClientOutput.
			tail, err := candidate.FinishEvents(t.Context(), encode)
			if err != nil || fixture.replay && len(tail) != 0 {
				t.Fatal("finish", err, tail)
			}
			output = append(output, tail...)
			consume(tail)
			var text strings.Builder
			terminals := 0
			for _, frame := range output {
				var fields struct {
					Created int64   `json:"created"`
					Usage   p.Value `json:"usage"`
					Choices []struct {
						Delta struct {
							Content string `json:"content"`
						} `json:"delta"`
						FinishReason p.Value `json:"finish_reason"`
					} `json:"choices"`
				}
				if err := frame.Decode(&fields); err != nil {
					t.Fatal(err)
				}
				if !fixture.replay && !fields.Usage.IsZero() && !fields.Usage.IsNull() {
					t.Fatal("caller received unrequested usage", frame.JSONString())
				}
				if fields.Created != 0 && fields.Created != 9007199254740993 {
					t.Fatal("native-to-semantic transition rounded the timestamp", frame.JSONString())
				}
				for _, choice := range fields.Choices {
					text.WriteString(choice.Delta.Content)
					if choice.FinishReason.IsString("stop") {
						terminals++
					}
				}
			}
			if text.String() != "AB" || terminals != 1 {
				t.Fatal("usage filtering changed content or terminal delivery", text.String(), terminals, output)
			}
		})
	}
}
