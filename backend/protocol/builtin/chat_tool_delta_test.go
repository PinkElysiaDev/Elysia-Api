package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

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
