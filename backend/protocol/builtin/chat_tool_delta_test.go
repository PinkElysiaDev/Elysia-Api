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
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer options.State.Release()
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
			if err != nil {
				t.Fatal(err)
			}
			route := fixtureConversionContext(t, conversion, p.ConversionEvent, compiled.Identity())
			route.Transport = p.SSE
			options = options.WithConversion(conversion, route)
			options, consume := bindFixtureOutput(t, compiled, options)
			encode := func(event p.Event) []p.Value {
				t.Helper()
				event.SchemaVersion = p.ProtocolCoreVersion
				event.ResponseID = p.StringValue("r")
				event, err := conversion.Event(t.Context(), event, route, nil)
				if err != nil {
					t.Fatal(err)
				}
				frames, err := compiled.EncodeFrames(t.Context(), event, options)
				if err != nil {
					t.Fatal(err)
				}
				consume(frames)
				return frames
			}
			encode(p.Event{Type: p.ResponseStarted, Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, `1`)}}})
			encode(p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("prefix"), Item: &p.Node{Kind: prefix, Payload: p.StringValue("before tools")}})
			encode(p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemFinished, ItemID: p.StringValue("prefix")})
			var frames []p.Value
			for i := 0; i < 2; i++ {
				id := p.StringValue(fmt.Sprintf("call_%d", i))
				frames = append(frames, encode(p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: id, Item: &p.Node{Kind: p.ToolCallNode, CallID: id, Name: p.StringValue("lookup"), Input: &p.ToolInput{Kind: p.JSONInput}}})...)
				if i == 0 {
					encode(p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("between"), Item: &p.Node{Kind: p.TextNode, Payload: p.StringValue("between tools")}})
					encode(p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemFinished, ItemID: p.StringValue("between")})
				}
			}
			for _, i := range []int{1, 0} {
				id := p.StringValue(fmt.Sprintf("call_%d", i))
				frames = append(frames, encode(p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemDelta, ItemID: id, Delta: p.StringValue(fmt.Sprintf(`{"n":%d}`, i))})...)
				frames = append(frames, encode(p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemFinished, ItemID: id})...)
			}
			encode(p.Event{Type: p.ResponseFinished})
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
	compiled := testCompiled(t, Chat)
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	options, consume := bindFixtureOutput(t, compiled, options)
	encode := func(event p.Event) []p.Value {
		t.Helper()
		event.SchemaVersion, event.ResponseID = p.ProtocolCoreVersion, p.StringValue("r")
		frames, err := compiled.EncodeFrames(t.Context(), event, options)
		if err != nil {
			t.Fatal(err)
		}
		consume(frames)
		return frames
	}
	encode(p.Event{Type: p.ResponseStarted, Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Attributes: p.Object{"created_at": testValue(t, "1")}}})
	for expectedIndex, id := range []string{"parallel_1", "parallel_2"} {
		var name, callID, arguments string
		for _, event := range []p.Event{
			{Type: p.ItemStarted, ItemID: p.StringValue(id), Item: &p.Node{Kind: p.ToolCallNode, Name: p.StringValue("audit_echo"), CallID: p.StringValue(id), Input: &p.ToolInput{Kind: p.JSONInput}}},
			{Type: p.ItemDelta, ItemID: p.StringValue(id), Delta: p.StringValue(`{"value":`)},
			{Type: p.ItemDelta, ItemID: p.StringValue(id), Delta: p.StringValue(`7}`)},
			{Type: p.ItemFinished, ItemID: p.StringValue(id)},
		} {
			for _, frame := range encode(event) {
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
						if call.Index != expectedIndex {
							t.Fatal("tool index changed", call.Index, expectedIndex)
						}
						name += call.Function.Name
						callID += call.ID
						arguments += call.Function.Arguments
					}
				}
			}
		}
		if name != "audit_echo" || callID != id || arguments != `{"value":7}` {
			t.Fatal("tool identity repeated or arguments changed", name, callID, arguments)
		}
	}
	encode(p.Event{Type: p.ResponseFinished})
}
