package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

func auditOctober14Fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("../protocol/builtin/testdata/audit_20261014.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]json.RawMessage
	if err := sonic.ConfigStd.Unmarshal(body, &fixtures); err != nil {
		t.Fatal(err)
	}
	if fixtures[name] == nil {
		t.Fatal("missing fixture", name)
	}
	return fixtures[name]
}

func TestCollectedChatModelRepairAndPreview(t *testing.T) {
	t.Parallel()
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetChatCompletionsID)
	raw := auditOctober14Fixture(t, "chat_missing_model")
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	defer provider.Close()
	var operation protocol.Operation
	for _, entry := range upstream.Operations() {
		if entry.Kind == "generate" && entry.Transport == protocol.HTTPJSON {
			operation = entry
		}
	}
	outbound := []byte(`{"model":"sent-model","messages":[{"role":"user","content":"synthetic"}]}`)
	request, err := upstream.DecodeRequest(t.Context(), outbound, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"compatible", "strict", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			policy := protocol.DefaultConversionPolicy(upstream, upstream)
			if mode == "strict" {
				policy.Mode = mode
			}
			for index := range policy.Rules {
				if mode == "disabled" && policy.Rules[index].Action == "chat_response_model" {
					policy.Rules[index].Enabled = false
				}
			}
			conversion, err := protocol.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			candidate := gatewayCandidate{compiled: upstream, conversion: conversion, operation: operation, model: config.ModelRef{ID: "configured-model", Name: "configured-model", BaseURL: provider.URL}, binding: protocol.Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: upstream.Definition().Capabilities, Transports: []protocol.Transport{protocol.HTTPJSON}}}
			record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: 1 << 20}}
			before := calls
			result, collectErr := s.collectProtocolGeneration(t.Context(), candidate, request, record, nil)
			if calls != before+1 {
				t.Fatal("generation repeated", before, calls)
			}
			if mode == "strict" {
				if collectErr == nil {
					t.Fatal("collector ignored strict mode")
				}
			} else {
				if collectErr != nil {
					t.Fatal(collectErr)
				}
				if mode == "compatible" && !result.Model.IsString("sent-model") {
					t.Fatal("collector guessed configured model", result.Model)
				}
				if mode == "disabled" && !result.Model.IsZero() {
					t.Fatal("collector ignored disabled rule")
				}
			}
			response, err := upstream.DecodeResponse(t.Context(), raw, protocol.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			route, err := candidate.conversionContext(upstream, true).WithResponseModelOrigin(upstream, upstream, outbound, raw)
			if err != nil {
				t.Fatal(err)
			}
			previewBody, err := sonic.ConfigStd.Marshal(map[string]any{"policy": policy, "phase": "response", "context": route, "input": response})
			if err != nil {
				t.Fatal(err)
			}
			ctx, preview := adminProtocolContext("POST", "/api/admin/protocols/conversion-policies/preview", string(previewBody))
			s.adminConversionPreview(ctx)
			if mode == "strict" {
				if preview.Code != 400 {
					t.Fatal(preview.Code, preview.Body.String())
				}
				return
			}
			if preview.Code != 200 {
				t.Fatal(preview.Code, preview.Body.String())
			}
			var resultBody struct {
				Data struct {
					Output           protocol.Response
					Issues           []protocol.ConversionIssue
					PersistentWrites bool
				}
			}
			if err := sonic.ConfigStd.Unmarshal(preview.Body.Bytes(), &resultBody); err != nil {
				t.Fatal(err)
			}
			if resultBody.Data.PersistentWrites || resultBody.Data.Output.Model != result.Model {
				t.Fatal(resultBody)
			}
			if len(resultBody.Data.Issues) != len(record.ConversionIssues) {
				t.Fatal(resultBody.Data.Issues, record.ConversionIssues)
			}
		})
	}
}

func TestGatewayChatMissingResponseModel(t *testing.T) {
	t.Parallel()
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetChatCompletionsID)
	raw := auditOctober14Fixture(t, "chat_missing_model")
	var outgoingModel string
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Model string `json:"model"`
		}
		if err := sonic.ConfigStd.Unmarshal(body, &request); err != nil {
			t.Error(err)
		}
		outgoingModel = request.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	req := httptest.NewRequest("POST", "/gateway/openai-chat-completions/chat/completions", strings.NewReader(`{"model":"group","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Authorization", "Bearer gateway-test-token")
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("missing model should be compatible: %d %s", rec.Code, rec.Body.String())
	}
	if calls != 1 || outgoingModel == "" || outgoingModel == "group" {
		t.Fatal("wrong outbound model or duplicate generation", calls, outgoingModel)
	}
	var reply map[string]json.RawMessage
	if err := sonic.ConfigStd.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if string(reply["model"]) != `"group"` || string(reply["vendor_integer"]) != "9007199254740993" {
		t.Fatal(rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "synthetic reply") {
		t.Fatal(rec.Body.String())
	}
	records := latestUsageRecords(t, s)
	if len(records) != 1 {
		t.Fatal(records)
	}
	persisted, _, err := s.store.GetUsageRecordJSON(t.Context(), records[0].RequestID)
	if err != nil {
		t.Fatal(err)
	}
	var record usageRecord
	if err := sonic.ConfigStd.Unmarshal(persisted, &record); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(record.ProviderResponse), `"model"`) {
		t.Fatal("original provider evidence was replaced")
	}
	if record.ProtocolUsage == nil || record.ProtocolUsage.Input.Count != 7 || record.ProtocolUsage.Output.Count != 3 {
		t.Fatal(record.ProtocolUsage)
	}
	var found bool
	for _, issue := range record.ConversionIssues {
		if issue.RuleID == "chat-response-model" {
			found = issue.Path == "/model" && issue.Fidelity == "lossy_compatible" && issue.PolicyHash != "" && issue.Direction == protocol.DecodeResponse
		}
	}
	if !found {
		t.Fatal("missing repair provenance", record.ConversionIssues)
	}
}

func TestGatewayChatInvalidResponseModelIsNotAliased(t *testing.T) {
	t.Parallel()
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetChatCompletionsID)
	var raw []byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, model := range []string{"null", `""`, "42", `{}`} {
		t.Run(model, func(t *testing.T) {
			fixture, _ := protocol.ParseValue(auditOctober14Fixture(t, "chat_missing_model"))
			value, _ := protocol.ParseValue([]byte(model))
			fixture, err := protocol.ApplyMutations(fixture, []protocol.Mutation{{Op: protocol.SetValue, Path: "/model", Value: value}}, protocol.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			raw = fixture.Bytes()
			req := httptest.NewRequest("POST", "/gateway/openai-chat-completions/chat/completions", strings.NewReader(`{"model":"group","messages":[{"role":"user","content":"hello"}]}`))
			req.Header.Set("Authorization", "Bearer gateway-test-token")
			rec := httptest.NewRecorder()
			s.engine.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("invalid provider model was hidden: %d %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "upstream_contract_violation") || !strings.Contains(rec.Body.String(), "decode_response") {
				t.Fatal("source model failure lost its attribution", rec.Body.String())
			}
		})
	}
}
