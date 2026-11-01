package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatPackageMessageStream(t *testing.T) {
	source, target := compileBuiltinPackage(t, "openai-responses"), compileBuiltinPackage(t, "openai-chat")
	document, _ := source.Package()
	var raws []string
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	for _, sample := range document.Mapping.Samples {
		if sample.ID != "declared-stream-text-decode" {
			continue
		}
		var values []p.Value
		if err := sample.Input.Decode(&values); err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			raws = append(raws, value.JSONString())
		}
	}
	if len(raws) == 0 {
		t.Fatal("missing plain Responses stream fixture")
	}
	frames, _ := decodePackageStream(t, source, raws, scope)
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(target, source))
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Transport: p.SSE, Scope: scope}
	converted := make([]*p.EventFrame, len(frames))
	for index, frame := range frames {
		converted[index] = &p.EventFrame{Events: make([]p.Event, len(frame.Events))}
		for i, event := range frame.Events {
			mapped, err := conversion.Event(t.Context(), event, route, nil)
			if err != nil {
				t.Fatal(err)
			}
			converted[index].Events[i] = mapped
		}
	}
	wire := encodePackageStream(t, target, converted, scope, false)
	_, response := decodePackageStream(t, target, wire, scope)
	if len(response.Content) != 1 || response.Content[0].Kind != p.MessageNode || len(response.Content[0].Children) != 1 || !response.Content[0].Children[0].Payload.IsString("hi") || response.Usage == nil || response.Usage.Input == nil || response.Usage.Input.Count != 20 || response.Usage.Output == nil || response.Usage.Output.Count != 2 {
		t.Fatal("message container lost its text or usage", response)
	}
}

func TestChatPackageDirectMessageAssociations(t *testing.T) {
	target := compileBuiltinPackage(t, "openai-chat")
	start := p.Event{SchemaVersion: target.CoreVersion(), Type: p.ResponseStarted, ResponseID: p.StringValue("r"), Response: &p.Response{SchemaVersion: target.CoreVersion(), ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("in_progress"), Attributes: p.Object{"created_at": testValue(t, `1`)}}}
	parent := p.Event{SchemaVersion: target.CoreVersion(), Type: p.ItemStarted, ItemID: p.StringValue("message"), Item: &p.Node{Kind: p.MessageNode, Role: p.StringValue("assistant")}}
	child := p.Event{SchemaVersion: target.CoreVersion(), Type: p.ItemStarted, ItemID: p.StringValue("text"), ParentID: parent.ItemID, Item: &p.Node{Kind: p.TextNode, Payload: p.StringValue("")}}
	finish := p.Event{SchemaVersion: target.CoreVersion(), Type: p.ItemFinished, ItemID: parent.ItemID}
	unparented := p.Event{SchemaVersion: target.CoreVersion(), Type: p.ItemDelta, ItemID: child.ItemID, Delta: p.StringValue("x")}
	collection := child
	collection.Collection = "summary"
	other := parent
	other.ItemID = p.StringValue("other")
	overlapping := child
	overlapping.ItemID = p.StringValue("other-text")
	annotated := parent
	annotatedNode := *parent.Item
	annotated.Item = &annotatedNode
	annotated.Item.Metadata = []p.ResponseMetadata{{Codec: Chat, SourceCodec: Chat, Location: "response", Name: "service_tier", Path: "/item/metadata", Value: p.StringValue("default")}}
	for _, fixture := range []struct {
		name    string
		prefix  []p.Event
		invalid p.Event
	}{
		{"orphan", nil, child},
		{"closed-parent", []p.Event{parent, finish}, child},
		{"changed-parent", []p.Event{parent, child}, unparented},
		{"parent-before-child", []p.Event{parent, child}, finish},
		{"overlapping-text-channel", []p.Event{parent, child}, overlapping},
		{"reasoning-collection", []p.Event{parent}, collection},
		{"interleaved-messages", []p.Event{parent}, other},
		{"independent-messages", []p.Event{parent, finish}, other},
		{"unprojected-metadata", nil, annotated},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			defer options.State.Release()
			if _, err := target.EncodeFrames(t.Context(), start, options); err != nil {
				t.Fatal(err)
			}
			for _, event := range fixture.prefix {
				if _, err := target.EncodeFrames(t.Context(), event, options); err != nil {
					t.Fatal("valid prefix", err)
				}
			}
			if _, err := target.EncodeFrames(t.Context(), fixture.invalid, options); err == nil {
				t.Fatal("direct encoding bypassed parent association or erased container data")
			}
		})
	}
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	defer options.State.Release()
	for _, event := range []p.Event{start, parent} {
		if _, err := target.EncodeFrames(t.Context(), event, options); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := target.FinishEvents(t.Context(), options); err == nil {
		t.Fatal("finalization silently closed an unfinished message")
	}
}
