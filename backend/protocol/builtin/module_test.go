package builtin

import (
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func testValue(t testing.TB, text string) p.Value {
	t.Helper()
	value, err := p.ParseValue([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testCompiled(t testing.TB, name string) *p.Compiled {
	t.Helper()
	return testCompiledWithLimits(t, name, p.DefaultLimits())
}

func testCompiledWithLimits(t testing.TB, name string, limits p.Limits) *p.Compiled {
	t.Helper()
	if limits == p.DefaultLimits() {
		compiled, err := shippedTestProtocols()
		if err != nil {
			t.Fatal(err)
		}
		if candidate := compiled[name]; candidate != nil {
			return candidate
		}
		t.Fatalf("unknown package fixture %q", name)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := readPackage(packageName(name))
	if err != nil {
		t.Fatal(err)
	}
	var document p.ProtocolPackage
	if err := raw.Decode(&document); err != nil {
		t.Fatal(err)
	}
	document.Mapping.Limits = &limits
	bounded, err := p.EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(bounded.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func sameJSON(t *testing.T, actual []byte, expected string) {
	t.Helper()
	var got, want any
	if err := testValue(t, string(actual)).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if err := testValue(t, expected).Decode(&want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actual: %s\nexpected: %s", actual, expected)
	}
}

func TestBuiltinNativeRequestAndResponsePreservation(t *testing.T) {
	cases := []struct{ name, request, response string }{
		{Chat, `{"model":"m","max_tokens":0,"messages":[{"role":"system","content":[{"type":"text","text":"prefix"}]},{"role":"user","content":"hello","x":9007199254740993}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"},"strict":false}}],"prompt_cache_key":"k","prompt_cache_retention":"24h","extension":null}`, `{"id":"r","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi","x":false},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":2,"total_tokens":22,"prompt_tokens_details":{"cached_tokens":10}},"extra":9007199254740993}`},
		{Responses, `{"model":"m","instructions":"prefix","input":[{"role":"user","content":"hello"},{"type":"custom_tool_call","id":"i","call_id":"c","name":"shell","input":"echo ok"},{"type":"custom_tool_call_output","call_id":"c","output":"ok"}],"tools":[{"type":"custom","name":"shell","format":{"type":"text"}},{"type":"web_search_preview","search_context_size":"low"}],"prompt_cache_key":"k","extra":{}}`, `{"id":"r","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi","annotations":[]}]}],"usage":{"input_tokens":20,"output_tokens":2,"total_tokens":22,"input_tokens_details":{"cached_tokens":10}},"extra":[]}`},
		{Anthropic, `{"model":"m","max_tokens":8,"system":[{"type":"text","text":"prefix","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{"n":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":"ok","cache_control":{"type":"ephemeral"}}]}],"tools":[{"name":"lookup","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral","ttl":"1h"}}],"cache_control":{"type":"ephemeral"}}`, `{"id":"r","model":"m","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":2,"cache_read_input_tokens":10,"cache_creation_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":5}}}`},
		{Gemini, `{"systemInstruction":{"parts":[{"text":"prefix"}]},"contents":[{"role":"user","parts":[{"text":"hi"}]},{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"n":9007199254740993}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"ok":true}}}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}],"cachedContent":"cachedContents/a","generationConfig":{"temperature":0}}`, `{"responseId":"r","modelVersion":"m","candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":2,"totalTokenCount":22,"cachedContentTokenCount":10}}`},
	}
	for _, fixture := range cases {
		t.Run(fixture.name, func(t *testing.T) {
			compiled := testCompiled(t, fixture.name)
			options := fixtureOptions(p.EvaluationContext{Scope: p.Scope{Provider: "p", Account: "a", Model: "m"}})
			request, err := compiled.DecodeRequest(t.Context(), []byte(fixture.request), options)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := compiled.EncodeRequest(t.Context(), request, options)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, encoded, fixture.request)
			response, err := compiled.DecodeResponse(t.Context(), []byte(fixture.response), options)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err = compiled.EncodeResponse(t.Context(), response, options)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, encoded, fixture.response)
			if response.Usage.Input.Count != 20 || response.Usage.CacheRead.Count != 10 {
				t.Fatalf("usage: %+v", response.Usage)
			}
		})
	}
}

func TestBuiltinScopedResponseReferenceAndChoiceLimits(t *testing.T) {
	compiled := testCompiled(t, Responses)
	options := fixtureOptions(p.EvaluationContext{Scope: p.Scope{Provider: "source", Account: "account", Model: "m"}})
	for _, account := range []string{"account", "different"} {
		options.Scope.Account = account
		_, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"continue","previous_response_id":"previous"}`), options)
		if !isUnsupported(err) {
			t.Fatal("unverified response reference reached routing", err)
		}
	}
	for name, body := range map[string]string{Chat: `{"model":"m","n":2,"stream":true,"messages":[{"role":"user","content":"hello"}]}`, Gemini: `{"generationConfig":{"candidateCount":2},"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`} {
		if _, err := testCompiled(t, name).DecodeRequest(t.Context(), []byte(body), options); err == nil {
			t.Fatalf("%s multi-choice request was accepted before unsupported streaming", name)
		}
	}
}

func TestBuiltinFourByFourTextAndFunctionHistory(t *testing.T) {
	request := p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}, {Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.ToolCallNode, Name: p.StringValue("lookup"), CallID: p.StringValue("c"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{"n":9007199254740993}`)}}}}, {Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.ToolResultNode, Name: p.StringValue("lookup"), CallID: p.StringValue("c"), Payload: testValue(t, `{"ok":true}`)}}}}, Tools: []p.Tool{{Kind: p.FunctionTool, Name: p.StringValue("lookup"), InputSchema: testValue(t, `{"type":"object"}`)}}}
	for _, source := range packageFixtures {
		for _, target := range packageFixtures {
			t.Run(source.name+"-"+target.name, func(t *testing.T) {
				from, to := testCompiled(t, source.name), testCompiled(t, target.name)
				result := testValue(t, `{"ok":true}`)
				if source.name != Gemini {
					result = p.StringValue("found")
				}
				request.Content[2].Children[0].Payload = result
				request.Source = from.Identity()
				setRequestParameter(&request, "max_output_tokens", testValue(t, "64"))
				preparation, err := p.ResolveConversion(p.DefaultConversionPolicy(from, from))
				if err != nil {
					t.Fatal(err)
				}
				prepared, err := preparation.Request(t.Context(), &request, fixtureConversionContext(t, preparation, p.ConversionRequest, from.Identity()), nil)
				if err != nil {
					t.Fatal(err)
				}
				input, err := from.EncodeRequest(t.Context(), prepared, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				semantic, err := from.DecodeRequest(t.Context(), input, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
				if err != nil {
					t.Fatal(err)
				}
				conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
				if err != nil {
					t.Fatal(err)
				}
				route := fixtureConversionContext(t, conversion, p.ConversionRequest, from.Identity())
				projected, err := conversion.Request(t.Context(), semantic, route, nil)
				if err != nil {
					t.Fatal(err)
				}
				output, err := to.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
				if err != nil {
					t.Fatal(err)
				}
				if source.name != Gemini && target.name == Gemini {
					for _, mode := range []string{"strict", "disabled"} {
						policy := p.DefaultConversionPolicy(from, to)
						if mode == "strict" {
							policy.Mode = mode
						} else {
							for i := range policy.Rules {
								if policy.Rules[i].ID == "tool-results" {
									policy.Rules[i].Enabled = false
								}
							}
						}
						restricted, err := p.ResolveConversion(policy)
						if err != nil {
							t.Fatal(err)
						}
						candidate, err := restricted.Request(t.Context(), semantic, route, nil)
						if err == nil {
							_, err = to.EncodeRequest(t.Context(), candidate, fixtureOptions(p.EvaluationContext{}))
						}
						if err == nil {
							t.Fatal(mode, "accepted an unresolved text tool result")
						}
					}
				}
				recovered, err := to.DecodeRequest(t.Context(), output, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
				if err != nil {
					t.Fatal(err)
				}
				var calls, results int
				var visit func([]p.Node)
				visit = func(nodes []p.Node) {
					for _, node := range nodes {
						if node.Kind == p.ToolCallNode {
							calls++
							sameJSON(t, node.Input.Value.Bytes(), `{"n":9007199254740993}`)
						}
						if node.Kind == p.ToolResultNode {
							results++
							payload := node.Payload.Bytes()
							if source.name != Gemini && target.name == Gemini {
								fields, err := node.Payload.ReadObject()
								if err != nil || len(fields) != 1 {
									t.Fatal("text result wrapper changed", node.Payload, err)
								}
								payload = fields["result"].Bytes()
							}
							if source.name == Gemini && target.name != Gemini {
								// 对象结果经 JSON 字符串序列化后，目标侧重回语义仍是文本载荷。
								var text string
								if err := node.Payload.Decode(&text); err == nil {
									payload = []byte(text)
								}
							}
							sameJSON(t, payload, string(result.Bytes()))
						}
						visit(node.Children)
					}
				}
				visit(recovered.Content)
				if calls != 1 || results != 1 || len(recovered.Tools) != 1 {
					t.Fatalf("calls=%d results=%d tools=%d", calls, results, len(recovered.Tools))
				}
			})
		}
	}
}
