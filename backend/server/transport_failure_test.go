package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	p "github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

func TestTransportFailureAttribution(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(p.PresetChatCompletionsID)
	var operation p.Operation
	for _, entry := range upstream.Operations() {
		if entry.Kind == "generate" && entry.Transport == p.SSE {
			operation = entry
		}
	}
	framing := *operation.Framing
	framing.IdleMillis = 100
	operation.Framing = &framing
	conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(upstream, upstream))
	request, err := upstream.DecodeRequest(t.Context(), []byte(`{"model":"m","stream":true,"messages":[{"role":"user","content":"synthetic"}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"awaiting_response", "reading_body", "http_failure", "provider_failure", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if scenario == "cancel" {
					cancel()
				}
				if scenario != "awaiting_response" && scenario != "cancel" {
					if scenario == "http_failure" {
						w.WriteHeader(400)
						io.WriteString(w, `{"error":`)
					} else {
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "data: {\"object\":\"chat.completion.chunk\",\"id\":\"c1\",\"model\":\"m\",\"created\":1,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"synthetic\"},\"finish_reason\":null}]}\n\n")
						if scenario == "provider_failure" {
							io.WriteString(w, "data: {\"error\":{\"type\":\"new_api_error\",\"code\":\"gateway_concurrency_limit\",\"message\":\"synthetic provider failure\"}}\n\n")
						}
					}
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer provider.Close()
			candidate := gatewayCandidate{compiled: upstream, conversion: conversion, operation: operation, model: config.ModelRef{ID: "m", Name: "m", BaseURL: provider.URL}, binding: p.Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: upstream.Definition().Capabilities, Transports: []p.Transport{p.SSE}}}
			record := &usageRecord{}
			_, err := s.collectProtocolGeneration(ctx, candidate, request, record, nil)
			if err == nil || calls.Load() != 1 {
				t.Fatal("missing failure or generation repeated", err, calls.Load())
			}
			setUsageError(record, ctx, err)
			out := httptest.NewRecorder()
			gateway, _ := gin.CreateTestContext(out)
			gateway.Request = httptest.NewRequest("POST", "/gateway/test/chat/completions", nil).WithContext(ctx)
			gatewayRecord := &usageRecord{UpstreamHTTPStatus: record.UpstreamHTTPStatus}
			s.failGateway(gateway, gatewayRecord, 502, err)
			wantKind := ErrorKindUpstreamIdleTimeout
			if scenario == "cancel" {
				wantKind = ErrorKindClientCanceled
			}
			if scenario == "http_failure" {
				wantKind = "invalid_request"
			}
			if scenario == "provider_failure" {
				wantKind = "rate_limit"
			}
			for _, observed := range []*usageRecord{record, gatewayRecord} {
				if observed.ErrorKind != wantKind {
					t.Fatalf("primary changed: %s, %v", observed.ErrorKind, err)
				}
				if scenario == "cancel" {
					if observed.TransportFailure != nil || observed.StatusCode != 499 {
						t.Fatal(observed)
					}
					continue
				}
				if scenario == "provider_failure" {
					if !strings.Contains(observed.Error, "synthetic provider failure") || observed.TransportFailure != nil {
						t.Fatal("provider failure should stop collection immediately", observed.Error)
					}
					continue
				}
				wantPhase := relay.StreamReadingBody
				if scenario == "awaiting_response" {
					wantPhase = relay.StreamAwaitingResponse
				}
				if observed.TransportFailure == nil || observed.TransportFailure.Phase != wantPhase {
					t.Fatal(observed.TransportFailure, err)
				}
				if scenario == "http_failure" && observed.StatusCode != 400 {
					t.Fatal("lost HTTP status", observed.StatusCode)
				}
				if scenario == "provider_failure" && !strings.Contains(observed.Error, "synthetic provider failure") {
					t.Fatal("lost provider cause", observed.Error)
				}
				encoded, encodeErr := json.Marshal(observed)
				if encodeErr != nil || !strings.Contains(string(encoded), `"transportFailure":{"kind":"upstream_idle_timeout","phase":"`+string(wantPhase)+`"}`) {
					t.Fatal(string(encoded), encodeErr)
				}
			}
			if scenario != "cancel" && scenario != "provider_failure" {
				var idle *relay.StreamIdleError
				if !errors.As(err, &idle) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestFailureEvidenceKeepsProviderBeforeTimeout(t *testing.T) {
	payload, _ := p.ParseValue([]byte(`{"category":"rate_limit","message":"synthetic original cause"}`))
	failure := &p.GenerationFailure{Payload: payload, Status: p.StringValue("failed")}
	err := attachFailureDiagnostics(failure, &relay.StreamIdleError{Phase: relay.StreamReadingBody})
	record := &usageRecord{StatusCode: 200, UpstreamHTTPStatus: 200}
	setUsageError(record, t.Context(), err)
	if record.ErrorKind != "rate_limit" || !strings.Contains(record.Error, "synthetic original cause") || record.TransportFailure == nil {
		t.Fatal(record)
	}
}
