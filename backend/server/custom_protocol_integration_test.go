package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

func registerIntegrationCustomProtocol(t *testing.T) {
	t.Helper()
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	err := relay.RegisterCustomProtocol(relay.CustomProtocolConfig{
		ID: "vendor-json",
		Request: relay.CustomProtocolRequest{
			Method:       http.MethodPost,
			PathTemplate: "/v2/generate/{{maheshvara.model}}",
			Headers: map[string]string{
				"X-Model": "{{maheshvara.model}}",
			},
			BodyTemplate: `{"model":"{{maheshvara.model}}","messages":{{maheshvara.messages}},"temperature":{{maheshvara.temperature | default:0.2}},"stream":{{maheshvara.stream}}}`,
		},
		Response: relay.CustomProtocolResponse{
			TextPath:         "answer.text",
			FinishReasonPath: "finish",
			UsagePath:        "usage",
		},
	})
	if err != nil {
		t.Fatalf("register custom protocol: %v", err)
	}
}

func TestChatCompletionsCustomProtocolEndToEnd(t *testing.T) {
	registerIntegrationCustomProtocol(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v2/generate/vendor-model" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Model") != "vendor-model" {
			t.Errorf("unexpected X-Model header: %q", r.Header.Get("X-Model"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("request body is not JSON: %v", err)
			return
		}
		if request["model"] != "vendor-model" {
			t.Errorf("unexpected request model: %#v", request["model"])
		}
		messages, ok := request["messages"].([]any)
		if !ok || len(messages) != 1 {
			t.Errorf("expected one rendered message, got %#v", request["messages"])
		}
		if temperature, ok := request["temperature"].(float64); !ok || temperature != 0.4 {
			t.Errorf("unexpected temperature: %#v", request["temperature"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"answer":{"text":"ok"},"finish":"stop","usage":{"prompt_tokens":2,"completion_tokens":3}}`)
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID:      "g1",
		Name:    "grp",
		Enabled: true,
		Models:  []config.ModelRef{{ID: "m1", Name: "vendor-model", BaseURL: upstream.URL, APIKey: "test-key", Platform: "custom:vendor-json"}},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	c, rec := chatRequestContext(`{"model":"grp","messages":[{"role":"user","content":"hello"}],"temperature":0.4}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response relay.OpenAIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode downstream response: %v", err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "ok" {
		t.Fatalf("unexpected downstream response: %s", rec.Body.String())
	}
}

func TestResponsesCustomProtocolEndToEnd(t *testing.T) {
	registerIntegrationCustomProtocol(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/generate/vendor-model" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		if !strings.Contains(string(body), `"model":"vendor-model"`) {
			t.Errorf("custom model was not rendered: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"answer":{"text":"responses-ok"},"finish":"stop"}`)
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID:      "g1",
		Name:    "grp",
		Enabled: true,
		Models:  []config.ModelRef{{ID: "m1", Name: "vendor-model", BaseURL: upstream.URL, APIKey: "test-key", Platform: "custom:vendor-json"}},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	rec := httptest.NewRecorder()
	c, _ := newResponsesContext(rec, `{"model":"grp","input":"hello"}`)
	s.responses(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response relay.OpenAIResponsesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode downstream Responses response: %v", err)
	}
	if len(response.Output) == 0 || response.Output[0].Content == nil {
		t.Fatalf("expected Responses output content: %s", rec.Body.String())
	}
	if len(response.Output[0].Content) == 0 || response.Output[0].Content[0].Text != "responses-ok" {
		t.Fatalf("unexpected Responses output: %s", rec.Body.String())
	}
}

func TestAnthropicCustomProtocolEndToEnd(t *testing.T) {
	registerIntegrationCustomProtocol(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"answer":{"text":"anthropic-ok"},"finish":"end_turn"}`)
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID:      "g1",
		Name:    "grp",
		Enabled: true,
		Models:  []config.ModelRef{{ID: "m1", Name: "vendor-model", BaseURL: upstream.URL, APIKey: "test-key", Platform: "custom:vendor-json"}},
	}
	server := newTestServer([]config.ModelGroupConfig{group})
	context, recorder := messagesRequestContext(`{"model":"grp","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`)
	server.chatCompletions(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response relay.ClaudeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode Anthropic response: %v", err)
	}
	if len(response.Content) != 1 || response.Content[0].Text != "anthropic-ok" {
		t.Fatalf("unexpected Anthropic custom response: %s", recorder.Body.String())
	}
}

func TestGeminiCustomProtocolEndToEnd(t *testing.T) {
	registerIntegrationCustomProtocol(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"answer":{"text":"gemini-ok"},"finish":"STOP"}`)
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID:      "g1",
		Name:    "grp",
		Enabled: true,
		Models:  []config.ModelRef{{ID: "m1", Name: "vendor-model", BaseURL: upstream.URL, APIKey: "test-key", Platform: "custom:vendor-json"}},
	}
	server := newTestServer([]config.ModelGroupConfig{group})
	context, recorder := chatRequestContext(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	context.Request.URL.Path = "/v1beta/models/grp:generateContent"
	context.Params = gin.Params{{Key: "action", Value: "/grp:generateContent"}}
	server.chatCompletions(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response relay.GeminiResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode Gemini response: %v", err)
	}
	if len(response.Candidates) != 1 || len(response.Candidates[0].Content.Parts) != 1 || response.Candidates[0].Content.Parts[0].Text != "gemini-ok" {
		t.Fatalf("unexpected Gemini custom response: %s", recorder.Body.String())
	}
}

func TestGeminiCustomProtocolStreamURLSetsMaheshvaraStream(t *testing.T) {
	registerIntegrationCustomProtocol(t)
	streamValue := make(chan bool, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read custom Gemini stream body: %v", err)
			return
		}
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode custom Gemini stream body: %v", err)
			return
		}
		streamValue <- request["stream"] == true
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"answer\":{\"text\":\"hello\"}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID:      "g1",
		Name:    "grp",
		Enabled: true,
		Models:  []config.ModelRef{{ID: "m1", Name: "vendor-model", BaseURL: upstream.URL, APIKey: "test-key", Platform: "custom:vendor-json"}},
	}
	server := newTestServer([]config.ModelGroupConfig{group})
	context, recorder := chatRequestContext(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	context.Request.URL.Path = "/v1beta/models/grp:streamGenerateContent"
	context.Params = gin.Params{{Key: "action", Value: "/grp:streamGenerateContent"}}
	server.chatCompletions(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if streamed := <-streamValue; !streamed {
		t.Fatal("Gemini stream URL did not set maheshvara.stream for the custom request template")
	}
}

func TestChatCompletionsCustomProtocolStreamingEndToEnd(t *testing.T) {
	registerIntegrationCustomProtocol(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"answer\":{\"text\":\"hello\"}}\n\n")
		_, _ = io.WriteString(w, "data: {\"answer\":{\"text\":\" world\"},\"finish\":\"stop\"}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID:      "g1",
		Name:    "grp",
		Enabled: true,
		Models:  []config.ModelRef{{ID: "m1", Name: "vendor-model", BaseURL: upstream.URL, APIKey: "test-key", Platform: "custom:vendor-json"}},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"hello"`) || !strings.Contains(body, `"content":" world"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("unexpected custom stream output: %s", body)
	}
}

func TestChatCompletionsCustomProtocolCumulativeMultilineStreamingEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	err := relay.RegisterCustomProtocol(relay.CustomProtocolConfig{
		ID: "vendor-cumulative",
		Request: relay.CustomProtocolRequest{
			Method:       http.MethodPost,
			PathTemplate: "/v2/stream",
			BodyTemplate: `{"model":{{maheshvara.model | json}},"stream":{{maheshvara.stream}}}`,
		},
		Response: relay.CustomProtocolResponse{Stream: &relay.CustomProtocolStreamMapping{
			PayloadPath: "payload",
			Mode:        "cumulative",
			DoneValues:  []string{"END"},
			Events:      []string{"message"},
			Response:    &relay.CustomProtocolResponse{TextPath: "text"},
		}},
	})
	if err != nil {
		t.Fatalf("register cumulative custom protocol: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: ignored\ndata: {\"payload\":{\"text\":\"ignored\"}}\n\n")
		_, _ = io.WriteString(w, "event: message\ndata: {\"payload\":{\"text\":\ndata: \"hel\"}}\n\n")
		_, _ = io.WriteString(w, "event: message\ndata: {\"payload\":{\"text\":\"hello\"}}\n\n")
		_, _ = io.WriteString(w, "event: message\ndata: END\n\n")
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID:      "g1",
		Name:    "grp",
		Enabled: true,
		Models:  []config.ModelRef{{ID: "m1", Name: "vendor-model", BaseURL: upstream.URL, APIKey: "test-key", Platform: "custom:vendor-cumulative"}},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"hel"`) || !strings.Contains(body, `"content":"lo"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("unexpected cumulative custom stream output: %s", body)
	}
	if strings.Contains(body, `"content":"ignored"`) || strings.Contains(body, `"content":"hello"`) {
		t.Fatalf("event filtering or cumulative delta handling failed: %s", body)
	}
}
