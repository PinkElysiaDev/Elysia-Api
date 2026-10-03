package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/gin-gonic/gin"
)

// C1 回归：/v1/responses 入口的故障转移。首个候选 500（可重试），第二个 200，
// 客户端应得 200，且两个上游都被尝试。走 transform 路径（chat_completions 上游）。
func TestResponsesFailoverToHealthyModel(t *testing.T) {
	var firstHits, secondHits int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&firstHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"upstream boom"}`)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okChatCompletionBody(t))
	}))
	defer good.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 2,
		Models: []config.ModelRef{openAIModel("m-bad", bad.URL), openAIModel("m-good", good.URL)},
	}
	s := newTestServer(t, []config.ModelGroupConfig{group})

	rec := httptest.NewRecorder()
	c, _ := newResponsesContext(rec, `{"model":"grp","input":"hi"}`)
	s.responses(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after responses failover, got %d body=%s", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&firstHits) != 1 {
		t.Fatalf("first (bad) upstream should be hit once, got %d", firstHits)
	}
	if atomic.LoadInt32(&secondHits) != 1 {
		t.Fatalf("second (good) upstream should be hit once, got %d", secondHits)
	}
}

// C1 回归：空模型组（无候选）应返回 500「no available models」，而非旧实现里
// 空 baseUrl 掉进 SSRF 校验误报的 403。
// 空组返回 404 + model_not_found(而非 500):模型不可用对客户端同义,
// 且 5xx 会触发 SDK/Codex 自动重试风暴。标准 OpenAI 错误对象四字段齐全。
func TestResponsesEmptyGroupReturnsModelNotFound(t *testing.T) {
	group := config.ModelGroupConfig{ID: "g1", Name: "grp", Enabled: true, Models: nil}
	s := newTestServer(t, []config.ModelGroupConfig{group})

	rec := httptest.NewRecorder()
	c, _ := newResponsesContext(rec, `{"model":"grp","input":"hi"}`)
	s.responses(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for empty group, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"code":"model_not_found"`, `"param":"model"`, `"type":"invalid_request_error"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("model_not_found body missing %s: %s", want, body)
		}
	}
}

func newResponsesContext(rec *httptest.ResponseRecorder, body string) (*gin.Context, *httptest.ResponseRecorder) {
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, rec
}
