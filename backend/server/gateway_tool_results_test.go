package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestGatewayMediaToolResultCandidateIsolation(t *testing.T) {
	t.Parallel()
	const raw = `{"model":"grp","max_tokens":100,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"f","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jT9sAAAAASUVORK5CYII="}}]}]}]}`
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"type":"image"`) {
			t.Error("media lost", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"r","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(provider.Close)
	groups := presetGroup(t, "custom:anthropic-messages", provider.URL)
	native := groups[0].Models[0]
	native.ID, native.Name, native.SourceID = "claude-sonnet-4-6", "claude-sonnet-4-6", "native"
	gemini := native
	gemini.ID, gemini.Name, gemini.SourceID = "gemini-2.5-flash", "gemini-2.5-flash", "gemini"
	gemini.Platform = "custom:google-generate-content"
	groups[0].Models = []config.ModelRef{gemini, native}
	s := newTestServer(t, groups)
	service, _ := s.protocolService()
	ingress, _ := service.Pin(protocol.PresetAnthropicID)
	// All cases share the immutable presets/store/provider. Candidate membership,
	// order, conversion evidence and call counters are reset for every request.
	for _, mode := range []string{"compatible", "strict"} {
		for _, order := range []string{"native", "gemini-first", "anthropic-first", "unsupported", "invalid", "reject"} {
			t.Run(mode+"/"+order, func(t *testing.T) {
				calls.Store(0)
				members := []string{"gemini:" + gemini.ID, "native:" + native.ID}
				switch order {
				case "native":
					members = []string{"native:" + native.ID}
				case "anthropic-first":
					members = []string{"native:" + native.ID, "gemini:" + gemini.ID}
				case "unsupported":
					members = []string{"gemini:" + gemini.ID}
				}
				if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "g1", Name: "grp", Enabled: true, ToolsCapable: true, VisionCapable: true, Models: members}); err != nil {
					t.Fatal(err)
				}
				policy := protocol.ConversionPolicy{SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "media-policy", Mode: mode, Rules: []protocol.ConversionRule{}}
				if order == "reject" {
					present := true
					policy.Rules = append(policy.Rules, protocol.ConversionRule{ID: "operator-reject", Order: 1, Enabled: true, Phase: protocol.ConversionRequest, Action: "reject", Match: protocol.ConversionMatch{Path: "/parameters/user", Present: &present}, Reason: "operator policy"})
				}
				bindings, err := s.store.ListProtocolBindings(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for i := range bindings {
					bindings[i].Conversion = &protocol.ConversionSelection{Overrides: &policy}
				}
				// Persist all selections before resolving any model/provider context.
				for _, binding := range bindings {
					if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
						t.Fatal(err)
					}
				}
				for _, binding := range bindings {
					binding.Combinations = verifyFixtureBinding(t, s, binding)
					if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
						t.Fatal(err)
					}
				}
				s.scheduler.ForgetGroup("g1") // Each case starts at its declared first candidate, as a fresh fixture did.
				s.invalidateRouteCache()
				input := raw
				if order == "reject" {
					input = strings.Replace(raw, `"max_tokens":100`, `"max_tokens":100,"metadata":{"user_id":"reject-media"}`, 1)
				}
				if order == "invalid" {
					input = strings.Replace(raw, `"tool_use_id":"call_1"`, `"tool_use_id":"call_1","is_error":"bad"`, 1)
				}
				ctx, rec := adminProtocolContext("POST", "/v1/messages", input)
				s.serveProtocolRequest(contextForTest(ctx), ctx, service.View(), ingress, "/v1/messages", []byte(input))
				flushTestResponse(ctx)
				rejected := order == "unsupported" || order == "invalid" || order == "reject"
				if rejected {
					if rec.Code != 400 || calls.Load() != 0 {
						t.Fatal(rec.Code, calls.Load(), rec.Body)
					}
				} else if rec.Code != 200 || calls.Load() != 1 {
					t.Fatal(rec.Code, calls.Load(), rec.Body)
				}
				if order == "unsupported" && !strings.Contains(rec.Body.String(), "conversion_rejected at /content/1/children/0/children:") {
					t.Fatal("missing target diagnostic", rec.Body)
				}
				if order == "reject" && !strings.Contains(rec.Body.String(), "operator policy") {
					t.Fatal("explicit rejection changed", rec.Body)
				}
			})
		}
	}
}
