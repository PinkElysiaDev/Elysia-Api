package builtin

import (
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatNullRefusalHistoryProjectsWithoutInventingRefusal(t *testing.T) {
	chat := shippedProjectionProtocol(t, Chat)
	const raw = `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"OK","refusal":null}]}`
	request, err := chat.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Content[1].Children) != 1 || request.Content[1].Children[0].Kind != p.TextNode {
		t.Fatal("null refusal invented a content node", request.Content[1])
	}
	for _, mode := range []string{"compatible", "strict"} {
		for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
			t.Run(mode+"/"+target, func(t *testing.T) {
				to := shippedProjectionProtocol(t, target)
				policy := p.DefaultConversionPolicy(chat, to)
				policy.Mode = mode
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: chat.Identity(), Target: to.Identity()}, nil)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
				if err != nil || !strings.Contains(string(wire), "OK") {
					t.Fatal(string(wire), err)
				}
			})
		}
	}
	request.Content[1].Children[0].Payload = p.StringValue("updated")
	wire, err := chat.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, strings.Replace(raw, `"OK"`, `"updated"`, 1))
}

func TestChatNullRefusalResponseKeepsNativePresence(t *testing.T) {
	chat := shippedProjectionProtocol(t, Chat)
	const raw = `{"id":"r","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK","refusal":null},"finish_reason":"stop"}]}`
	response, err := chat.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Content[0].Children) != 1 {
		t.Fatal("null refusal invented a response node", response.Content)
	}
	for _, changed := range []bool{false, true} {
		want := raw
		if changed {
			response.Content[0].Children[0].Payload = p.StringValue("updated")
			want = strings.Replace(raw, `"OK"`, `"updated"`, 1)
		}
		wire, err := chat.EncodeResponse(t.Context(), response, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, want)
	}
}

func TestChatRefusalTypeAndContentRemainProtected(t *testing.T) {
	chat := shippedProjectionProtocol(t, Chat)
	for _, value := range []string{`42`, `false`, `[]`, `{}`} {
		_, err := chat.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[{"role":"assistant","content":"OK","refusal":`+value+`}]}`), p.EvaluationContext{})
		var issue *p.ConversionError
		if !errors.As(err, &issue) || len(issue.Issues) == 0 || issue.Issues[0].Code != p.InvalidInput || issue.Issues[0].Path != "/messages/0/refusal" {
			t.Fatal("invalid refusal accepted or misdiagnosed", value, err)
		}
	}
	for _, value := range []string{`""`, `"Cannot comply"`} {
		request, err := chat.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[{"role":"assistant","refusal":`+value+`}]}`), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		if len(request.Content[0].Children) != 1 || request.Content[0].Children[0].Kind != p.RefusalNode {
			t.Fatal("actual refusal discarded", request.Content)
		}
		for _, target := range []string{Anthropic, Gemini} {
			if _, err := shippedProjectionProtocol(t, target).EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
				t.Fatal("unsupported refusal was silently relabeled", target)
			}
		}
	}
}
