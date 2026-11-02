package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatPackageTextLayoutAndCitations(t *testing.T) {
	source, target := compileBuiltinPackage(t, "openai-responses"), compileBuiltinPackage(t, "openai-chat")
	response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("completed"), Attributes: p.Object{"created_at": testValue(t, "1")}, Content: []p.Node{
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("😀"), Metadata: []p.ResponseMetadata{{Name: "logprobs", Codec: "openai-chat", Location: "content", Value: testValue(t, `[{"token":"😀","logprob":-1}]`)}}}}},
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hi"), Metadata: []p.ResponseMetadata{
			{Name: "annotations", Codec: "responses", Location: "content", Path: "/output/1/content/0/annotations", Value: testValue(t, `[{"type":"url_citation","url":"https://example.test","title":"reference","start_index":0,"end_index":2}]`)},
			{Name: "logprobs", Codec: "openai-chat", Location: "content", Value: testValue(t, `[{"token":"hi","logprob":-2}]`)},
		}}}},
	}}
	before, _ := p.EncodeValue(response)
	policy := p.DefaultConversionPolicy(target, source)
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
	projected, err := conversion.Response(t.Context(), response, route, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := target.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	var actual struct {
		Choices []struct {
			Message struct {
				Content     string
				Annotations []struct {
					URLCitation struct {
						Start int `json:"start_index"`
						End   int `json:"end_index"`
					} `json:"url_citation"`
				}
			}
			Logprobs struct{ Content []struct{ Token string } }
		}
	}
	if err := testValue(t, string(wire)).Decode(&actual); err != nil {
		t.Fatal(err)
	}
	if len(actual.Choices) != 1 || actual.Choices[0].Message.Content != "😀hi" || len(actual.Choices[0].Message.Annotations) != 1 || actual.Choices[0].Message.Annotations[0].URLCitation.Start != 2 || actual.Choices[0].Message.Annotations[0].URLCitation.End != 4 {
		t.Fatal("text or UTF-16 citation range changed", string(wire))
	}
	probabilities := actual.Choices[0].Logprobs.Content
	if len(probabilities) != 2 || probabilities[0].Token != "😀" || probabilities[1].Token != "hi" {
		t.Fatal("probabilities lost their owner order", string(wire))
	}
	after, _ := p.EncodeValue(response)
	if before != after {
		t.Fatal("target layout mutated the input")
	}
	for _, id := range []string{"response-layout", "response-text"} {
		for index := range policy.Rules {
			policy.Rules[index].Enabled = policy.Rules[index].ID != id
		}
		disabled, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		unresolved, err := disabled.Response(t.Context(), response, route, nil)
		if err == nil {
			_, err = target.EncodeResponse(t.Context(), unresolved, p.EvaluationContext{})
		}
		if err == nil {
			t.Fatal("encoder repeated disabled target layout", id)
		}
	}
}

func TestChatPackageMixedHistoryLayout(t *testing.T) {
	target := compileBuiltinPackage(t, "openai-chat")
	request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Content: []p.Node{
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.ReasoningNode, Payload: p.StringValue("think")}}},
		{Kind: p.ReasoningNode, Payload: p.StringValue("more")},
		{Kind: p.ToolCallNode, CallID: p.StringValue("call"), Name: p.StringValue("f"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, "{}")}},
		{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{
			{Kind: p.ToolResultNode, CallID: p.StringValue("call"), Payload: p.StringValue("result")},
			{Kind: p.TextNode, Payload: p.StringValue("continue")},
		}},
	}}
	policy := p.DefaultConversionPolicy(target, target)
	var rules []p.ConversionRule
	for _, rule := range policy.Rules {
		if rule.ID == "assistant-history" || rule.ID == "history-text" || rule.ID == "tool-result-history" {
			rules = append(rules, rule)
		}
	}
	policy.Rules = rules
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: target.Identity(), Target: target.Identity()}
	projected, err := conversion.Request(t.Context(), request, route, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := target.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Messages []struct {
			Role      string
			Content   p.Value
			Reasoning string                `json:"reasoning_content"`
			CallID    string                `json:"tool_call_id"`
			Calls     []struct{ ID string } `json:"tool_calls"`
		}
	}
	if err := testValue(t, string(wire)).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 3 || result.Messages[0].Reasoning != "thinkmore" || len(result.Messages[0].Calls) != 1 || result.Messages[0].Calls[0].ID != "call" || result.Messages[1].Role != "tool" || result.Messages[1].CallID != "call" || !result.Messages[1].Content.IsString("result") || result.Messages[2].Role != "user" || !result.Messages[2].Content.IsString("continue") {
		t.Fatal("mixed history changed content order or tool association", string(wire))
	}
	for _, id := range []string{"assistant-history", "history-text", "tool-result-history"} {
		for index := range policy.Rules {
			policy.Rules[index].Enabled = policy.Rules[index].ID != id
		}
		disabled, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		unresolved, err := disabled.Request(t.Context(), request, route, nil)
		if err == nil {
			_, err = target.EncodeRequest(t.Context(), unresolved, p.EvaluationContext{})
		}
		if err == nil {
			t.Fatal("encoder repeated disabled history layout", id)
		}
	}
}
