package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/gin-gonic/gin"
)

// 端到端:Claude 客户端 × Responses 型上游的完整工具循环(定义→调用→
// 结果回传),验证三协议→Responses 接线后全链路可用且 call_id 配对。
func TestClaudeClientResponsesUpstreamToolLoop(t *testing.T) {
	var receivedBodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBodies = append(receivedBodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "resp_1", "object": "response", "status": "completed", "model": "gpt",
			"output": [{"type": "message", "id": "msg_1", "role": "assistant",
				"content": [{"type": "output_text", "text": "sunny"}]}]
		}`))
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true,
		Models: []config.ModelRef{{ID: "m1", Name: "gpt-x", BaseURL: upstream.URL, APIKey: "k", Platform: "responses"}},
	}
	s := newTestServer([]config.ModelGroupConfig{group})

	// 第二轮:Claude 客户端回传 tool_result(此前该链路上游 400)。
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model": "grp", "max_tokens": 64,
		"messages": [
			{"role": "user", "content": "weather?"},
			{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": {"city": "Shanghai"}}]},
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "sunny 28C"}]}
		],
		"tools": [{"name": "get_weather", "description": "w", "input_schema": {"type": "object", "properties": {"city": {"type": "string"}}}}]
	}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	// 上游收到的请求体:tools 必须是 Responses 扁平形状,input 必须含配对的
	// function_call + function_call_output(载荷非空)。
	if len(receivedBodies) == 0 {
		t.Fatal("upstream received no requests")
	}
	upstreamBody := receivedBodies[len(receivedBodies)-1]
	if strings.Contains(upstreamBody, `"input_schema"`) || strings.Contains(upstreamBody, `{"function":{"name"`) {
		t.Fatalf("upstream body must be flat Responses shape: %s", upstreamBody)
	}
	if !strings.Contains(upstreamBody, `"name":"get_weather"`) {
		t.Fatalf("tool definition lost: %s", upstreamBody)
	}
	if !strings.Contains(upstreamBody, `"type":"function_call"`) {
		t.Fatalf("function_call item missing: %s", upstreamBody)
	}
	if !strings.Contains(upstreamBody, `"call_id":"toolu_1"`) || !strings.Contains(upstreamBody, "sunny 28C") {
		t.Fatalf("function_call_output missing or payload lost: %s", upstreamBody)
	}
	// 客户端按 Claude 形态收到响应。
	if !strings.Contains(rec.Body.String(), `"type":"text"`) && !strings.Contains(rec.Body.String(), `"text":"sunny"`) {
		t.Fatalf("claude client should receive anthropic-shaped response: %s", rec.Body.String())
	}
}
