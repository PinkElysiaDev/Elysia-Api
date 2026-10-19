package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	p "github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func TestGatewayNewAPIErrorKeepsHTTPFailure(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(p.PresetResponsesID)
	raw := auditOctober14Fixture(t, "new_api_error")
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = w.Write(raw)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, ingress := range []struct{ id, path, body string }{
		{p.PresetChatCompletionsID, "/chat/completions", `{"model":"group","messages":[{"role":"user","content":"hi"}],"stream":%v}`},
		{p.PresetResponsesID, "/responses", `{"model":"group","input":"hi","store":false,"stream":%v}`},
		{p.PresetAnthropicID, "/v1/messages", `{"model":"group","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"stream":%v}`},
		{p.PresetGeminiID, "/v1beta/models/group:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`},
	} {
		for _, isStream := range []bool{false, true} {
			body, path := ingress.body, ingress.path
			if ingress.id == p.PresetGeminiID {
				if isStream {
					path = "/v1beta/models/group:streamGenerateContent"
				}
			} else {
				body = fmt.Sprintf(body, isStream)
			}
			req := httptest.NewRequest("POST", "/gateway/"+ingress.id+path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer gateway-test-token")
			out := httptest.NewRecorder()
			before := calls.Load()
			s.engine.ServeHTTP(out, req)
			if out.Code != 400 || calls.Load() != before+1 {
				t.Fatal(out.Code, calls.Load(), out.Body.String())
			}
			for _, detail := range []string{"new_api_error", "input_tokens_out_of_range", "Synthetic provider rejected the input"} {
				if !strings.Contains(out.Body.String(), detail) {
					t.Fatal("missing detail", ingress.id, isStream, out.Body.String())
				}
			}
			records := latestUsageRecords(t, s)
			stored, _, err := s.store.GetUsageRecordJSON(t.Context(), records[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var record usageRecord
			if err := json.Unmarshal(stored, &record); err != nil {
				t.Fatal(err)
			}
			if record.UpstreamHTTPStatus != 400 || record.StatusCode != 400 || record.ErrorKind != "invalid_request" || strings.Contains(record.Error, "error conversion failed") {
				t.Fatal(record.ErrorKind, record.Error, record.UpstreamHTTPStatus, record.StatusCode)
			}
			for _, issue := range record.ConversionIssues {
				if issue.Severity == p.SeverityError {
					t.Fatal("secondary conversion failure", issue)
				}
			}
		}
	}
	var operation p.Operation
	for _, entry := range upstream.Operations() {
		if entry.Kind == "generate" && entry.Transport == p.HTTPJSON {
			operation = entry
		}
	}
	conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(upstream, upstream))
	candidate := gatewayCandidate{compiled: upstream, conversion: conversion, operation: operation, model: config.ModelRef{ID: "m", Name: "m", BaseURL: provider.URL}, binding: p.Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: upstream.Definition().Capabilities, Transports: []p.Transport{p.HTTPJSON}}}
	request, err := upstream.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"synthetic"}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	record := &usageRecord{}
	result, err := s.collectProtocolGeneration(t.Context(), candidate, request, record, nil)
	var failure *upstreamHTTPFailure
	if result == nil || !errors.As(err, &failure) || failure.status != 400 || generationFailureClass(err) != builtin.ErrorClassInvalidRequest {
		t.Fatal(result, err)
	}
}
