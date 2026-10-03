package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

func declaredChatCacheDefinition(t *testing.T) protocol.Definition {
	definition := presetDefinition(t, "chat-completions-api")
	definition.ID = "User-Chat-Cache"
	definition.Capabilities[protocol.CacheBreakpointsCapability] = true
	for _, direction := range []protocol.Direction{protocol.DecodeRequest, protocol.EncodeRequest} {
		mapping := definition.Directions[direction]
		mapping.Capabilities[protocol.CacheBreakpointsCapability] = true
		definition.Directions[direction] = mapping
	}
	wire := mustProtocolValue(t, `{"model":"m","max_completion_tokens":64,"messages":[{"role":"system","content":[{"type":"text","text":"stable system","cache_control":{"type":"ephemeral","ttl":"1h"}}]},{"role":"user","content":"question"}]}`)
	semantic := mustProtocolValue(t, `{"schemaVersion":1,"model":"m","parameters":{"max_output_tokens":64},"content":[{"kind":"message","role":"system","children":[{"kind":"text","payload":"stable system","cache":[{"kind":"breakpoint","location":"block","value":{"type":"ephemeral"},"ttl":"1h"}]}]},{"kind":"message","role":"user","children":[{"kind":"text","payload":"question"}]}]}`)
	definition.Samples = append(definition.Samples,
		protocol.Sample{ID: "declared-cache.decode", Direction: protocol.DecodeRequest, Input: wire, Expected: semantic, Capabilities: []protocol.Capability{protocol.CacheBreakpointsCapability}},
		protocol.Sample{ID: "declared-cache.encode", Direction: protocol.EncodeRequest, Input: semantic, Expected: wire, Capabilities: []protocol.Capability{protocol.CacheBreakpointsCapability}})
	return definition
}

func TestDeclaredChatCacheExtensionReachesAnthropic(t *testing.T) {
	definition := declaredChatCacheDefinition(t)
	wire := definition.Samples[len(definition.Samples)-2].Input
	var captured [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		captured = append(captured, body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"cache_read_input_tokens":70,"output_tokens":2}}`)
	}))
	defer upstream.Close()
	server := newTestServer(t, presetGroup(t, "custom:anthropic-api", upstream.URL), definition)
	for attempt := 0; attempt < 2; attempt++ {
		request, recorder := chatRequestContext(strings.ReplaceAll(string(wire.Bytes()), `"model":"m"`, `"model":"grp"`))
		request.Request.URL.Path = "/gateway/" + definition.ID + "/chat/completions"
		request.Params = gin.Params{{Key: "protocolId", Value: definition.ID}, {Key: "path", Value: "/chat/completions"}}
		server.gatewayProtocol(request)
		if recorder.Code != http.StatusOK {
			t.Fatal(recorder.Code, recorder.Body)
		}
	}
	if len(captured) != 2 || string(captured[0]) != string(captured[1]) {
		t.Fatal("repeated request changed the cached prefix")
	}
	var body struct {
		System []struct {
			Text         string `json:"text"`
			CacheControl struct {
				Type string `json:"type"`
				TTL  string `json:"ttl"`
			} `json:"cache_control"`
		} `json:"system"`
	}
	if err := json.Unmarshal(captured[0], &body); err != nil {
		t.Fatal(err)
	}
	if len(body.System) != 1 || body.System[0].Text != "stable system" || body.System[0].CacheControl.Type != "ephemeral" || body.System[0].CacheControl.TTL != "1h" {
		t.Fatalf("cache intent not rendered at the system block: %s", captured[0])
	}
}

func cacheEnvelopeDefinition(t *testing.T) protocol.Definition {
	t.Helper()
	definition := agentEnvelopeDefinition(t)
	definition.ID, definition.Family = "cache-envelope", "cache-envelope"
	definition.Agent = nil
	definition.Capabilities[protocol.CacheBreakpointsCapability] = true
	definition.Capabilities[protocol.UsageCapability] = true
	request := mustProtocolValue(t, `{"schemaVersion":1,"model":"m","content":[{"kind":"message","role":"system","children":[{"kind":"text","payload":"stable system","cache":[{"kind":"breakpoint","location":"block","value":{"type":"ephemeral"},"ttl":"1h"}]}]},{"kind":"message","role":"user","children":[{"kind":"text","payload":"question"}]},{"kind":"tool_call","name":"lookup","callId":"c","input":{"kind":"json","value":{"n":9007199254740993}}},{"kind":"tool_result","callId":"c","payload":"result"}],"tools":[{"kind":"function","name":"lookup","inputSchema":{"type":"object"},"cache":[{"kind":"breakpoint","location":"tool","value":{"type":"ephemeral"}}]}],"cache":[{"kind":"breakpoint","location":"request","value":{"type":"ephemeral"}}]}`)
	response := mustProtocolValue(t, `{"schemaVersion":1,"id":"r","status":"completed","attributes":{"finishReason":"tool_calls"},"content":[{"kind":"message","role":"assistant","children":[{"kind":"text","payload":"ok"},{"kind":"tool_call","name":"lookup","callId":"next","input":{"kind":"json","value":{"n":1}}}]}],"usage":{"input":{"count":100,"origin":"observed"},"output":{"count":5,"origin":"observed"},"total":{"count":105,"origin":"observed"},"cacheRead":{"count":70,"origin":"observed"},"cacheCreation":{"count":20,"origin":"observed"}}}`)
	definition.Samples = nil
	for _, pair := range []struct {
		decode, encode protocol.Direction
		semantic       protocol.Value
	}{
		{protocol.DecodeRequest, protocol.EncodeRequest, request},
		{protocol.DecodeResponse, protocol.EncodeResponse, response},
	} {
		mapping := definition.Directions[pair.encode]
		fields := mapping.Transform.Fields["payload"]
		fields.Fields["attributes"] = protocol.Expression{Op: "read", From: "input", Path: "/attributes"}
		if pair.encode == protocol.EncodeRequest {
			fields.Fields["cache"] = protocol.Expression{Op: "read", From: "input", Path: "/cache"}
		}
		mapping.Transform.Fields["payload"] = fields
		definition.Directions[pair.encode] = mapping
		wire := mustEncodedProtocolValue(t, protocol.Object{"payload": pair.semantic})
		definition.Samples = append(definition.Samples,
			protocol.Sample{ID: string(pair.decode), Direction: pair.decode, Input: wire, Expected: pair.semantic},
			protocol.Sample{ID: string(pair.encode), Direction: pair.encode, Input: pair.semantic, Expected: wire})
	}
	return definition
}

func TestCacheNewCustomProtocolNestedHTTP(t *testing.T) {
	definition := cacheEnvelopeDefinition(t)
	var captured []byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(definition.Samples[2].Input.Bytes())
	}))
	defer provider.Close()
	groups := presetGroup(t, "custom:"+definition.ID, provider.URL)
	groups[0].Models[0].VisionCapable = false
	s := newTestServerWithStore(t, groups, definition)
	service, _ := s.protocolService()
	ingress, _ := service.Pin("anthropic-api")
	upstream, _ := service.Pin(definition.ID)
	request := cacheWireFixtures()[1].request
	c, rec := messagesRequestContext(request)
	s.chatCompletions(c)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body)
	}
	if !strings.Contains(string(captured), `"payload":`) || !strings.Contains(string(captured), `"ttl":"1h"`) || !strings.Contains(string(captured), `"location":"tool"`) {
		t.Fatal("nested cache intent lost", string(captured))
	}
	semantic, err := ingress.DecodeRequest(t.Context(), []byte(request), protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	semantic.Model = protocol.StringValue("preset-model")
	preview, err := upstream.EncodeRequest(t.Context(), semantic, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := upstream.DecodeRequest(t.Context(), captured, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := upstream.DecodeRequest(t.Context(), preview, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	actualCache, _ := json.Marshal(actual.Cache)
	wantCache, _ := json.Marshal(want.Cache)
	if string(actualCache) != string(wantCache) || len(actual.Tools) != 1 || len(actual.Tools[0].Cache) != 1 {
		t.Fatal("preview and HTTP cache differ", string(captured))
	}
	rows := latestUsageRecords(t, s)
	if len(rows) != 1 || rows[0].CacheHitTokens != 70 || rows[0].TotalTokens != 105 {
		t.Fatal("custom usage lost", rows)
	}
	stored := storedRecordJSON(t, s.store, rows[0].RequestID)
	if !strings.Contains(stored, `"cacheCreationInputTokens":20`) {
		t.Fatal("creation counter lost", stored)
	}
	// The author may omit cache only after removing that capability and its
	// witnesses. Leaving the promise in place must fail offline verification.
	mapping := definition.Directions[protocol.EncodeRequest]
	payload := mapping.Transform.Fields["payload"]
	delete(payload.Fields, "cache")
	mapping.Transform.Fields["payload"] = payload
	definition.Directions[protocol.EncodeRequest] = mapping
	raw, _ := json.Marshal(definition)
	compiled, issues := service.Validate(raw)
	if compiled == nil {
		t.Fatal(issues)
	}
	if report := protocol.Verify(t.Context(), compiled); report.Passed {
		t.Fatal("omitted promised cache accepted")
	}
}
