package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

// DBG-001 回归:删除某 token 的唯一授权组后,该 token 必须被禁用,而不是因
// 「空列表=不限制」扩权为全组可用;多组/无限制 token 不受影响。
func TestDeleteLastAllowedGroupDisablesToken(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	store := s.store
	ctx := context.Background()

	seed := func(id, name string) {
		if err := store.UpsertGroup(ctx, storage.ModelGroup{ID: id, Name: name, Enabled: true}); err != nil {
			t.Fatalf("seed group %s: %v", name, err)
		}
	}
	seed("g-allowed", "allowed-group")
	seed("g-other", "restricted-group")
	seed("g-multi", "multi-group")

	tokens := []storage.APIToken{
		{Name: "restricted", Token: "sk-restricted", Enabled: true, AllowedGroups: []string{"allowed-group"}},
		{Name: "multi", Token: "sk-multi", Enabled: true, AllowedGroups: []string{"allowed-group", "multi-group"}},
		{Name: "unrestricted", Token: "sk-open", Enabled: true},
	}
	for _, token := range tokens {
		if err := store.UpsertAPIToken(ctx, token); err != nil {
			t.Fatalf("seed token %s: %v", token.Name, err)
		}
	}

	disabled, err := store.DeleteGroup(ctx, "g-allowed")
	if err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if len(disabled) != 1 || disabled[0] != "restricted" {
		t.Fatalf("only the single-group token must be disabled, got %v", disabled)
	}

	byName := map[string]storage.APIToken{}
	all, err := store.ListAPITokens(ctx)
	if err != nil {
		t.Fatalf("ListAPITokens: %v", err)
	}
	for _, token := range all {
		byName[token.Name] = token
	}
	if byName["restricted"].Enabled {
		t.Fatal("token restricted must be disabled after its only allowed group is deleted")
	}
	if len(byName["restricted"].AllowedGroups) != 0 {
		t.Fatalf("dangling group reference must be removed: %v", byName["restricted"].AllowedGroups)
	}
	if !byName["multi"].Enabled || len(byName["multi"].AllowedGroups) != 1 || byName["multi"].AllowedGroups[0] != "multi-group" {
		t.Fatalf("multi-group token must survive with its remaining group: %+v", byName["multi"])
	}
	if !byName["unrestricted"].Enabled || len(byName["unrestricted"].AllowedGroups) != 0 {
		t.Fatalf("unrestricted token must be untouched: %+v", byName["unrestricted"])
	}

	// 管理响应透出禁用名单。
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/admin/groups/g-multi", nil)
	c.Params = gin.Params{{Key: "id", Value: "g-multi"}}
	s.adminDeleteGroup(c)
	if !strings.Contains(rec.Body.String(), `"disabledTokens":["multi"]`) {
		t.Fatalf("admin response must surface disabled tokens: %s", rec.Body.String())
	}
}

// DBG-002 回归:query 鉴权的 API Key 不得随网络错误文本流向下游——传输错误
// 统一剥离 URL 查询串。
func TestQueryAuthSecretSanitizedInTransportError(t *testing.T) {
	// 上游接受连接后立即关闭,制造携带 URL 的传输错误。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer upstream.Close()

	transport := relay.NewProtocolTransport(5_000_000_000)
	operation := protocol.Operation{
		Method:    http.MethodPost,
		Path:      "/generate",
		Transport: protocol.HTTPJSON,
		Auth:      protocol.Credential{Location: "query", Name: "api_key"},
	}
	_, err := transport.SendProtocolRequest(context.Background(), upstream.URL, "AUDIT_UPSTREAM_SECRET", operation, nil, nil)
	if err == nil {
		t.Fatal("expected a transport error from the closed connection")
	}
	if strings.Contains(err.Error(), "AUDIT_UPSTREAM_SECRET") {
		t.Fatalf("transport error must not leak the query auth key: %v", err)
	}
	if !strings.Contains(err.Error(), "/generate") {
		t.Fatalf("sanitized error should keep the non-secret URL path for diagnosis: %v", err)
	}
}

// Interleaved tool arguments must retain distinct, stable downstream slots.
func TestStreamToolStableSlotIndices(t *testing.T) {
	decoder := compileFixtureDefinition(t, presetDefinition(t, "anthropic-messages"))
	encoder := compileFixtureDefinition(t, presetDefinition(t, "openai-chat-completions"))
	decodeOptions := protocol.EvaluationContext{State: protocol.NewEvaluationState()}
	encodeOptions := protocol.EvaluationContext{State: protocol.NewEvaluationState()}
	frames := []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_a","name":"first","input":{}}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_b","name":"second","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"b\":1}"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"2}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`{"type":"message_stop"}`,
	}
	slots := map[string]int{}
	arguments := map[int]string{}
	for _, raw := range frames {
		frame, err := decoder.DecodeFrame(t.Context(), mustProtocolValue(t, raw), decodeOptions)
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := encoder.EncodeFrame(t.Context(), frame, encodeOptions)
		if err != nil {
			t.Fatal(err)
		}
		for _, chunk := range chunks {
			var wire struct {
				Choices []struct {
					Delta struct {
						Tools []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Function struct {
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := chunk.Decode(&wire); err != nil {
				t.Fatal(err)
			}
			for _, choice := range wire.Choices {
				for _, tool := range choice.Delta.Tools {
					if tool.ID != "" {
						if previous, exists := slots[tool.ID]; exists && previous != tool.Index {
							t.Fatal("tool changed slot")
						}
						slots[tool.ID] = tool.Index
					}
					arguments[tool.Index] += tool.Function.Arguments
				}
			}
		}
	}
	if len(slots) != 2 || slots["call_a"] == slots["call_b"] {
		t.Fatalf("distinct tools expected: %v", slots)
	}
	if arguments[slots["call_a"]] != `{"a":2}` || arguments[slots["call_b"]] != `{"b":1}` {
		t.Fatalf("interleaved arguments crossed slots: %v", arguments)
	}
}

func eventNameOf(frame string) string {
	var parsed map[string]any
	_ = json.Unmarshal([]byte(frame), &parsed)
	name, _ := parsed["type"].(string)
	return name
}

// DBG-007 回归:复合帧(正文+结束+usage 同帧)不得因 first-match 只映射一类
// 字段而丢失其余——预设每帧全量映射。
func TestPresetCombinedFramesEndToEnd(t *testing.T) {

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"last\"},\"finish_reason\":\"stop\",\"index\":0}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3},\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	s := newTestServer(t, presetGroup(t, "custom:openai-chat-completions", upstream.URL))
	c, rec := chatRequestContext(`{"model":"grp","max_tokens":64,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat combined frame: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"content":"last"`, `"finish_reason":"stop"`, `"prompt_tokens":2`, "data: [DONE]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("chat combined frame must keep text+finish+usage, missing %s: %s", want, body)
		}
	}

	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"final answer\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":3}}\n\n")
	}))
	defer upstream.Close()
	s = newTestServer(t, presetGroup(t, "custom:google-generate-content", upstream.URL))
	c, rec = chatRequestContext(`{"model":"grp","max_tokens":64,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("gemini combined frame: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	for _, want := range []string{`"content":"final answer"`, `"prompt_tokens":2`, `"finish_reason":"stop"`, "data: [DONE]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("gemini combined frame must keep text+finish+usage, missing %s: %s", want, body)
		}
	}
}

// DBG-008 回归:HTTP 200 携带业务错误(ErrorPath 显式映射)必须渲染为失败,
// 不得包装成空答案的成功响应。
func TestCustomProtocolMappedErrorIsFailure(t *testing.T) {
	definition := standaloneWireDefinition(t, protocol.HTTPJSON)
	for _, direction := range []protocol.Direction{protocol.DecodeResponse, protocol.EncodeResponse} {
		mapping := definition.Directions[direction]
		errorFields := map[string]protocol.Expression{"error": {Op: "read", Path: "/error", Required: true}}
		if direction == protocol.DecodeResponse {
			errorFields["schemaVersion"] = protocol.Expression{Op: "literal", Value: mustProtocolValue(t, `1`)}
			errorFields["content"] = protocol.Expression{Op: "literal", Value: mustProtocolValue(t, `[]`)}
		}
		mapping.Transform = &protocol.Expression{Op: "if", When: &protocol.Expression{Op: "exists", Source: &protocol.Expression{Op: "read", Path: "/error"}}, Then: &protocol.Expression{Op: "object", Fields: errorFields}, Otherwise: mapping.Transform}
		definition.Directions[direction] = mapping
	}
	errorWire := mustProtocolValue(t, `{"error":{"message":"quota exhausted","category":"upstream"}}`)
	errorSemantic := mustProtocolValue(t, `{"schemaVersion":1,"content":[],"error":{"message":"quota exhausted","category":"upstream"}}`)
	definition.Samples = append(definition.Samples,
		protocol.Sample{ID: "failure.decode", Direction: protocol.DecodeResponse, Input: errorWire, Expected: errorSemantic},
		protocol.Sample{ID: "failure.encode", Direction: protocol.EncodeResponse, Input: errorSemantic, Expected: errorWire})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"quota exhausted","category":"upstream"}}`))
	}))
	defer upstream.Close()

	s := newTestServer(t, standaloneGroup(t, definition, upstream.URL), definition)
	c, rec := chatRequestContext(`{"model":"grp","messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	if rec.Code == http.StatusOK {
		t.Fatalf("mapped business error must not surface as 200: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "quota exhausted") {
		t.Fatalf("mapped error message must be preserved: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"content":""`) {
		t.Fatalf("empty-answer success shape must be gone: %s", rec.Body.String())
	}
}

// DBG-009 回归:同一 delta 帧携带多个工具时全部产出(tool.path 数组遍历)。
func TestPresetChatMultipleToolsInOneFrame(t *testing.T) {

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"name\":\"first\",\"arguments\":\"{}\"}},{\"index\":1,\"id\":\"call_b\",\"function\":{\"name\":\"second\",\"arguments\":\"{}\"}}]},\"index\":0}],\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\",\"index\":0}],\"id\":\"r1\",\"model\":\"m\",\"created\":1,\"object\":\"chat.completion.chunk\"}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	s := newTestServer(t, presetGroup(t, "custom:openai-chat-completions", upstream.URL))
	c, rec := chatRequestContext(`{"model":"grp","max_tokens":64,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"name":"first"`) || !strings.Contains(body, `"name":"second"`) {
		t.Fatalf("both tools in one frame must be extracted: %s", body)
	}
}

// DBG-010 回归:预设的失败帧必须映射为流失败,终止后错误同样生效。
func TestPresetErrorFramesEndToEnd(t *testing.T) {

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n")
		_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"overloaded\"}}\n\n")
	}))
	defer upstream.Close()

	s := newTestServer(t, presetGroup(t, "custom:anthropic-messages", upstream.URL))
	c, rec := chatRequestContext(`{"model":"grp","max_tokens":64,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	if !strings.Contains(rec.Body.String(), "overloaded") {
		t.Fatalf("trailing error frame must surface as failure: %s", rec.Body.String())
	}
}

// Arbitrary protocol IDs use the same target tool-choice semantics.
func TestShapeAnthropicToolChoice(t *testing.T) {
	decoder := compileFixtureDefinition(t, presetDefinition(t, "openai-chat-completions"))
	definition := presetDefinition(t, "anthropic-messages")
	definition.ID = "user-tool-choice"
	encoder := compileFixtureDefinition(t, definition)
	request, err := decoder.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}],"tool_choice":"required"}`), protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := encoder.EncodeRequest(t.Context(), request, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		ToolChoice struct {
			Type string `json:"type"`
		} `json:"tool_choice"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.ToolChoice.Type != "any" || len(wire.Tools) != 1 || wire.Tools[0].Name != "f" {
		t.Fatalf("target tool choice and definition: %s", body)
	}
}
