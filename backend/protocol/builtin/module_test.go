package builtin

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func testValue(t *testing.T, text string) p.Value {
	t.Helper()
	value, err := p.ParseValue([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testCompiled(t testing.TB, name string) *p.Compiled {
	t.Helper()
	compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var adapter module
	for _, entry := range modules {
		if entry.name == name {
			adapter = entry
		}
	}
	capabilities := p.CapabilitySet{}
	for _, capability := range p.CapabilityCatalog() {
		capabilities[capability] = true
	}
	directions := map[p.Direction]p.Mapping{}
	for _, direction := range adapter.Directions() {
		directions[direction] = p.Mapping{Module: name, FrameBatch: direction == p.EncodeEvent}
	}
	definition := p.Definition{SchemaVersion: 2, ID: "arbitrary-" + name, Name: name, Version: "1", Family: adapter.family, WireVersion: WireVersion, Capabilities: capabilities, Directions: directions, Native: p.NativePolicy{Preserve: true}, Operations: map[string]p.Operation{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: p.HTTPJSON, Auth: p.Credential{Location: "none"}}}}
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(raw)
	if compiled == nil {
		t.Fatal(issues)
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
			options := p.EvaluationContext{Scope: p.Scope{Provider: "p", Account: "a", Model: "m"}}
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
	options := p.EvaluationContext{Scope: p.Scope{Provider: "source", Account: "account", Model: "m"}}
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"continue","previous_response_id":"previous"}`), options)
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasScopedResources(request) || len(request.Resources) != 1 {
		t.Fatal("preceding response bypassed resource-aware routing")
	}
	if _, err := compiled.EncodeRequest(t.Context(), request, options); err != nil {
		t.Fatal(err)
	}
	options.Scope.Account = "different"
	if _, err := compiled.EncodeRequest(t.Context(), request, options); err == nil {
		t.Fatal("preceding response crossed accounts")
	}
	for name, body := range map[string]string{Chat: `{"model":"m","n":2,"stream":true,"messages":[{"role":"user","content":"hello"}]}`, Gemini: `{"generationConfig":{"candidateCount":2},"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`} {
		if _, err := testCompiled(t, name).DecodeRequest(t.Context(), []byte(body), options); err == nil {
			t.Fatalf("%s multi-choice request was accepted before unsupported streaming", name)
		}
	}
}

func TestBuiltinFourByFourTextAndFunctionHistory(t *testing.T) {
	request := p.Request{SchemaVersion: 1, Model: p.StringValue("m"), Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}, {Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.ToolCallNode, Name: p.StringValue("lookup"), CallID: p.StringValue("c"), Input: &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{"n":9007199254740993}`)}}}}, {Kind: p.MessageNode, Role: p.StringValue("user"), Children: []p.Node{{Kind: p.ToolResultNode, Name: p.StringValue("lookup"), CallID: p.StringValue("c"), Payload: testValue(t, `{"ok":true}`)}}}}, Tools: []p.Tool{{Kind: p.FunctionTool, Name: p.StringValue("lookup"), InputSchema: testValue(t, `{"type":"object"}`)}}}
	for _, source := range modules {
		for _, target := range modules {
			t.Run(source.name+"-"+target.name, func(t *testing.T) {
				from, to := testCompiled(t, source.name), testCompiled(t, target.name)
				result := testValue(t, `{"ok":true}`)
				if source.name != Gemini {
					result = p.StringValue("found")
				}
				request.Content[2].Children[0].Payload = result
				input, err := from.EncodeRequest(t.Context(), &request, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				semantic, err := from.DecodeRequest(t.Context(), input, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
				if err != nil {
					t.Fatal(err)
				}
				output, err := to.EncodeRequest(t.Context(), semantic, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
				if source.name != Gemini && target.name == Gemini {
					// 非 JSON 文本工具结果在 Gemini 目标仍显式拒绝（本夹具为 "found"）；
					// Gemini 的对象结果到其它目标现按 JSON 字符串序列化转换。
					var conversion *p.ConversionError
					if !errors.As(err, &conversion) || conversion.Issues[0].Code != p.UnsupportedCapability {
						t.Fatalf("non-JSON text tool result must stay rejected by Gemini: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				recovered, err := to.DecodeRequest(t.Context(), output, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
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
