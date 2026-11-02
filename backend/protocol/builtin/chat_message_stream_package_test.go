package builtin

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatPackageStreamLayoutDisposition(t *testing.T) {
	source, target := compileBuiltinPackage(t, "openai-responses"), compileBuiltinPackage(t, "openai-chat")
	raw, err := os.ReadFile("testdata/responses-stream-layout.json")
	if err != nil {
		t.Fatal(err)
	}
	var values []p.Value
	if err := testValue(t, string(raw)).Decode(&values); err != nil {
		t.Fatal(err)
	}
	raws := make([]string, len(values))
	for index, value := range values {
		raws[index] = value.JSONString()
	}
	frames, _ := decodePackageStream(t, source, raws, p.Scope{})
	defaults := p.DefaultConversionPolicy(target, source)
	conversion, err := p.ResolveConversion(defaults)
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Transport: p.SSE}
	for _, frame := range frames {
		for index, event := range frame.Events {
			frame.Events[index], err = conversion.Event(t.Context(), event, route, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	sink := &p.DiagnosticSink{}
	wire := encodePackageStreamWithOptions(t, target, frames, (p.EvaluationContext{Diagnostics: sink}).WithConversion(conversion, route), false)
	var delivery []string
	for _, raw := range wire {
		var frame struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					Tools   []struct {
						Function struct{ Name, Arguments string } `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := testValue(t, raw).Decode(&frame); err != nil {
			t.Fatal(err)
		}
		for _, choice := range frame.Choices {
			if choice.Delta.Content != "" {
				delivery = append(delivery, choice.Delta.Content)
			}
			for _, tool := range choice.Delta.Tools {
				if tool.Function.Name != "" {
					delivery = append(delivery, tool.Function.Name)
				}
				if tool.Function.Arguments != "" {
					delivery = append(delivery, tool.Function.Arguments)
				}
			}
		}
	}
	if !slices.Equal(delivery, []string{"😀", "lookup", `{"n":9007199254740993}`, "hi", "!"}) {
		t.Fatal("layout reordered incremental delivery", delivery)
	}
	_, response := decodePackageStream(t, target, wire, p.Scope{})
	var text strings.Builder
	var probabilities []string
	tools, citations := 0, 0
	var visit func([]p.Node)
	visit = func(nodes []p.Node) {
		for _, node := range nodes {
			if node.Kind == p.TextNode {
				var value string
				if err := node.Payload.Decode(&value); err != nil {
					t.Fatal(err)
				}
				text.WriteString(value)
			}
			if node.Kind == p.ToolCallNode {
				tools++
				if !node.CallID.IsString("call") || !node.Name.IsString("lookup") || node.Input == nil {
					t.Fatal("tool identity changed", node)
				}
				sameJSON(t, node.Input.Value.Bytes(), `{"n":9007199254740993}`)
			}
			for _, metadata := range node.Metadata {
				if metadata.Name == "logprobs" {
					var entries []struct {
						Token string `json:"token"`
					}
					if err := metadata.Value.Decode(&entries); err != nil {
						t.Fatal(err)
					}
					for _, entry := range entries {
						probabilities = append(probabilities, entry.Token)
					}
				}
				if metadata.Name == "annotations" {
					citations++
					sameJSON(t, metadata.Value.Bytes(), `[{"type":"url_citation","url_citation":{"url":"https://example.test","title":"reference","start_index":2,"end_index":4}}]`)
				}
			}
			visit(node.Children)
		}
	}
	visit(response.Content)
	if !slices.Equal(probabilities, []string{"😀", "hi", "!"}) {
		t.Fatal("logprobs changed owners or duplicated a completed snapshot", probabilities)
	}
	if text.String() != "😀hi!" || tools != 1 || citations != 1 || !response.Status.IsString("completed") || response.Usage == nil || response.Usage.Total == nil || response.Usage.Total.Count != 22 {
		t.Fatal("complex layout lost text, tool, citation, terminal or usage", text.String(), tools, citations, response)
	}
	if len(sink.Issues()) != 2 {
		t.Fatal("expected one diagnostic per distinct layout location", sink.Issues())
	}
	for _, issue := range sink.Issues() {
		if issue.RuleID != "event-layout" || issue.Code != p.ConversionDegraded {
			t.Fatal("layout warning was not attributed to its rule", issue)
		}
	}
	for _, mode := range []string{"strict", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			overrides := p.ConversionPolicy{SchemaVersion: p.ConversionPolicySchemaVersion, ID: "layout-override"}
			if mode == "strict" {
				overrides.Mode = "strict"
			} else {
				for _, rule := range defaults.Rules {
					if rule.ID == "event-layout" {
						rule.Enabled = false
						// Complete replacement, not a second fallback default.
						overrides.Rules = []p.ConversionRule{rule}
					}
				}
			}
			selected, err := p.ResolveConversion(defaults, overrides)
			if err != nil {
				t.Fatal(err)
			}
			options := (p.EvaluationContext{State: p.NewEvaluationState(), Diagnostics: &p.DiagnosticSink{}}).WithConversion(selected, route)
			defer options.State.Release()
			for frameIndex, frame := range frames {
				_, err := target.EncodeFrame(t.Context(), &p.EventFrame{Events: frame.Events}, options)
				if err == nil {
					continue
				}
				var failure *p.ConversionError
				if frameIndex != 6 || !errors.As(err, &failure) || failure.Issues[0].Code != p.ConversionRejected || failure.Issues[0].RuleID != "event-layout" || len(options.Diagnostics.Issues()) != 0 {
					t.Fatal("did not reject the first actual layout loss", frameIndex, err, options.Diagnostics.Issues())
				}
				// The rejected tool start must remain usable by the same transaction owner.
				if _, err := target.EncodeFrame(t.Context(), &p.EventFrame{Events: frame.Events}, options.WithConversion(conversion, route)); err != nil {
					t.Fatal("rejected layout advanced tool identity", err)
				}
				return
			}
			t.Fatal("unauthorized layout reached the terminal")
		})
	}
}

func TestChatPackageStreamTextRanges(t *testing.T) {
	source, target := compileBuiltinPackage(t, "openai-responses"), compileBuiltinPackage(t, "openai-chat")
	raw, err := os.ReadFile("testdata/responses-text-ranges.json")
	if err != nil {
		t.Fatal(err)
	}
	var values []p.Value
	if err := testValue(t, string(raw)).Decode(&values); err != nil {
		t.Fatal(err)
	}
	raws := make([]string, len(values))
	for index, value := range values {
		raws[index] = value.JSONString()
	}
	frames, _ := decodePackageStream(t, source, raws, p.Scope{})
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(target, source))
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Transport: p.SSE}
	for _, frame := range frames {
		for index, event := range frame.Events {
			frame.Events[index], err = conversion.Event(t.Context(), event, route, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	wire := encodePackageStreamWithOptions(t, target, frames, (p.EvaluationContext{}).WithConversion(conversion, route), false)
	_, response := decodePackageStream(t, target, wire, p.Scope{})
	if len(response.Content) != 1 || len(response.Content[0].Children) != 1 || !response.Content[0].Children[0].Payload.IsString("😀hi") {
		t.Fatal("sequential text owners changed output order", response.Content)
	}
	annotations := 0
	for _, metadata := range response.Content[0].Children[0].Metadata {
		if metadata.Name != "annotations" {
			continue
		}
		annotations++
		sameJSON(t, metadata.Value.Bytes(), `[{"type":"url_citation","url_citation":{"url":"https://example.test","title":"reference","start_index":2,"end_index":4}}]`)
	}
	if annotations != 1 || !response.Status.IsString("completed") || response.Usage == nil || response.Usage.Total == nil || response.Usage.Total.Count != 22 {
		t.Fatal("citation, terminal or usage was lost", annotations, response)
	}
}

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
