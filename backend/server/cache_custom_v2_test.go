package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/hertz/pkg/route/param"
	"github.com/elysia-api/backend/protocol"
)

func declaredChatCacheDefinition(t *testing.T) protocol.ProtocolPackage {
	definition := presetPackage(t, "openai-chat-completions")
	definition.Spec.ID = "User-Chat-Cache"
	definition.Mapping.Capabilities[protocol.CacheBreakpointsCapability] = true
	for _, direction := range []protocol.Direction{protocol.DecodeRequest, protocol.EncodeRequest} {
		mapping := definition.Mapping.Directions[direction]
		mapping.Capabilities[protocol.CacheBreakpointsCapability] = true
		definition.Mapping.Directions[direction] = mapping
	}
	encode := definition.Mapping.Directions[protocol.EncodeRequest]
	for index := range encode.Effects {
		effect := &encode.Effects[index]
		if effect.ID == "cache-unsupported-hint" {
			effect.When = &protocol.Expression{Op: "all", Items: []protocol.Expression{*effect.When,
				{Op: "not", Source: &protocol.Expression{Op: "equal", Items: []protocol.Expression{{Op: "read", Path: "/kind"}, {Op: "literal", Value: protocol.StringValue("breakpoint")}}}},
			}}
		}
	}
	definition.Mapping.Directions[protocol.EncodeRequest] = encode
	block := definition.Mapping.Expressions["encode-block"]
	text := block.Items[0].Cases["text"]
	text.Fields["cache_control"] = protocol.Expression{Op: "if", When: &protocol.Expression{Op: "exists", Source: &protocol.Expression{Op: "read", From: "item", Path: "/cache/0"}},
		Then: &protocol.Expression{Op: "merge", Policy: "reject", Items: []protocol.Expression{
			{Op: "read", From: "item", Path: "/cache/0/value"},
			{Op: "object", Fields: map[string]protocol.Expression{"ttl": {Op: "read", From: "item", Path: "/cache/0/ttl"}}},
		}}}
	block.Items[0].Cases["text"] = text
	definition.Mapping.Expressions["encode-block"] = block
	wire := mustProtocolValue(t, `{"model":"m","max_completion_tokens":64,"messages":[{"role":"system","content":[{"type":"text","text":"stable system","cache_control":{"type":"ephemeral","ttl":"1h"}}]},{"role":"user","content":"question"}]}`)
	semantic := mustProtocolValue(t, `{"schemaVersion":2,"model":"m","parameters":{"max_output_tokens":64},"clientOutput":{"responsesStorage":{"sourceCodec":"openai-chat","requested":false,"effective":false,"effectiveUnknown":true}},"content":[{"kind":"message","role":"system","children":[{"kind":"text","payload":"stable system","cache":[{"kind":"breakpoint","location":"block","value":{"type":"ephemeral"},"ttl":"1h"}]}]},{"kind":"message","role":"user","children":[{"kind":"text","payload":"question"}]}]}`)
	definition.Mapping.Samples = append(definition.Mapping.Samples,
		protocol.Sample{ID: "declared-cache.decode", Direction: protocol.DecodeRequest, Input: wire, Expected: semantic, Capabilities: []protocol.Capability{protocol.CacheBreakpointsCapability}},
		protocol.Sample{ID: "declared-cache.encode", Direction: protocol.EncodeRequest, Input: semantic, Expected: wire, Capabilities: []protocol.Capability{protocol.CacheBreakpointsCapability}})
	return definition
}

func TestDeclaredChatCacheExtensionReachesAnthropic(t *testing.T) {
	definition := declaredChatCacheDefinition(t)
	wire := definition.Mapping.Samples[len(definition.Mapping.Samples)-2].Input
	var captured [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		captured = append(captured, body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"cache-response","type":"message","model":"preset-model","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"cache_read_input_tokens":70,"output_tokens":2}}`)
	}))
	defer upstream.Close()
	server := newTestServer(t, presetGroup(t, "custom:anthropic-messages", upstream.URL), definition)
	for attempt := 0; attempt < 2; attempt++ {
		request, recorder := chatRequestContext(strings.ReplaceAll(string(wire.Bytes()), `"model":"m"`, `"model":"grp"`))
		request.Request.URI().SetPath("/gateway/" + definition.Spec.ID + "/v1/chat/completions")
		request.Params = param.Params{{Key: "protocolId", Value: definition.Spec.ID}, {Key: "path", Value: "/v1/chat/completions"}}
		server.gatewayProtocol(contextForTest(request), request)
		flushTestResponse(request)
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
	if err := sonic.ConfigStd.Unmarshal(captured[0], &body); err != nil {
		t.Fatal(err)
	}
	if len(body.System) != 1 || body.System[0].Text != "stable system" || body.System[0].CacheControl.Type != "ephemeral" || body.System[0].CacheControl.TTL != "1h" {
		t.Fatalf("cache intent not rendered at the system block: %s", captured[0])
	}
}

func cacheEnvelopeDefinition(t *testing.T) protocol.ProtocolPackage {
	t.Helper()
	definition := agentEnvelopePackage(t)
	definition.Spec.ID, definition.Spec.Family = "cache-envelope", "cache-envelope"
	definition.Mapping.Agent = nil
	definition.Mapping.Capabilities[protocol.CacheBreakpointsCapability] = true
	definition.Mapping.Capabilities[protocol.UsageCapability] = true
	request := mustProtocolValue(t, `{"schemaVersion":2,"model":"m","content":[{"kind":"message","role":"system","children":[{"kind":"text","payload":"stable system","cache":[{"kind":"breakpoint","location":"block","value":{"type":"ephemeral"},"ttl":"1h"}]}]},{"kind":"message","role":"user","children":[{"kind":"text","payload":"question"}]},{"kind":"tool_call","name":"lookup","callId":"c","input":{"kind":"json","value":{"n":9007199254740993}}},{"kind":"tool_result","callId":"c","payload":"result"}],"tools":[{"kind":"function","name":"lookup","inputSchema":{"type":"object"},"cache":[{"kind":"breakpoint","location":"tool","value":{"type":"ephemeral"}}]}],"cache":[{"kind":"breakpoint","location":"request","value":{"type":"ephemeral"}}]}`)
	response := mustProtocolValue(t, `{"schemaVersion":2,"id":"r","model":"preset-model","status":"completed","attributes":{"finishReason":"tool_calls"},"content":[{"kind":"message","role":"assistant","children":[{"kind":"text","payload":"ok"},{"kind":"tool_call","name":"lookup","callId":"next","input":{"kind":"json","value":{"n":1}}}]}],"usage":{"input":{"count":100,"origin":"observed"},"output":{"count":5,"origin":"observed"},"total":{"count":105,"origin":"observed"},"cacheRead":{"count":70,"origin":"observed"},"cacheCreation":{"count":20,"origin":"observed"}}}`)
	definition.Mapping.Samples = nil
	for _, pair := range []struct {
		decode, encode protocol.Direction
		semantic       protocol.Value
	}{
		{protocol.DecodeRequest, protocol.EncodeRequest, request},
		{protocol.DecodeResponse, protocol.EncodeResponse, response},
	} {
		mapping := definition.Mapping.Directions[pair.encode]
		fields := mapping.Program.Fields["payload"]
		fields.Fields["content"] = envelopeWireNodes("/content")
		if pair.encode == protocol.EncodeRequest {
			fields.Fields["tools"] = envelopeWireTools()
		}
		fields.Fields["attributes"] = protocol.Expression{Op: "read", From: "input", Path: "/attributes"}
		fields.Fields["model"] = protocol.Expression{Op: "read", From: "input", Path: "/model"}
		if pair.encode == protocol.EncodeRequest {
			fields.Fields["cache"] = envelopeWireCache("input")
		}
		mapping.Program.Fields["payload"] = fields
		identity := "id"
		if pair.encode == protocol.EncodeRequest {
			identity = "model"
		}
		mapping.Effects = []protocol.ProtocolSemanticEffect{{ID: identity, Kind: "emit", Path: "/" + identity, Target: "/payload/" + identity,
			When: &protocol.Expression{Op: "exists", Source: &protocol.Expression{Op: "read", Path: "/" + identity}}}}
		definition.Mapping.Directions[pair.encode] = mapping
		wire := mustEncodedProtocolValue(t, protocol.Object{"payload": pair.semantic})
		definition.Mapping.Samples = append(definition.Mapping.Samples,
			protocol.Sample{ID: string(pair.decode), Direction: pair.decode, Input: wire, Expected: pair.semantic},
			protocol.Sample{ID: string(pair.encode), Direction: pair.encode, Input: pair.semantic, Expected: wire})
	}
	return definition
}

func envelopeWireCache(from string) protocol.Expression {
	return optionalEnvelopeArray(protocol.Expression{Op: "map", Source: &protocol.Expression{Op: "read", From: from, Path: "/cache"},
		Body: &protocol.Expression{Op: "project", Policy: "exclude", Keys: []string{"source", "native"}, Source: &protocol.Expression{Op: "read", From: "item"}}})
}

func optionalEnvelopeArray(program protocol.Expression) protocol.Expression {
	return protocol.Expression{Op: "if", When: &protocol.Expression{Op: "exists", Source: program.Source}, Then: &program}
}

func envelopeWireNodes(path string) protocol.Expression {
	return optionalEnvelopeArray(protocol.Expression{Op: "walk", Source: &protocol.Expression{Op: "read", Path: path}, Body: &protocol.Expression{Op: "merge", Policy: "last", Items: []protocol.Expression{
		{Op: "project", Policy: "exclude", Keys: []string{"source", "native", "cache", "children"}, Source: &protocol.Expression{Op: "read", From: "item"}},
		{Op: "object", Fields: map[string]protocol.Expression{
			"children": optionalEnvelopeArray(protocol.Expression{Op: "recur", Source: &protocol.Expression{Op: "read", From: "item", Path: "/children"}}),
			"cache":    envelopeWireCache("item"),
		}},
	}}})
}

func envelopeWireTools() protocol.Expression {
	return optionalEnvelopeArray(protocol.Expression{Op: "map", Source: &protocol.Expression{Op: "read", Path: "/tools"}, Body: &protocol.Expression{Op: "merge", Policy: "last", Items: []protocol.Expression{
		{Op: "project", Policy: "exclude", Keys: []string{"source", "native", "cache"}, Source: &protocol.Expression{Op: "read", From: "item"}},
		{Op: "object", Fields: map[string]protocol.Expression{"cache": envelopeWireCache("item")}},
	}}})
}

// The author may omit cache only after removing that capability and its
// witnesses. Leaving the promise in place must fail offline verification.
func TestCacheNewCustomProtocolNestedHTTP(t *testing.T) {
	definition := cacheEnvelopeDefinition(t)
	var captured []byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(definition.Mapping.Samples[2].Input.Bytes())
	}))
	defer provider.Close()
	groups := presetGroup(t, "custom:"+definition.Spec.ID, provider.URL)
	groups[0].Models[0].VisionCapable = false
	s := newTestServerWithStore(t, groups, definition)
	service, _ := s.protocolService()
	ingress, _ := service.Pin("anthropic-messages")
	upstream, _ := service.Pin(definition.Spec.ID)
	request := cacheWireFixtures()[1].request
	c, rec := messagesRequestContext(request)
	s.chatCompletions(contextForTest(c), c)
	flushTestResponse(c)
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
	actualCache, _ := sonic.ConfigStd.Marshal(actual.Cache)
	wantCache, _ := sonic.ConfigStd.Marshal(want.Cache)
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
	mapping := definition.Mapping.Directions[protocol.EncodeRequest]
	payload := mapping.Program.Fields["payload"]
	delete(payload.Fields, "cache")
	mapping.Program.Fields["payload"] = payload
	definition.Mapping.Directions[protocol.EncodeRequest] = mapping
	raw, _ := sonic.ConfigStd.Marshal(definition)
	compiled, issues := service.Validate(raw)
	if compiled == nil {
		t.Fatal(issues)
	}
	if report := protocol.Verify(t.Context(), compiled); report.Passed {
		t.Fatal("omitted promised cache accepted")
	}
}
