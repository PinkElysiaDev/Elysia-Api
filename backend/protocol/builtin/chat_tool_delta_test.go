package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatToolIndexesExcludeOtherContent(t *testing.T) {
	for _, prefix := range []p.NodeKind{p.TextNode, p.ReasoningNode, p.RefusalNode} {
		t.Run(string(prefix), func(t *testing.T) {
			compiled := shippedProjectionProtocol(t, Chat)
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			encode := func(event p.Event) []p.Value {
				t.Helper()
				event.SchemaVersion = p.SemanticSchemaVersion
				frames, err := compiled.EncodeFrames(t.Context(), event, options)
				if err != nil {
					t.Fatal(err)
				}
				return frames
			}
			encode(p.Event{Type: p.ItemStarted, ItemID: p.StringValue("prefix"), Item: &p.Node{Kind: prefix, Payload: p.StringValue("before tools")}})
			encode(p.Event{Type: p.ItemFinished, ItemID: p.StringValue("prefix")})
			var frames []p.Value
			for i := 0; i < 2; i++ {
				id := p.StringValue(fmt.Sprintf("call_%d", i))
				frames = append(frames, encode(p.Event{Type: p.ItemStarted, ItemID: id, Item: &p.Node{Kind: p.ToolCallNode, CallID: id, Name: p.StringValue("lookup"), Input: &p.ToolInput{Kind: p.JSONInput}}})...)
				if i == 0 {
					encode(p.Event{Type: p.ItemStarted, ItemID: p.StringValue("between"), Item: &p.Node{Kind: p.TextNode, Payload: p.StringValue("between tools")}})
					encode(p.Event{Type: p.ItemFinished, ItemID: p.StringValue("between")})
				}
			}
			for _, i := range []int{1, 0} {
				id := p.StringValue(fmt.Sprintf("call_%d", i))
				frames = append(frames, encode(p.Event{Type: p.ItemDelta, ItemID: id, Delta: p.StringValue(fmt.Sprintf(`{"n":%d}`, i))})...)
				frames = append(frames, encode(p.Event{Type: p.ItemFinished, ItemID: id})...)
			}
			ids, arguments := map[int]string{}, map[int]string{}
			for _, frame := range frames {
				var wire struct {
					Choices []struct {
						Delta struct {
							Calls []struct {
								Index    int
								ID       string
								Function struct{ Arguments string }
							} `json:"tool_calls"`
						}
					}
				}
				if err := frame.Decode(&wire); err != nil {
					t.Fatal(err)
				}
				for _, choice := range wire.Choices {
					for _, call := range choice.Delta.Calls {
						if call.ID != "" {
							ids[call.Index] = call.ID
						}
						arguments[call.Index] += call.Function.Arguments
					}
				}
			}
			if len(ids) != 2 || ids[0] != "call_0" || ids[1] != "call_1" || arguments[0] != `{"n":0}` || arguments[1] != `{"n":1}` {
				t.Fatalf("tool slots contain holes or crossed arguments: IDs=%v arguments=%v", ids, arguments)
			}
		})
	}
}

func TestChatToolIdentityIsEmittedOnceWithArgumentFragments(t *testing.T) {
	stream := &streamModule{module: module{name: Chat}, limits: p.DefaultLimits(), items: map[string]*streamItem{}, identities: p.NewItemIdentities(p.DefaultLimits().StateItems)}
	for _, id := range []string{"parallel_1", "parallel_2"} {
		var name, callID, arguments string
		index := -1
		for _, event := range []p.Event{
			{Type: p.ItemStarted, ItemID: p.StringValue(id), Item: &p.Node{Kind: p.ToolCallNode, Name: p.StringValue("audit_echo"), CallID: p.StringValue(id), Input: &p.ToolInput{Kind: p.JSONInput}}},
			{Type: p.ItemDelta, ItemID: p.StringValue(id), Delta: p.StringValue(`{"value":`)},
			{Type: p.ItemDelta, ItemID: p.StringValue(id), Delta: p.StringValue(`7}`)},
			{Type: p.ItemFinished, ItemID: p.StringValue(id)},
		} {
			frames, err := stream.encodeEvent(event, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			for _, frame := range frames {
				var body struct {
					Choices []struct {
						Delta struct {
							Calls []struct {
								Index    int
								ID       string
								Function struct{ Name, Arguments string }
							} `json:"tool_calls"`
						}
					}
				}
				if err := frame.Decode(&body); err != nil {
					t.Fatal(err)
				}
				for _, choice := range body.Choices {
					for _, call := range choice.Delta.Calls {
						if index != -1 && index != call.Index {
							t.Fatal("tool index changed")
						}
						index = call.Index
						name += call.Function.Name
						callID += call.ID
						arguments += call.Function.Arguments
					}
				}
			}
		}
		if name != "audit_echo" || callID != id || arguments != `{"value":7}` {
			t.Fatalf("client concatenation corrupted tool: %q %q %q", callID, name, arguments)
		}
	}
}
