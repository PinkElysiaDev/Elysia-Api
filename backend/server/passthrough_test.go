package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/gin-gonic/gin"
)

func claudeModel(name, baseURL string) config.ModelRef {
	return config.ModelRef{ID: name, Name: name, BaseURL: baseURL, Platform: "anthropic", APIKey: "test-key"}
}

func messagesRequestContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	// chatCompletions 用路径后缀判定 inputFormat=Claude。
	c.Request.URL.Path = "/v1/messages"
	return c, rec
}

// Claude→Anthropic 同源：应走透传，上游收到的请求体保留客户端原始字段
// （含 unified 模型不携带的 cache_control / 未知扩展），且 model 被改写为上游模型名。
func TestChatCompletionsClaudePassthroughPreservesFields(t *testing.T) {
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-claude","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 1,
		Models: []config.ModelRef{claudeModel("upstream-claude", upstream.URL)},
	}
	s := newTestServer([]config.ModelGroupConfig{group})

	// 含 cache_control（unified 模型不携带）与未知扩展字段，验证透传保真。
	reqBody := `{"model":"grp","system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}],"x_future_flag":true}`
	c, rec := messagesRequestContext(reqBody)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var sent map[string]any
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("upstream body not JSON: %v (%s)", err, gotBody)
	}
	if sent["model"] != "upstream-claude" {
		t.Errorf("model not rewritten for routing: got %v", sent["model"])
	}
	if _, ok := sent["x_future_flag"]; !ok {
		t.Errorf("unknown field x_future_flag dropped — passthrough not active")
	}
	// cache_control 嵌在 system[0]，unified 往返会丢失；透传应保留。
	system, ok := sent["system"].([]any)
	if !ok || len(system) == 0 {
		t.Fatalf("system field lost: %v", sent["system"])
	}
	first, _ := system[0].(map[string]any)
	if _, ok := first["cache_control"]; !ok {
		t.Errorf("cache_control dropped — passthrough not preserving rich content")
	}
}

// 关闭 passthrough 后，同源请求应回退到转换路径（unified 往返），
// 此时上游收到的请求体不再含未知扩展字段。
func TestChatCompletionsPassthroughDisabledFallsBackToConvert(t *testing.T) {
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-claude","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 1,
		Models: []config.ModelRef{claudeModel("upstream-claude", upstream.URL)},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	disabled := false
	s.config.Relay = config.RelayConfig{Passthrough: &disabled}

	reqBody := `{"model":"grp","messages":[{"role":"user","content":"hello"}],"x_future_flag":true}`
	c, rec := messagesRequestContext(reqBody)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var sent map[string]any
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("upstream body not JSON: %v", err)
	}
	if _, ok := sent["x_future_flag"]; ok {
		t.Errorf("convert path must not carry unknown field x_future_flag through unified model")
	}
}
