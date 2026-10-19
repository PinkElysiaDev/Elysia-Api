package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

func TestGatewayMediaToolResultCandidateIsolation(t *testing.T) {
	t.Parallel()
	const raw = `{"model":"grp","max_tokens":100,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"f","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jT9sAAAAASUVORK5CYII="}}]}]}]}`
	for _, mode := range []string{"compatible", "strict"} {
		for _, order := range []string{"native", "gemini-first", "anthropic-first", "unsupported", "invalid", "reject"} {
			t.Run(mode+"/"+order, func(t *testing.T) {
				calls := 0
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					body, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(body), `"type":"image"`) {
						t.Error("media lost", string(body))
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"r","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
				}))
				defer provider.Close()
				groups := presetGroup(t, "custom:anthropic-messages", provider.URL)
				native := groups[0].Models[0]
				native.ID = "claude-sonnet-4-6"
				native.Name = native.ID
				gemini := native
				gemini.ID = "gemini-2.5-flash"
				gemini.Name = gemini.ID
				gemini.Platform = "custom:google-generate-content"
				switch order {
				case "native":
					groups[0].Models = []config.ModelRef{native}
				case "anthropic-first":
					groups[0].Models = []config.ModelRef{native, gemini}
				case "unsupported":
					groups[0].Models = []config.ModelRef{gemini}
				default:
					groups[0].Models = []config.ModelRef{gemini, native}
				}
				s := newTestServer(t, groups)
				service, _ := s.protocolService()
				ingress, _ := service.Pin(protocol.PresetAnthropicID)
				policy := protocol.ConversionPolicy{SchemaVersion: 1, ID: "media-policy", Mode: mode, Rules: []protocol.ConversionRule{}}
				if order == "reject" {
					present := true
					policy.Rules = append(policy.Rules, protocol.ConversionRule{ID: "operator-reject", Order: 1, Enabled: true, Phase: protocol.ConversionRequest, Action: "reject", Match: protocol.ConversionMatch{Path: "/parameters/anthropic_metadata/user_id", Present: &present}, Reason: "operator policy"})
				}
				bindings, err := s.store.ListProtocolBindings(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for i := range bindings {
					bindings[i].Conversion = &protocol.ConversionSelection{Overrides: &policy}
				}
				bindings, err = s.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
				if err != nil {
					t.Fatal(err)
				}
				for _, binding := range bindings {
					if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
						t.Fatal(err)
					}
				}
				s.invalidateRouteCache()
				input := raw
				if order == "reject" {
					input = strings.Replace(raw, `"max_tokens":100`, `"max_tokens":100,"metadata":{"user_id":"reject-media"}`, 1)
				}
				if order == "invalid" {
					input = strings.Replace(raw, `"tool_use_id":"call_1"`, `"tool_use_id":"call_1","is_error":"bad"`, 1)
				}
				ctx, rec := adminProtocolContext("POST", "/v1/messages", input)
				s.serveProtocolRequest(ctx, service.View(), ingress, "/v1/messages", []byte(input))
				rejected := order == "unsupported" || order == "invalid" || order == "reject"
				if rejected {
					if rec.Code != 400 || calls != 0 {
						t.Fatal(rec.Code, calls, rec.Body)
					}
				} else if rec.Code != 200 || calls != 1 {
					t.Fatal(rec.Code, calls, rec.Body)
				}
				if order == "unsupported" && !strings.Contains(rec.Body.String(), "plain text blocks") {
					t.Fatal("missing target diagnostic", rec.Body)
				}
				if order == "reject" && !strings.Contains(rec.Body.String(), "operator policy") {
					t.Fatal("explicit rejection changed", rec.Body)
				}
			})
		}
	}
}
