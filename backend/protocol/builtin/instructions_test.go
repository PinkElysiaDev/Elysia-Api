package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestNativeInstructionsStayOutsideInputAfterHistoryEdits(t *testing.T) {
	compiled := testCompiled(t, Responses)
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","instructions":"prefix","input":[{"role":"user","content":"hello","vendor":9007199254740993}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	request.Content = append(request.Content, p.Node{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("next")}}})
	body, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil || strings.Contains(string(body), `"role":"system"`) || !strings.Contains(string(body), `"instructions":"prefix"`) || !strings.Contains(string(body), `9007199254740993`) {
		t.Fatal(string(body), err)
	}
	request.Content[0].Children[0].Payload = p.StringValue("edited")
	body, err = compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil || !strings.Contains(string(body), `"instructions":"edited"`) || strings.Contains(string(body), "prefix") {
		t.Fatal(string(body), err)
	}
	request.Content = request.Content[1:]
	body, err = compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil || strings.Contains(string(body), "instructions") {
		t.Fatal("deleted instructions restored", string(body), err)
	}
}

func TestNativeInstructionsRejectUnrepresentableEdits(t *testing.T) {
	for _, edit := range []func(*p.Node){
		func(node *p.Node) { node.ID = p.StringValue("identity") },
		func(node *p.Node) { node.Payload = p.StringValue("extra") },
		func(node *p.Node) { node.Children[0].Status = p.StringValue("completed") },
		func(node *p.Node) { node.Children[0].Name = p.StringValue("named") },
		func(node *p.Node) {
			node.Children[0].Children = []p.Node{{Kind: p.TextNode, Payload: p.StringValue("nested")}}
		},
	} {
		compiled := testCompiled(t, Responses)
		request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","instructions":"prefix","input":"hello"}`), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		edit(&request.Content[0])
		if body, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
			t.Fatal("instruction metadata silently discarded", string(body))
		}
	}
}
