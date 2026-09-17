package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
)

// OpenAI 兼容流的标准形态:finish_reason 帧之后还有独立的 usage 尾帧(即
// stream_options.include_usage),最后才是 [DONE]。修复前解码器在
// finish_reason 帧即断流,usage 尾帧永远读不到——真实用量丢失。
func TestCustomProtocolStreamTrailingUsageAfterFinishEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	err := relay.RegisterCustomProtocol(relay.CustomProtocolConfig{
		ID: "openai-compat",
		Request: relay.CustomProtocolRequest{
			Method:       http.MethodPost,
			PathTemplate: "/v1/chat/completions",
			BodyTemplate: `{"model":{{maheshvara.model | json}},"stream":{{maheshvara.stream}}}`,
		},
		Response: relay.CustomProtocolResponse{Stream: &relay.CustomProtocolStreamMapping{
			Response: &relay.CustomProtocolResponse{
				TextPath:         "choices[0].delta.content",
				FinishReasonPath: "choices[0].finish_reason",
				UsagePath:        "usage",
			},
		}},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":22}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true,
		Models: []config.ModelRef{{ID: "m1", Name: "vendor-model", BaseURL: upstream.URL, APIKey: "k", Platform: "custom:openai-compat"}},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"Hel"`) || !strings.Contains(body, `"content":"lo"`) {
		t.Fatalf("missing text deltas: %s", body)
	}
	if !strings.Contains(body, `"prompt_tokens":11`) || !strings.Contains(body, `"completion_tokens":22`) {
		t.Fatalf("trailing usage frame must be drained into the client stream: %s", body)
	}
	if !strings.Contains(body, `"finish_reason":"stop"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("missing finish/[DONE]: %s", body)
	}
}

// 终态排水窗:上游 finish 后不关连接、也不发 [DONE] 时,流必须在短窗内干净
// 收尾(而不是挂到 5 分钟空闲超时,也不是把已完成的流标为失败)。
func TestCustomProtocolStreamDrainWindowEndsCleanly(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	err := relay.RegisterCustomProtocol(relay.CustomProtocolConfig{
		ID: "hang-after-finish",
		Request: relay.CustomProtocolRequest{
			Method:       http.MethodPost,
			PathTemplate: "/v1/chat",
			BodyTemplate: `{"model":{{maheshvara.model | json}}}`,
		},
		Response: relay.CustomProtocolResponse{Stream: &relay.CustomProtocolStreamMapping{
			Response: &relay.CustomProtocolResponse{
				TextPath:         "text",
				FinishReasonPath: "finish",
			},
		}},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"text\":\"done\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"text\":\"\",\"finish\":\"stop\"}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// 模拟 finish 后连接悬置:不发 [DONE],不主动结束,直到对端断开。
		<-r.Context().Done()
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true,
		Models: []config.ModelRef{{ID: "m1", Name: "vendor-model", BaseURL: upstream.URL, APIKey: "k", Platform: "custom:hang-after-finish"}},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	started := time.Now()
	s.chatCompletions(c)
	elapsed := time.Since(started)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected clean 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"done"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("stream must complete with content and [DONE]: %s", body)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("drain window must bound the hang, took %v", elapsed)
	}
}

// 空补全(finish_reason 有值但零输出,如内容过滤)不再判为失败——对齐内置
// 路径的豁免;[DONE] 兜底且无 finish 的空流仍是错误。
func TestCustomProtocolStreamEmptyCompletionWithFinishReasonEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	err := relay.RegisterCustomProtocol(relay.CustomProtocolConfig{
		ID: "finish-only",
		Request: relay.CustomProtocolRequest{
			Method:       http.MethodPost,
			PathTemplate: "/v1/chat",
			BodyTemplate: `{"model":{{maheshvara.model | json}}}`,
		},
		Response: relay.CustomProtocolResponse{Stream: &relay.CustomProtocolStreamMapping{
			Response: &relay.CustomProtocolResponse{FinishReasonPath: "choices[0].finish_reason"},
		}},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"content_filter\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true,
		Models: []config.ModelRef{{ID: "m1", Name: "vendor-model", BaseURL: upstream.URL, APIKey: "k", Platform: "custom:finish-only"}},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("empty completion with finish_reason must succeed, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"finish_reason":"content_filter"`) {
		t.Fatalf("finish_reason must be surfaced: %s", rec.Body.String())
	}
}

// Responses 型类型化事件流:每类事件载荷形状不同,靠 frames[] 各自映射;
// 未声明的 response.created 跳过;completed 帧以事件名终止并携带 usage。
func TestCustomProtocolResponsesStyleFramesEndToEnd(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	err := relay.RegisterCustomProtocol(relay.CustomProtocolConfig{
		ID: "responses-style",
		Request: relay.CustomProtocolRequest{
			Method:       http.MethodPost,
			PathTemplate: "/v1/responses",
			BodyTemplate: `{"model":{{maheshvara.model | json}},"stream":{{maheshvara.stream}}}`,
		},
		Response: relay.CustomProtocolResponse{Stream: &relay.CustomProtocolStreamMapping{
			Frames: []relay.CustomProtocolStreamFrame{
				{Event: "response.output_text.delta", Response: &relay.CustomProtocolResponse{TextPath: "delta"}},
				{Event: "response.reasoning_text.delta", Response: &relay.CustomProtocolResponse{ReasoningPath: "delta"}},
				{Event: "response.completed", Terminal: true, PayloadPath: "response", Response: &relay.CustomProtocolResponse{UsagePath: "usage"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.created\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"pondering\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true,
		Models: []config.ModelRef{{ID: "m1", Name: "vendor-model", BaseURL: upstream.URL, APIKey: "k", Platform: "custom:responses-style"}},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	c, rec := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"Hel"`) || !strings.Contains(body, `"content":"lo"`) {
		t.Fatalf("text deltas must map via the text frame: %s", body)
	}
	if !strings.Contains(body, `"reasoning_content":"pondering"`) {
		t.Fatalf("reasoning delta must map via its own frame: %s", body)
	}
	if !strings.Contains(body, `"prompt_tokens":3`) || !strings.Contains(body, `"completion_tokens":2`) {
		t.Fatalf("completed frame usage must be collected: %s", body)
	}
	if !strings.Contains(body, `"finish_reason":"stop"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("terminal frame must produce finish + [DONE]: %s", body)
	}
	if strings.Contains(body, `"content":"pondering"`) {
		t.Fatalf("reasoning must not leak into content: %s", body)
	}
}
