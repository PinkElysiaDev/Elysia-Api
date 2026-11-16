package builtin

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func targetLogprobEvents(t *testing.T, codec, ending string) []p.Event {
	t.Helper()
	metadata := p.ResponseMetadata{Codec: codec, SourceCodec: codec, Location: "content", Name: "logprobs", Value: testValue(t, auditLogprobs), Update: "append"}
	events := []p.Event{
		{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted, Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}}},
		{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode, ID: p.StringValue("text")}},
	}
	for index := range 9 {
		next := metadata
		next.Path = fmt.Sprintf("/probabilities/%d", index)
		if ending == "wrong-codec" && index == 5 {
			next.Codec = "unsupported"
		}
		text := "x"
		if index%3 == 0 {
			text = "" // Metadata-only events must still emit immediately.
		}
		events = append(events, p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemDelta, ItemID: p.StringValue("text"), Delta: p.StringValue(text), Metadata: []p.ResponseMetadata{next}})
	}
	if ending == "cancel" {
		return append(events, p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.OperationCancelled})
	}
	if ending == "explicit" {
		events = append(events, p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemFinished, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode, Payload: p.StringValue("xxxxxx")}})
	}
	events = append(events,
		p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}},
		p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.UsageUpdated, Usage: &p.Usage{Input: &p.Counter{Count: 7, Origin: p.ObservedCount}, Output: &p.Counter{Count: 0, Origin: p.ObservedCount}}},
	)
	return events
}

func TestTargetDeferredLogprobsPreserveFramesAndBudget(t *testing.T) {
	for _, codec := range []string{Chat, Responses} {
		for _, limit := range []int{256, 1024, 8192} {
			for _, stateItems := range []int{2, 4096} {
				limits := p.DefaultLimits()
				limits.BufferBytes, limits.StateItems = limit, stateItems
				compiled := compileLogprobBudget(t, codec, limits)
				conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
				if err != nil {
					t.Fatal(err)
				}
				route := p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Transport: p.SSE}
				for _, ending := range []string{"explicit", "implicit", "cancel", "wrong-codec"} {
					t.Run(fmt.Sprintf("%s/%d/%d/%s", codec, limit, stateItems, ending), func(t *testing.T) {
						events := targetLogprobEvents(t, codec, ending)
						before, _ := p.EncodeValue(events)
						options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
						defer options.State.Release()
						options, consume := bindFixtureOutput(t, compiled, options)
						var output []string
						var failure error
						for _, event := range events {
							projected, err := conversion.Event(t.Context(), event, route, nil)
							var frames []p.Value
							if err == nil {
								frames, err = compiled.EncodeFrames(t.Context(), projected, options)
							}
							if err != nil {
								failure = err
								break
							}
							consume(frames)
							if event.Type == p.ItemDelta && len(frames) == 0 {
								t.Fatal("probability fragment was delayed until completion")
							}
							for _, frame := range frames {
								output = append(output, frame.JSONString())
								if event.Type == p.ItemDelta && !strings.Contains(frame.JSONString(), `"logprobs"`) {
									t.Fatal("immediate delta lost its probabilities", frame)
								}
							}
						}
						if failure == nil {
							frames, err := compiled.FinishEvents(t.Context(), options)
							failure = err
							if err == nil {
								consume(frames)
								for _, frame := range frames {
									output = append(output, frame.JSONString())
								}
							}
						}
						if failure != nil {
							var rejected *p.ConversionError
							if !errors.As(failure, &rejected) || len(rejected.Issues) == 0 {
								t.Fatal("unstructured stream rejection", failure)
							}
							if limit == 8192 && stateItems == 4096 && ending != "wrong-codec" {
								t.Fatal("valid stream exceeded its original budget", failure)
							}
							if rejected.Issues[0].Code != p.LimitExceeded && ending != "wrong-codec" {
								t.Fatal("small budget concealed another failure", failure)
							}
						} else if ending == "wrong-codec" {
							t.Fatal("foreign probability owner was accepted")
						} else if ending != "cancel" {
							_, response := decodePackageStream(t, testCompiled(t, codec), output, p.Scope{})
							texts := fixtureNodes(response.Content, p.TextNode)
							if len(texts) != 1 || !texts[0].Payload.IsString("xxxxxx") {
								t.Fatal("probability stream changed text", response.Content)
							}
							assertLogprobCount(t, texts[0], 9)
						}
						after, _ := p.EncodeValue(events)
						if before != after {
							t.Fatal("target changed caller-owned events")
						}
					})
				}
			}
		}
	}
}

func compileLogprobBudget(t *testing.T, codec string, limits p.Limits) *p.Compiled {
	t.Helper()
	document, ok := testCompiled(t, codec).Package()
	if !ok {
		t.Fatal("missing authored package")
	}
	// This fixture restricts stream state independently of activation sample count.
	document.Mapping.Samples, document.Mapping.Limits = nil, &limits
	document.Mapping.ModelSamples = nil
	document.Mapping.TokenCountSamples = nil
	document.Mapping.TaskSamples = nil
	document.Mapping.SessionSamples = nil
	if document.Mapping.Agent != nil {
		document.Mapping.Agent.Samples = nil
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func assertLogprobCount(t *testing.T, node p.Node, count int) {
	t.Helper()
	for _, metadata := range node.Metadata {
		if metadata.Name == "logprobs" {
			var values []p.Value
			err := metadata.Value.Decode(&values)
			if err != nil || len(values) != count {
				t.Fatal("probability count changed", count, metadata.Value, err)
			}
			return
		}
	}
	t.Fatal("probabilities disappeared", node)
}

func TestResponsesClosesItemsBeforeUsageTail(t *testing.T) {
	const probabilityCount = 9
	expectedUsage := map[string]int{"input_tokens": 7, "output_tokens": 0, "total_tokens": 7}
	compiled := testCompiled(t, Responses)
	for _, ending := range []string{"explicit", "implicit"} {
		t.Run(ending, func(t *testing.T) {
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer options.State.Release()
			validation, err := compiled.NewWireStreamValidation(options.Scope)
			if err != nil {
				t.Fatal(err)
			}
			defer validation.Release()
			options = options.WithOutputStream(validation)
			var completedItems []string
			consume := func(frames []p.Value) {
				for _, frame := range frames {
					if err := validation.Consume(t.Context(), frame); err != nil {
						t.Fatal(err)
					}
					kind := checkLogprobCompletion(t, frame, probabilityCount)
					if kind != "" && kind != "response.completed" {
						completedItems = append(completedItems, kind)
					}
				}
				if len(frames) > 0 {
					if err := validation.CommitDelivery(); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, event := range targetLogprobEvents(t, Responses, ending) {
				frames, err := compiled.EncodeFrames(t.Context(), event, options)
				if err != nil {
					t.Fatal(event.Type, err)
				}
				consume(frames)
				if event.Type == p.ResponseFinished && strings.Join(completedItems, ",") != "response.output_text.done,response.content_part.done,response.output_item.done" {
					t.Fatal("item completion waited for finalization or repeated", completedItems)
				}
			}
			frames, err := compiled.FinishEvents(t.Context(), options)
			if err != nil || len(frames) != 1 {
				t.Fatal("finalization repeated item completion", len(frames), err)
			}
			consume(frames)
			fields, _ := frames[0].ReadObject()
			usage := fixturePointerValue(t, frames[0].Bytes(), "/response/usage")
			var counters map[string]int
			if !fields["type"].IsString("response.completed") || usage == nil || testValue(t, *usage).Decode(&counters) != nil || !maps.Equal(counters, expectedUsage) {
				t.Fatal("final response lost its usage tail", frames[0])
			}
			if err := validation.Finish(); err != nil {
				t.Fatal(err)
			}
			if frames, err := compiled.FinishEvents(t.Context(), options); err != nil || len(frames) != 0 {
				t.Fatal("finalization emitted twice", frames, err)
			}
		})
	}
}

func checkLogprobCompletion(t *testing.T, frame p.Value, count int) string {
	t.Helper()
	paths := map[string]string{
		"response.output_text.done":  "/logprobs",
		"response.content_part.done": "/part/logprobs",
		"response.output_item.done":  "/item/content/0/logprobs",
		"response.completed":         "/response/output/0/content/0/logprobs",
	}
	fields, err := frame.ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	kind, err := stringValue(fields["type"])
	if err != nil {
		t.Fatal(err)
	}
	path, hasProbabilities := paths[kind]
	if !hasProbabilities {
		return ""
	}
	probabilities := fixturePointerValue(t, frame.Bytes(), path)
	var entries []p.Value
	if probabilities == nil || testValue(t, *probabilities).Decode(&entries) != nil || len(entries) != count {
		t.Fatal("completion lost probability snapshots", kind)
	}
	return kind
}

func TestTargetLogprobSnapshotsKeepParallelItems(t *testing.T) {
	compiled := testCompiled(t, Responses)
	events := targetLogprobEvents(t, Responses, "implicit")[:1]
	for _, key := range []string{"left", "right"} {
		events = append(events, p.Event{Type: p.ItemStarted, ItemID: p.StringValue(key), Item: &p.Node{Kind: p.TextNode, ID: p.StringValue(key)}})
	}
	for index := range 4 {
		for _, key := range []string{"left", "right"} {
			metadata := p.ResponseMetadata{Codec: Responses, SourceCodec: Responses, Location: "content", Name: "logprobs", Path: "/" + key, Value: testValue(t, auditLogprobs), Update: "append"}
			event := p.Event{Type: p.ItemDelta, ItemID: p.StringValue(key), Delta: p.StringValue("x"), Metadata: []p.ResponseMetadata{metadata}}
			if index == 3 && key == "left" {
				metadata.Value, metadata.Update = testValue(t, `[]`), ""
				event.Type, event.Delta, event.Metadata = p.ItemSnapshot, p.Value{}, nil
				event.Item = &p.Node{Kind: p.TextNode, Payload: p.StringValue("xxx"), Metadata: []p.ResponseMetadata{metadata}}
			}
			events = append(events, event)
		}
	}
	events = append(events, p.Event{Type: p.ResponseFinished, Response: &p.Response{Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}})
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	options, consume := bindFixtureOutput(t, compiled, options)
	var raws []string
	appendFrames := func(frames []p.Value) {
		consume(frames)
		for _, frame := range frames {
			raws = append(raws, frame.JSONString())
		}
	}
	for _, event := range streamEventsAtVersion(events, compiled.CoreVersion()) {
		frames, err := compiled.EncodeFrames(t.Context(), event, options)
		if event.Type == p.ItemSnapshot {
			// Already delivered probabilities cannot be retracted. The rejected
			// snapshot must leave both item histories available for completion.
			if err == nil || len(frames) != 0 {
				t.Fatal("empty snapshot retracted delivered probabilities", frames, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(event.Type, err)
		}
		appendFrames(frames)
	}
	frames, err := compiled.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	appendFrames(frames)
	_, response := decodePackageStream(t, compiled, raws, p.Scope{})
	texts := fixtureNodes(response.Content, p.TextNode)
	if len(texts) != 2 || !texts[0].Payload.IsString("xxx") || !texts[1].Payload.IsString("xxxx") {
		t.Fatal("parallel snapshots changed their sibling", response.Content)
	}
	assertLogprobCount(t, texts[0], 3)
	assertLogprobCount(t, texts[1], 4)
}
