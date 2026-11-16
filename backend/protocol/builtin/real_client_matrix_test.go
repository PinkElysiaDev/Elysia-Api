package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// 人工协议边界样例，包含中段 system 消息及消息级缓存标记。
// 另覆盖 Responses 消息 item id、Gemini 对象工具结果和 Chat strict 工具，锁定四协议
// 点对点的保证行为——正确转换或显式诊断。

const syntheticAnthropicHistory = `{
	"model":"claude-opus-5-5","max_tokens":8192,"stream":true,
	"system":[{"type":"text","text":"You are a helpful assistant.","cache_control":{"type":"ephemeral"}}],
	"messages":[
		{"role":"user","content":[{"type":"text","text":"list files"}]},
		{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}},{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"pwd"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"a\nb"}],"is_error":false},{"type":"tool_result","tool_use_id":"t2","content":[{"type":"text","text":"err"}],"is_error":true},{"type":"text","text":"and t2"}]},
		{"role":"system","content":[{"type":"text","text":"<system-reminder>context low</system-reminder>"}],"cache_control":{"type":"ephemeral"}},
		{"role":"user","content":[{"type":"text","text":"continue"}]}
	],
	"tools":[{"name":"Bash","description":"run","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral","ttl":"1h"}}],
	"tool_choice":{"type":"auto","disable_parallel_tool_use":false},
	"metadata":{"user_id":"user_acct"}
}`

// The current Anthropic SDK includes system in MessageParam.role. Native
// encoding must retain its position and message-level cache boundary.
func TestAnthropicPreservesInPlaceSystem(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	request, err := compiled.DecodeRequest(t.Context(), testValue(t, syntheticAnthropicHistory).Bytes(), fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, syntheticAnthropicHistory)
}

// 人工 Anthropic → Chat：并行 tool_result 拆为多条 tool 消息、is_error 文本标记、
// strict 剥离、thinking 签名丢弃——全部转成 Chat 可表达形态。
func TestSyntheticAnthropicToChatConvertsToolResultGroup(t *testing.T) {
	source := testCompiled(t, Anthropic)
	target := testCompiled(t, Chat)
	request, err := source.DecodeRequest(t.Context(), testValue(t, syntheticAnthropicHistory).Bytes(), fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
	if err != nil {
		t.Fatal(err)
	}
	sink := &p.DiagnosticSink{}
	conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
	request, err = conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: target.Identity()}, sink)
	if err != nil {
		t.Fatal(err)
	}
	output, err := target.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}, Diagnostics: sink}))
	if err != nil {
		// 定位残余的 wire:claude 拒绝来自哪个节点，便于修正剥离点。
		for index, node := range request.Content {
			for key := range node.Attributes {
				t.Logf("node[%d] attr %s", index, key)
			}
			for childIndex, child := range node.Children {
				for key := range child.Attributes {
					t.Logf("node[%d].child[%d] attr %s", index, childIndex, key)
				}
			}
		}
		t.Fatal(err)
	}
	body := string(output)
	for _, expected := range []string{`"tool_call_id":"t1"`, `"tool_call_id":"t2"`, "[Tool error] err", "and t2"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %s in output: %s", expected, body[:min(600, len(body))])
		}
	}
	if !sinkHas(sink, "/tools/0/cache") {
		t.Fatal("expected cache hint omission diagnostic")
	}
	recovered, err := target.DecodeRequest(t.Context(), output, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
	if err != nil {
		t.Fatal(err)
	}
	var toolResults int
	var visit func([]p.Node)
	visit = func(nodes []p.Node) {
		for _, node := range nodes {
			if node.Kind == p.ToolResultNode {
				toolResults++
			}
			visit(node.Children)
		}
	}
	visit(recovered.Content)
	if toolResults != 2 {
		t.Fatalf("parallel tool results lost: %d", toolResults)
	}
}

// 中段/带元数据 system 到 Gemini 仍显式拒绝（contents 无 system 角色）。
func TestInPlaceSystemToGeminiStaysDiagnosed(t *testing.T) {
	source := testCompiled(t, Anthropic)
	target := testCompiled(t, Gemini)
	request, err := source.DecodeRequest(t.Context(), testValue(t, syntheticAnthropicHistory).Bytes(), fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}})); !isUnsupported(err) {
		t.Fatalf("gemini must keep the explicit diagnostic, got %v", err)
	}
}

// History item identity/status loss is owned by the shared conversion policy.
func TestResponsesItemIdentityConvertsCrossFamily(t *testing.T) {
	payload := `{"model":"gpt","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}],"id":"msg_1","status":"completed"}
	],"tools":[{"type":"function","name":"f","description":"d","parameters":{"type":"object"},"strict":true}]}`
	source := testCompiled(t, Responses)
	request, err := source.DecodeRequest(t.Context(), testValue(t, payload).Bytes(), fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{Chat, Anthropic, Gemini} {
		compiled := testCompiled(t, target)
		sink := &p.DiagnosticSink{}
		conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, compiled))
		converted, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: source.Identity(), Target: compiled.Identity()}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := compiled.EncodeRequest(t.Context(), converted, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}, Diagnostics: sink})); err != nil {
			t.Fatalf("%s: item identity must convert with a warning, got %v", target, err)
		}
		if !sinkHas(sink, "/content/") {
			t.Fatalf("%s: expected identity-drop warning", target)
		}
	}
}

// Gemini 对象工具结果 ↔ 其它目标的 JSON 字符串往返。
func TestGeminiObjectToolResultSerializesRoundTrip(t *testing.T) {
	payload := `{"contents":[
		{"role":"user","parts":[{"text":"run"}]},
		{"role":"model","parts":[{"functionCall":{"name":"f","args":{"n":1}}}]},
		{"role":"user","parts":[{"functionResponse":{"name":"f","response":{"ok":true}}}]}
	],"tools":[{"functionDeclarations":[{"name":"f","description":"d","parameters":{"type":"object"}}]}]}`
	source := testCompiled(t, Gemini)
	request, err := source.DecodeRequest(t.Context(), testValue(t, payload).Bytes(), fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{Chat, Responses, Anthropic} {
		compiled := testCompiled(t, target)
		conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, compiled))
		converted, e := conversion.Request(t.Context(), request, fixtureConversionContext(t, conversion, p.ConversionRequest, request.Source), nil)
		if e != nil {
			t.Fatal(e)
		}
		output, err := compiled.EncodeRequest(t.Context(), converted, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
		if err != nil {
			t.Fatalf("%s: object tool result must serialize, got %v", target, err)
		}
		if !strings.Contains(string(output), `\"ok\":true`) && !strings.Contains(string(output), `{"ok":true}`) {
			t.Fatalf("%s: serialized payload missing: %s", target, output[:min(400, len(output))])
		}
	}
	// JSON-looking text remains text inside the declared result envelope.
	jsonPayload := `{"model":"m","messages":[
		{"role":"user","content":"run"},
		{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"{\"ok\":true}"}
	],"tools":[{"type":"function","function":{"name":"f","description":"d"}}]}`
	chatRequest, err := testCompiled(t, Chat).DecodeRequest(t.Context(), testValue(t, jsonPayload).Bytes(), fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
	if err != nil {
		t.Fatal(err)
	}
	gemini := testCompiled(t, Gemini)
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(testCompiled(t, Chat), gemini))
	if err != nil {
		t.Fatal(err)
	}
	projected, err := conversion.Request(t.Context(), chatRequest, fixtureConversionContext(t, conversion, p.ConversionRequest, chatRequest.Source), nil)
	if err != nil {
		t.Fatal(err)
	}
	output, err := gemini.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
	if err != nil || !strings.Contains(string(output), `"response":{"result":"{\"ok\":true}"}`) {
		t.Fatalf("JSON-looking text must remain text in the declared result envelope: %s %v", output, err)
	}
	plainPayload := `{"model":"m","messages":[
		{"role":"user","content":"run"},
		{"role":"assistant","tool_calls":[{"id":"c2","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c2","content":"plain text"}
	],"tools":[{"type":"function","function":{"name":"f","description":"d"}}]}`
	plainRequest, err := testCompiled(t, Chat).DecodeRequest(t.Context(), testValue(t, plainPayload).Bytes(), fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}}))
	if err != nil {
		t.Fatal(err)
	}
	policy := p.DefaultConversionPolicy(testCompiled(t, Chat), gemini)
	for index := range policy.Rules {
		if policy.Rules[index].ID == "tool-results" {
			policy.Rules[index].Enabled = false
		}
	}
	disabled, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	unresolved, err := disabled.Request(t.Context(), plainRequest, fixtureConversionContext(t, disabled, p.ConversionRequest, plainRequest.Source), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gemini.EncodeRequest(t.Context(), unresolved, fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}})); !isUnsupported(err) || !strings.Contains(err.Error(), "/content") {
		t.Fatalf("unprojected text result must retain its target rejection: %v", err)
	}
}

// Gemini RECITATION/OTHER finish 映射到各目标最近语义。

func sinkHas(sink *p.DiagnosticSink, path string) bool {
	for _, issue := range sink.Issues() {
		if strings.Contains(issue.Path, path) || strings.HasPrefix(issue.Path, path) {
			return true
		}
	}
	return false
}

func isUnsupported(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unsupported_capability")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
