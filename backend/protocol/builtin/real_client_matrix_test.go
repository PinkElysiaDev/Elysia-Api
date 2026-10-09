package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// 人工协议边界夹具：包含非标准中段 system 消息，不能视为已捕获的 Claude Code 流量。
// 另覆盖 Responses 消息 item id、Gemini 对象工具结果和 Chat strict 工具，锁定四协议
// 点对点的保证行为——正确转换或显式诊断。

const syntheticAnthropicHistory = `{
	"model":"claude-opus-5-5","max_tokens":8192,"stream":true,
	"system":[{"type":"text","text":"You are Claude Code.","cache_control":{"type":"ephemeral"}}],
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

// In-message system is not an Anthropic wire role. The semantic hoist action
// must resolve scope and cache boundaries before this encoder can accept it.
func TestAnthropicRejectsUnprojectedInPlaceSystem(t *testing.T) {
	compiled := testCompiled(t, Anthropic)
	request, err := compiled.DecodeRequest(t.Context(), testValue(t, syntheticAnthropicHistory).Bytes(), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{Scope: p.Scope{Model: "m"}}); !isUnsupported(err) {
		t.Fatalf("unprojected in-message system must fail: %v", err)
	}
}

// 人工 Anthropic → Chat：并行 tool_result 拆为多条 tool 消息、is_error 文本标记、
// strict 剥离、thinking 签名丢弃——全部转成 Chat 可表达形态。
func TestSyntheticAnthropicToChatConvertsToolResultGroup(t *testing.T) {
	source := testCompiled(t, Anthropic)
	target := testCompiled(t, Chat)
	request, err := source.DecodeRequest(t.Context(), testValue(t, syntheticAnthropicHistory).Bytes(), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	sink := &p.DiagnosticSink{}
	output, err := target.EncodeRequest(t.Context(), request, p.EvaluationContext{Scope: p.Scope{Model: "m"}, Diagnostics: sink})
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
	for _, expected := range []string{`"tool_call_id":"t1"`, `"tool_call_id":"t2"`, "[Tool error] err"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %s in output: %s", expected, body[:min(600, len(body))])
		}
	}
	if !sinkHas(sink, "/content/result") {
		t.Fatal("expected explicit warning for result metadata drop")
	}
	recovered, err := target.DecodeRequest(t.Context(), output, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
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
	request, err := source.DecodeRequest(t.Context(), testValue(t, syntheticAnthropicHistory).Bytes(), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.EncodeRequest(t.Context(), request, p.EvaluationContext{Scope: p.Scope{Model: "m"}}); !isUnsupported(err) {
		t.Fatalf("gemini must keep the explicit diagnostic, got %v", err)
	}
}

// Codex/Responses 客户端：消息 item id/status 不再硬拒——跨族丢弃 + warning。
func TestResponsesItemIdentityConvertsCrossFamily(t *testing.T) {
	payload := `{"model":"gpt","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}],"id":"msg_1","status":"completed"}
	],"tools":[{"type":"function","name":"f","description":"d","parameters":{"type":"object"},"strict":true}]}`
	source := testCompiled(t, Responses)
	request, err := source.DecodeRequest(t.Context(), testValue(t, payload).Bytes(), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{Chat, Anthropic, Gemini} {
		compiled := testCompiled(t, target)
		sink := &p.DiagnosticSink{}
		if _, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{Scope: p.Scope{Model: "m"}, Diagnostics: sink}); err != nil {
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
	request, err := source.DecodeRequest(t.Context(), testValue(t, payload).Bytes(), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{Chat, Responses, Anthropic} {
		compiled := testCompiled(t, target)
		conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(source, compiled))
		converted, e := conversion.Request(t.Context(), request, p.ConversionContext{}, nil)
		if e != nil {
			t.Fatal(e)
		}
		output, err := compiled.EncodeRequest(t.Context(), converted, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
		if err != nil {
			t.Fatalf("%s: object tool result must serialize, got %v", target, err)
		}
		if !strings.Contains(string(output), `\"ok\":true`) && !strings.Contains(string(output), `{"ok":true}`) {
			t.Fatalf("%s: serialized payload missing: %s", target, output[:min(400, len(output))])
		}
	}
	// Chat 字符串结果可解析为 JSON 时转回 Gemini 对象；纯文本结果整请求显式拒绝。
	jsonPayload := `{"messages":[
		{"role":"user","content":"run"},
		{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"{\"ok\":true}"}
	],"tools":[{"type":"function","function":{"name":"f","description":"d"}}]}`
	chatRequest, err := testCompiled(t, Chat).DecodeRequest(t.Context(), testValue(t, jsonPayload).Bytes(), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	gemini := testCompiled(t, Gemini)
	output, err := gemini.EncodeRequest(t.Context(), chatRequest, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil || !strings.Contains(string(output), `"functionResponse"`) {
		t.Fatalf("JSON string must convert to a Gemini object response: %v", err)
	}
	plainPayload := `{"messages":[
		{"role":"user","content":"run"},
		{"role":"assistant","tool_calls":[{"id":"c2","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c2","content":"plain text"}
	],"tools":[{"type":"function","function":{"name":"f","description":"d"}}]}`
	plainRequest, err := testCompiled(t, Chat).DecodeRequest(t.Context(), testValue(t, plainPayload).Bytes(), p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gemini.EncodeRequest(t.Context(), plainRequest, p.EvaluationContext{Scope: p.Scope{Model: "m"}}); !isUnsupported(err) {
		t.Fatalf("plain-text tool result must stay rejected by Gemini: %v", err)
	}
}

// Gemini RECITATION/OTHER finish 映射到各目标最近语义。
func TestGeminiExclusiveFinishReasonsMap(t *testing.T) {
	cases := []struct{ semantic string }{{"recitation"}, {"other"}}
	for _, attempt := range cases {
		for target, expected := range map[string]string{Chat: "", Anthropic: "", Gemini: ""} {
			value, err := encodeFinishReason(target, p.StringValue(attempt.semantic))
			if err != nil {
				t.Fatalf("%s %s: %v", attempt.semantic, target, err)
			}
			if value.IsZero() || value.IsNull() {
				t.Fatalf("%s %s: empty mapping", attempt.semantic, target)
			}
			_ = expected
		}
	}
	if value, _ := encodeFinishReason(Chat, p.StringValue("recitation")); string(value.Bytes()) != `"content_filter"` {
		t.Fatalf("recitation→chat: %s", value.Bytes())
	}
	if value, _ := encodeFinishReason(Anthropic, p.StringValue("other")); string(value.Bytes()) != `"end_turn"` {
		t.Fatalf("other→anthropic: %s", value.Bytes())
	}
}

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
