package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestToolResultStatusProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Anthropic)
	for _, codec := range []string{Anthropic, Chat, Responses, Gemini} {
		to := shippedProjectionProtocol(t, codec)
		for _, flag := range []string{"absent", "false", "true"} {
			for _, blocks := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/blocks=%t", codec, flag, blocks), func(t *testing.T) {
					status, content := "", `"result-body"`
					if flag != "absent" {
						status = `,"is_error":` + flag
					}
					if blocks {
						content = `[{"type":"text","text":"result-"},{"type":"text","text":"body"}]`
					}
					raw := fmt.Sprintf(`{"model":"m","max_tokens":100,"messages":[{"role":"user","content":"read"},{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"read","input":{"is_error":true,"error":"untouched"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":%s%s}]}],"tools":[{"name":"read","input_schema":{"type":"object"}}]}`, content, status)
					r, err := from.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
					before, _ := p.EncodeValue(r)
					c, err := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
					if err != nil {
						t.Fatal(err)
					}
					sink := &p.DiagnosticSink{}
					out, err := c.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
					if err != nil {
						t.Fatal(err)
					}
					wire, err := to.EncodeRequest(t.Context(), out, p.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
					back, err := to.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
					result, call := toolResultStatusNodes(back.Content)
					if result == nil || call == nil || result.CallID != call.CallID || call.Input.Value != r.Content[1].Children[0].Input.Value {
						t.Fatal("tool input or association changed", string(wire))
					}
					switch codec {
					case Anthropic:
						if result.Status != r.Content[2].Children[0].Status || result.Payload != r.Content[2].Children[0].Payload {
							t.Fatal("native error flag or content changed")
						}
					case Gemini:
						payload := result.Payload
						if flag == "true" {
							fields, _ := payload.ReadObject()
							payload = fields["error"]
						}
						if payload != testValue(t, `{"result":"result-body"}`) {
							t.Fatal("incorrect object result", string(wire))
						}
					default:
						expected := "result-body"
						if flag == "true" {
							expected = "[Tool error] " + expected
						}
						if codec == Chat || !blocks {
							if result.Payload != p.StringValue(expected) {
								t.Fatal("incorrect text result", string(wire))
							}
						} else if !strings.Contains(string(wire), "result-") || !strings.Contains(string(wire), "body") || (flag == "true" && !strings.Contains(string(wire), "[Tool error] ")) {
							t.Fatal("result blocks lost", string(wire))
						}
					}
					statusIssues := 0
					for _, issue := range sink.Issues() {
						if !strings.HasSuffix(issue.Path, "/status") {
							continue
						}
						statusIssues++
						lossy := flag == "true" && (codec == Chat || codec == Responses)
						if (issue.Fidelity == "lossy_compatible") != lossy || (issue.Severity == p.SeverityWarning) != lossy {
							t.Fatal("wrong status fidelity", issue)
						}
					}
					wantIssues := 0
					if codec != Anthropic && flag != "absent" {
						wantIssues = 1
					}
					if statusIssues != wantIssues {
						t.Fatal("missing or duplicate status diagnostic", sink.Issues())
					}
					after, _ := p.EncodeValue(r)
					if before != after {
						t.Fatal("candidate changed original request")
					}
				})
			}
		}
	}
}

func toolResultStatusNodes(nodes []p.Node) (result, call *p.Node) {
	for i := range nodes {
		switch nodes[i].Kind {
		case p.ToolResultNode:
			result = &nodes[i]
		case p.ToolCallNode:
			call = &nodes[i]
		}
		r, c := toolResultStatusNodes(nodes[i].Children)
		if r != nil {
			result = r
		}
		if c != nil {
			call = c
		}
	}
	return
}

func TestToolResultStatusModesAndGuards(t *testing.T) {
	from := shippedProjectionProtocol(t, Anthropic)
	raw := `{"model":"m","max_tokens":100,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"body","is_error":true}]}]}`
	input, err := from.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{Anthropic, Chat, Responses, Gemini} {
		to := shippedProjectionProtocol(t, codec)
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			for _, flag := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", codec, mode, flag), func(t *testing.T) {
					r := input.Clone()
					result, _ := toolResultStatusNodes(r.Content)
					result.Status = testValue(t, fmt.Sprintf(`{"isError":%t}`, flag))
					if codec == Gemini {
						result.Payload = testValue(t, `{"error":"ordinary data","output":"also data","n":9007199254740993}`)
					}
					policy := p.DefaultConversionPolicy(from, to)
					if mode == "strict" {
						policy.Mode = mode
					}
					if mode == "disabled" {
						for i := range policy.Rules {
							if strings.HasPrefix(policy.Rules[i].Action, "tool_result_") {
								policy.Rules[i].Enabled = false
							}
						}
					}
					c, _ := p.ResolveConversion(policy)
					sink := &p.DiagnosticSink{}
					out, err := c.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
					var wire []byte
					if err == nil {
						wire, err = to.EncodeRequest(t.Context(), out, p.EvaluationContext{})
					}
					wantError := codec != Anthropic && mode == "disabled" || flag && mode == "strict" && (codec == Chat || codec == Responses)
					if (err != nil) != wantError {
						t.Fatal("unexpected result", err, string(wire))
					}
					if wantError {
						return
					}
					projected, _ := toolResultStatusNodes(out.Content)
					if codec == Gemini {
						key := "output"
						if flag {
							key = "error"
						}
						fields, _ := projected.Payload.ReadObject()
						if fields[key] != result.Payload {
							t.Fatal("caller object altered or success misclassified", string(wire))
						}
					}
					if codec == Chat || codec == Responses {
						// Reusing the source encoder must not resurrect the original flag.
						request := r.Clone()
						original, _ := toolResultStatusNodes(request.Content)
						*original = *projected
						back, err := from.EncodeRequest(t.Context(), request, p.EvaluationContext{})
						if err != nil || strings.Contains(string(back), `"is_error"`) {
							t.Fatal("native snapshot restored status", err, string(back))
						}
					}
				})
			}
		}
	}
	to := shippedProjectionProtocol(t, Gemini)
	for _, status := range []string{`{"isError":null}`, `{"isError":1}`, `{"isError":"false"}`, `{"isError":[]}`, `{"isError":true,"extra":1}`, `{"future":true}`, `true`} {
		r := input.Clone()
		result, _ := toolResultStatusNodes(r.Content)
		result.Status = testValue(t, status)
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
		if _, err := c.Request(t.Context(), r, p.ConversionContext{}, nil); err == nil {
			t.Fatal("malformed or unknown status accepted", status)
		}
	}
	for _, value := range []string{`null`, `1`, `"false"`, `[]`} {
		if _, err := from.DecodeRequest(t.Context(), []byte(strings.Replace(raw, `"is_error":true`, `"is_error":`+value, 1)), p.EvaluationContext{}); err == nil {
			t.Fatal("invalid wire flag accepted", value)
		}
		r := input.Clone()
		result, _ := toolResultStatusNodes(r.Content)
		result.Status = testValue(t, `{"isError":`+value+`}`)
		policy := p.DefaultConversionPolicy(from, to)
		policy.Rules = append(policy.Rules, p.ConversionRule{ID: "remove-status", Order: 1, Enabled: true, Phase: p.ConversionRequest, Match: p.ConversionMatch{NodeKind: p.ToolResultNode}, Action: "remove", Path: "/status"})
		c, _ := p.ResolveConversion(policy)
		if _, err := c.Request(t.Context(), r, p.ConversionContext{}, nil); err == nil {
			t.Fatal("authored removal hid malformed error flag", value)
		}
	}
	for _, block := range []p.Node{
		{Kind: p.ImageNode, Payload: testValue(t, `{"data":"YQ==","mime":"image/png"}`)},
		{Kind: p.TextNode, Payload: p.StringValue("body"), Resources: []p.Resource{{Kind: "file", ID: p.StringValue("file_1")}}},
		{Kind: p.TextNode, Payload: p.StringValue("body"), Attributes: p.Object{"future": p.StringValue("keep")}},
	} {
		r := input.Clone()
		result, _ := toolResultStatusNodes(r.Content)
		result.Children = []p.Node{block}
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
		if _, err := c.Request(t.Context(), r, p.ConversionContext{}, nil); err == nil {
			t.Fatal("unhandled result content discarded", block)
		}
	}
}

func TestEmptyToolResultStatus(t *testing.T) {
	from := shippedProjectionProtocol(t, Anthropic)
	for _, codec := range []string{Anthropic, Chat, Responses, Gemini} {
		to := shippedProjectionProtocol(t, codec)
		for _, content := range []string{`""`, `[]`, `[ ]`} {
			for _, failed := range []bool{false, true} {
				raw := fmt.Sprintf(`{"model":"m","max_tokens":100,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"f","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":%s,"is_error":%t}]}]}`, content, failed)
				r, err := from.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				c, _ := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
				out, err := c.Request(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
				if err == nil {
					_, err = to.EncodeRequest(t.Context(), out, p.EvaluationContext{})
				}
				if err != nil {
					t.Fatal(codec, content, failed, err)
				}
			}
		}
	}
}
