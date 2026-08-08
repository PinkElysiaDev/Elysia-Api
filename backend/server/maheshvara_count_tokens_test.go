package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elysia-api/backend/relay"
)

func TestCountTokensUsesMaheshvaraEstimator(t *testing.T) {
	body := `{
		"model":"claude-test",
		"system":"follow policy",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"hello"},
			{"type":"thinking","thinking":"consider"},
			{"type":"redacted_thinking","data":"opaque"}
		]}],
		"tools":[{"name":"lookup","description":"look up data","input_schema":{"type":"object"}}]
	}`

	server := newTestServer(nil)
	canonical, err := relay.ClaudeRequestToCanonical([]byte(body))
	if err != nil {
		t.Fatalf("parse Maheshvara request: %v", err)
	}
	expected := estimateCanonicalRequestUsage(canonical, server.config.GetUsageConfig()).InputTokens

	context, recorder := messagesRequestContext(body)
	server.countTokens(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode count_tokens response: %v", err)
	}
	if response.InputTokens != expected {
		t.Fatalf("count_tokens did not use Maheshvara estimate: got %d want %d", response.InputTokens, expected)
	}
}
