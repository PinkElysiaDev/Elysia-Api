package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

	adapter := relay.NewOpenAIAdapter(5_000_000_000)
	rendered := &relay.CustomProtocolRequestResult{
		Method: http.MethodPost,
		Path:   "/generate",
		Auth:   relay.CustomProtocolAuth{Mode: "query", Query: "api_key"},
	}
	_, err := adapter.SendCustomProtocolRequest(context.Background(), upstream.URL, "AUDIT_UPSTREAM_SECRET", rendered, false)
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

// DBG-005 回归:流内多个工具必须拿到互异且稳定的下游 index(此前每帧 Output
// 下标都是 0,两个工具的参数会串进同一状态)。分帧与 legacy 两条路径。
func TestStreamToolStableSlotIndices(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerPresetForTest(t, "anthropic-messages")
	configs, _ := PresetProtocolConfigs()
	var anthropic relay.CustomProtocolConfig
	for _, candidate := range configs {
		if candidate.ID == "anthropic-messages" {
			anthropic = candidate
		}
	}
	decoder, err := relay.NewCustomProtocolStreamDecoder(anthropic)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	frames := []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_a","name":"first","input":{}}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_b","name":"second","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"b\":1}"}}`,
	}
	slots := map[string]int{}
	for _, frame := range frames {
		events, _, err := decoder.Decode(relay.SSEEvent{Event: eventNameOf(frame), Data: frame})
		if err != nil {
			t.Fatalf("decode %s: %v", frame, err)
		}
		for _, event := range events {
			if event.Type == relay.MaheshvaraEventFunctionCallAdded {
				slots[event.ToolCallID] = event.ToolCallIndex
			}
		}
	}
	if len(slots) != 2 {
		t.Fatalf("two tools expected, got %v", slots)
	}
	if slots["call_a"] == slots["call_b"] {
		t.Fatalf("parallel tools must get distinct downstream indices: %v", slots)
	}

	// legacy ToolCallsPath 路径:两帧各一个工具,同样不得撞 index。
	legacy, err := relay.NewCustomProtocolStreamDecoder(relay.CustomProtocolConfig{
		ID:      "legacy-slots",
		Request: relay.CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"m":{{maheshvara.model | json}}}`},
		Response: relay.CustomProtocolResponse{Stream: &relay.CustomProtocolStreamMapping{
			Response: &relay.CustomProtocolResponse{ToolCallsPath: "calls"},
		}},
	})
	if err != nil {
		t.Fatalf("legacy decoder: %v", err)
	}
	legacySlots := map[string]int{}
	for _, frame := range []string{
		`{"calls":[{"id":"first","name":"first_tool","arguments":"{}"}]}`,
		`{"calls":[{"id":"second","name":"second_tool","arguments":"{}"}]}`,
	} {
		events, _, err := legacy.Decode(relay.SSEEvent{Data: frame})
		if err != nil {
			t.Fatalf("legacy decode: %v", err)
		}
		for _, event := range events {
			if event.Type == relay.MaheshvaraEventFunctionCallAdded {
				legacySlots[event.ToolCallID] = event.ToolCallIndex
			}
		}
	}
	if len(legacySlots) == 2 && legacySlots["first"] == legacySlots["second"] {
		t.Fatalf("legacy path must also get distinct indices: %v", legacySlots)
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
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	registerPresetForTest(t, "openai-chat")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"last\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	s := newTestServer(presetGroup(t, "custom:openai-chat", upstream.URL))
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
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

	relay.ClearCustomProtocols()
	registerPresetForTest(t, "gemini-generate")
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"final answer\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":3}}\n\n")
	}))
	defer upstream.Close()
	s = newTestServer(presetGroup(t, "custom:gemini-generate", upstream.URL))
	c, rec = chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
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
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	if err := relay.RegisterCustomProtocol(relay.CustomProtocolConfig{
		ID: "mapped-error",
		Request: relay.CustomProtocolRequest{
			Method:       http.MethodPost,
			PathTemplate: "/v1/chat",
			BodyTemplate: `{"model":{{maheshvara.model | json}}}`,
		},
		Response: relay.CustomProtocolResponse{ErrorPath: "error"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"quota exhausted"}}`))
	}))
	defer upstream.Close()

	s := newTestServer(presetGroup(t, "custom:mapped-error", upstream.URL))
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
