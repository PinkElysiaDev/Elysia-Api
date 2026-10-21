package server

import (
	"net/http"
	"testing"

	"github.com/bytedance/sonic"
)

func TestCountTokensDoesNotRequireGenerationBudget(t *testing.T) {
	s := newTestServer(t, nil)
	for _, body := range []string{
		`{"model":"m","messages":[{"role":"user","content":"hello"}]}`,
		`{"model":"m","system":"follow policy","messages":[{"role":"user","content":"hello"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`,
	} {
		context, recorder := messagesRequestContext(body)
		s.countTokens(contextForTest(context), context)
		flushTestResponse(context)
		var count struct {
			InputTokens int `json:"input_tokens"`
		}
		if recorder.Code != http.StatusOK || sonic.ConfigStd.Unmarshal(recorder.Body.Bytes(), &count) != nil || count.InputTokens <= 0 || count.InputTokens >= s.config.GetUsageConfig().DefaultOutputTokenEstimate {
			t.Fatal("input-only estimate includes a generation budget", recorder.Code, recorder.Body)
		}
		context, recorder = messagesRequestContext(body)
		s.chatCompletions(contextForTest(context), context)
		flushTestResponse(context)
		if recorder.Code == http.StatusOK {
			t.Fatal("generation accepted a missing budget")
		}
	}
}

func TestCountTokensRejectsUnboundScopedPayload(t *testing.T) {
	s := newTestServer(t, nil)
	context, recorder := messagesRequestContext(`{"model":"m","messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque"}]}]}`)
	s.countTokens(contextForTest(context), context)
	flushTestResponse(context)
	if recorder.Code != http.StatusBadRequest {
		t.Fatal("scoped encrypted payload accepted without provenance", recorder.Code, recorder.Body)
	}
}
