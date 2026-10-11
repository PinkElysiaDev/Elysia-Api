package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayToolStrictPreview(t *testing.T) {
	cases := []struct {
		name, source, target, path, raw string
		loss                            bool
	}{
		{"chat-default", protocol.PresetChatCompletionsID, protocol.PresetResponsesID, "/chat/completions", `{"model":"grp","stream":false,"store":false,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}]}`, false},
		{"anthropic-strict", protocol.PresetAnthropicID, protocol.PresetChatCompletionsID, "/v1/messages", `{"model":"grp","stream":false,"max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"f","input_schema":{"type":"object"},"strict":true}]}`, false},
		{"responses-default", protocol.PresetResponsesID, protocol.PresetChatCompletionsID, "/responses", `{"model":"grp","stream":false,"store":false,"input":"hi","tools":[{"type":"function","name":"f","parameters":{"type":"object"}}]}`, true},
	}
	for _, tc := range cases {
		// Positive strict conversions are covered by the codec matrix; these fixture response pairs are lossy.
		modes := []string{"compatible"}
		if tc.loss {
			modes = append(modes, "strict")
		}
		for _, mode := range modes {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				calls := 0
				var sent []byte
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					sent, _ = io.ReadAll(r.Body)
					w.Header().Set("Content-Type", "application/json")
					// Stop after capturing the request; response projection is independent.
					w.WriteHeader(http.StatusBadGateway)
					fmt.Fprint(w, `{"error":{"message":"request captured"}}`)
				}))
				defer provider.Close()
				s := newTestServer(t, presetGroup(t, "custom:"+tc.target, provider.URL))
				svc, _ := s.protocolService()
				source, _ := svc.Pin(tc.source)
				target, _ := svc.Pin(tc.target)
				policy := protocol.ConversionPolicy{SchemaVersion: 1, ID: "tool-mode", Mode: mode, Rules: []protocol.ConversionRule{}}
				bindings, err := s.store.ListProtocolBindings(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for i := range bindings {
					bindings[i].Conversion = &protocol.ConversionSelection{Overrides: &policy}
				}
				bindings, err = s.verifyConversionBindings(t.Context(), svc.View(), nil, bindings)
				if err != nil {
					t.Fatal(err)
				}
				for _, b := range bindings {
					if err := s.store.SaveProtocolBinding(t.Context(), b); err != nil {
						t.Fatal(err)
					}
				}
				s.invalidateRouteCache()
				req, err := source.DecodeRequest(t.Context(), []byte(tc.raw), protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				body, _ := json.Marshal(map[string]any{"policy": policy, "phase": "request", "context": protocol.ConversionContext{Source: source.Identity(), Target: target.Identity(), Model: "preset-model"}, "input": req})
				ctx, preview := adminProtocolContext("POST", "/api/admin/protocols/conversion-policies/preview", string(body))
				s.adminConversionPreview(ctx)
				ctx, rec := adminProtocolContext("POST", tc.path, tc.raw)
				s.serveProtocolRequest(ctx, svc.View(), source, tc.path, []byte(tc.raw))
				if mode == "strict" && tc.loss {
					if preview.Code != 400 || rec.Code != 400 || calls != 0 {
						t.Fatal(preview.Code, rec.Code, calls, rec.Body)
					}
					return
				}
				if preview.Code != 200 || rec.Code != http.StatusBadGateway || calls != 1 {
					t.Fatal(preview.Code, rec.Code, calls, preview.Body, rec.Body)
				}
				var result struct {
					Data struct {
						Output           protocol.Request
						PersistentWrites bool
						Issues           []protocol.ConversionIssue
					}
				}
				if err := json.Unmarshal(preview.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Data.PersistentWrites {
					t.Fatal("preview wrote persistent data")
				}
				result.Data.Output.Model = protocol.StringValue("preset-model")
				predicted, err := target.EncodeRequest(t.Context(), &result.Data.Output, protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				var a, b any
				if json.Unmarshal(predicted, &a) != nil || json.Unmarshal(sent, &b) != nil || !reflect.DeepEqual(a, b) {
					t.Fatalf("preview differs from outgoing request: %s / %s", predicted, sent)
				}
				if tc.name == "chat-default" && string(result.Data.Output.Tools[0].Options["strict"].Bytes()) != "false" {
					t.Fatal("non-strict default lost")
				}
				if tc.name == "anthropic-strict" && string(result.Data.Output.Tools[0].Options["strict"].Bytes()) != "true" {
					t.Fatal("strict guarantee lost")
				}
			})
		}
	}
}
