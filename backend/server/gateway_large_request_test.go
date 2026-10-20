package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

func TestGatewayLargeAnthropicRequest(t *testing.T) {
	imageSize := 9 << 20
	if os.Getenv("ELYSIA_RESOURCE_PROFILE") == "1" {
		imageSize = 31 << 20
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var input struct {
			Model    string
			Stream   bool
			Messages []json.RawMessage
		}
		if err := sonic.ConfigStd.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if input.Model != "m" || len(input.Messages) != 2 || !bytes.Contains(input.Messages[1], []byte(strings.Repeat("A", imageSize))) {
			t.Error("image/tool history lost")
		}
		if !input.Stream {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"complete"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"complete"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", frame)
		}
	}))
	defer provider.Close()
	group := config.ModelGroupConfig{ID: "g", Name: "grp", Enabled: true, Strategy: "round-robin", Models: []config.ModelRef{{ID: "m", Name: "m", BaseURL: provider.URL, Platform: "claude", APIKey: "synthetic", ToolsCapable: true, VisionCapable: true}}}
	s := newTestServerWithStore(t, []config.ModelGroupConfig{group})
	s.engine.POST("/v1/messages", s.chatCompletions)
	gateway := httptest.NewServer(s.engine)
	defer gateway.Close()
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			body := fmt.Sprintf(`{"model":"grp","stream":%t,"max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"%s"}}]}]}]}`, stream, strings.Repeat("A", imageSize))
			before := calls.Load()
			request, _ := http.NewRequest("POST", gateway.URL+"/v1/messages", io.NopCloser(strings.NewReader(body)))
			request.Header.Set("Content-Type", "application/json") // unknown length exercises chunked reading
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			result, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != 200 || !bytes.Contains(result, []byte("complete")) || calls.Load() != before+1 {
				t.Fatalf("status=%d calls=%d error=%v body=%s", response.StatusCode, calls.Load()-before, err, result)
			}
			if stream && (!bytes.Contains(result, []byte("message_stop")) || response.Trailer.Get(gatewayStreamErrorTrailer) != "") {
				t.Fatal("stream did not complete", string(result))
			}
		})
	}
}

func TestGatewayConfiguredRequestLimit(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	compiled, _ := service.Pin(protocol.PresetAnthropicID)
	ctx, recorder := adminProtocolContext("POST", "/v1/messages", strings.Repeat(" ", 1025))
	pinned, err := protocol.WithRequestBodyLimit(ctx.Request.Context(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Request = ctx.Request.WithContext(pinned)
	_, ok := s.readGatewayRequest(ctx, compiled)
	if ok || recorder.Code != 413 {
		t.Fatalf("%v %d %s", ok, recorder.Code, recorder.Body.String())
	}
	records := latestUsageRecords(t, s)
	if len(records) != 1 {
		t.Fatal("missing local rejection record")
	}
	raw, _, err := s.store.GetUsageRecordJSON(t.Context(), records[0].RequestID)
	if err != nil {
		t.Fatal(err)
	}
	var record usageRecord
	if err := sonic.ConfigStd.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.ErrorKind != "limit_exceeded" || record.UpstreamHTTPStatus != 0 || len(record.ConversionIssues) != 1 || record.ConversionIssues[0].Direction != protocol.DecodeRequest {
		t.Fatalf("wrong attribution: %+v", record.ConversionIssues)
	}
}

func TestGatewayStructuralLimitsRejectBeforeSending(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer provider.Close()
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	compiled, _ := service.Pin(protocol.PresetChatCompletionsID)
	setupGatewayModel(t, s, compiled, provider.URL)
	for _, extra := range []string{"[" + strings.Repeat("0,", 100000) + "0]", strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65)} {
		req := httptest.NewRequest("POST", "/gateway/openai-chat-completions/chat/completions", strings.NewReader(`{"model":"group","messages":[{"role":"user","content":"x"}],"extra":`+extra+`}`))
		req.Header.Set("Authorization", "Bearer gateway-test-token")
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, req)
		if rec.Code != 400 || calls.Load() != 0 || !strings.Contains(rec.Body.String(), "limit_exceeded") {
			t.Fatalf("status=%d calls=%d body=%s", rec.Code, calls.Load(), rec.Body.String())
		}
	}
}

func TestLargeRequestPreviewAndCollector(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	compiled, _ := service.Pin(protocol.PresetChatCompletionsID)
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("a", 9<<20) + `"}]}`)
	input, _ := protocol.ParseValue(body)
	definition, _ := protocol.EncodeValue(compiled.Definition())
	envelope, _ := sonic.ConfigStd.Marshal(protocol.PreviewInput{Definition: definition, Direction: protocol.DecodeRequest, Input: input})
	ctx, rec := adminProtocolContext("POST", "/api/admin/protocols/preview", string(envelope))
	s.adminProtocolPreviewV2(ctx)
	if rec.Code != 200 {
		t.Fatalf("preview status %d", rec.Code)
	}
	var result struct{ Data protocol.PreviewResult }
	if err := sonic.ConfigStd.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if err := protocol.IssuesError(result.Data.Issues); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		received, err := io.ReadAll(r.Body)
		if err != nil || len(received) < 9<<20 {
			t.Error("collector truncated request", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"complete"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer provider.Close()
	var operation protocol.Operation
	var operationName string
	for name, entry := range compiled.Operations() {
		if entry.Kind == "generate" && entry.Transport == protocol.HTTPJSON {
			operation, operationName = entry, name
		}
	}
	conversion, err := protocol.ResolveConversion(protocol.DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	candidate := gatewayCandidate{compiled: compiled, conversion: conversion, operation: operation, operationName: operationName, model: config.ModelRef{ID: "m", Name: "m", BaseURL: provider.URL}, binding: protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: compiled.Definition().Capabilities, Transports: []protocol.Transport{protocol.HTTPJSON}}}
	request, err := compiled.DecodeRequest(t.Context(), body, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	response, err := s.collectProtocolGeneration(t.Context(), candidate, request, &usageRecord{}, nil)
	if err != nil || response == nil || calls.Load() != 1 {
		t.Fatal("collector failed", err, calls.Load())
	}
}
