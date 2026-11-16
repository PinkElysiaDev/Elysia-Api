package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageMetadataWithoutNative(t *testing.T) {
	const raw = `{"id":"r","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[],"logprobs":[]}]}]}`
	options := fixtureOptions(p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}})
	for _, shouldPreserve := range []bool{true, false} {
		t.Run(fmt.Sprintf("native=%t", shouldPreserve), func(t *testing.T) {
			document, _ := testCompiled(t, Responses).Package()
			document.Mapping.Native.Preserve = shouldPreserve
			compiled, issues := compilePackageDocument(t, document)
			if err := p.IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			response, err := compiled.DecodeResponse(t.Context(), []byte(raw), options)
			if err != nil {
				t.Fatal(err)
			}
			node := response.Content[0].Children[0]
			if node.Source == nil || node.Source.Path != "/output/0/content/0" || node.Source.Scope != options.Scope || (node.Native != nil) != shouldPreserve || len(node.Metadata) != 2 {
				t.Fatal("metadata provenance depends on native preservation", node)
			}
			for _, metadata := range node.Metadata {
				if metadata.Path != "/output/0/content/0/"+metadata.Name || metadata.Value.JSONString() != "[]" {
					t.Fatal("metadata lost its original location or empty value", metadata)
				}
			}
			response.Native = nil
			wire, err := compiled.EncodeResponse(t.Context(), response, options)
			if err != nil {
				t.Fatal("metadata reconstruction failed", err)
			}
			sameJSON(t, wire, `{"id":"r","model":"m","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[],"logprobs":[]}]}]}`)
		})
	}
}

func TestResponsesPackageGroupedHistory(t *testing.T) {
	compiled := testCompiled(t, Responses)
	content := []p.Node{
		{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{
			{Kind: p.TextNode, Payload: p.StringValue("before")},
			{Kind: p.TextNode, Payload: p.StringValue("adjacent")},
			{Kind: p.ToolCallNode, Name: p.StringValue("lookup"), CallID: p.StringValue("call"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{"n":9007199254740993}`)}},
			{Kind: p.TextNode, Payload: p.StringValue("after")},
		}},
		{Kind: p.MessageNode, Role: p.StringValue("assistant")},
	}
	for _, direction := range []p.Direction{p.EncodeRequest, p.EncodeResponse} {
		t.Run(string(direction), func(t *testing.T) {
			var wire []byte
			var err error
			field := "input"
			if direction == p.EncodeRequest {
				request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Content: content}
				wire, err = compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			} else {
				field = "output"
				response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Content: content}
				policy := declaredRulePolicy(t, compiled, compiled, "response-output-layout", "compatible")
				conversion, resolveErr := p.ResolveConversion(policy)
				if resolveErr != nil {
					t.Fatal(resolveErr)
				}
				route := p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
				sink := &p.DiagnosticSink{}
				response, err = conversion.Response(t.Context(), response, route, sink)
				if err != nil || !sinkHas(sink, "/content/0") {
					t.Fatal("declared output layout did not diagnose message boundary change", err, sink.Issues())
				}
				wire, err = compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			}
			if err != nil {
				t.Fatal("grouped message reconstruction failed", err)
			}
			fields, err := testValue(t, string(wire)).ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			expected := `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"before","annotations":[]},{"type":"output_text","text":"adjacent","annotations":[]}]},{"type":"function_call","call_id":"call","name":"lookup","arguments":"{\"n\":9007199254740993}"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"after","annotations":[]}]},{"type":"message","role":"assistant","content":[]}]`
			sameJSON(t, fields[field].Bytes(), expected)
		})
	}
}

func TestResponsesPackageDerivedTextAndNullFilters(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, raw := range []string{
		`{"id":"r","model":"m","output":[],"output_text":"","content_filters":null}`,
		`{"id":"r","model":"m","output_text":"A&B","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"A\u0026"}]},{"type":"function_call","call_id":"c","name":"lookup","arguments":"{\"n\":9007199254740993}"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"B"}]}]}`,
		`{"id":"r","model":"m","output":[],"content_filters":{"vendor":true}}`,
		`{"id":"r","model":"m","output_text":"short","output":[{"role":"assistant","content":"short"}]}`,
		`{"id":"r","model":"m","output_text":"","output":[{"type":"message","role":"assistant"}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			response, err := compiled.DecodeResponse(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal("valid derived text or nullable filters rejected", err)
			}
			response.Native = nil
			wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal("derived text reconstruction failed", err)
			}
			before, _ := testValue(t, raw).ReadObject()
			after, _ := testValue(t, string(wire)).ReadObject()
			for _, name := range []string{"output_text", "content_filters"} {
				if before[name].IsZero() != after[name].IsZero() {
					t.Fatal("metadata presence changed", name, string(wire))
				}
				if !before[name].IsZero() {
					sameJSON(t, after[name].Bytes(), before[name].JSONString())
				}
			}
			if _, err := compiled.DecodeResponse(t.Context(), wire, fixtureOptions(p.EvaluationContext{})); err != nil {
				t.Fatal("rebuilt derived text disagrees with output", string(wire), err)
			}
			calls := fixtureNodes(response.Content, p.ToolCallNode)
			if len(calls) != 0 && (len(calls) != 1 || calls[0].CallID != p.StringValue("c") || calls[0].Input == nil || calls[0].Input.Value.JSONString() != `{"n":9007199254740993}`) {
				t.Fatal("derived text changed the intervening tool call", calls)
			}
		})
	}
	for _, raw := range []string{
		`{"output":[],"output_text":null}`,
		`{"output":[],"output_text":42}`,
		`{"output":[],"output_text":[]}`,
		`{"output":[],"output_text":"different"}`,
		`{"output_text":""}`,
		`{"output":null,"output_text":""}`,
		`{"output_text":"","output":[{"type":"message","role":"assistant","content":null}]}`,
	} {
		t.Run("invalid/"+raw, func(t *testing.T) {
			_, err := compiled.DecodeResponse(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
			requireFailureRejection(t, err)
		})
	}
}
