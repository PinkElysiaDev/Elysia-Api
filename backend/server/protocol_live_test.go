package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

func liveRequest(t *testing.T, model string, isStream bool) *protocol.Request {
	return &protocol.Request{SchemaVersion: 1, Model: protocol.StringValue(model), Content: []protocol.Node{
		{Kind: protocol.MessageNode, Role: protocol.StringValue("system"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("API verification. Reply briefly; do not execute external tools.")}}},
		{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("Reply with OK.")}}},
	}, Parameters: protocol.Object{"max_output_tokens": mustProtocolValue(t, fmt.Sprint(liveOutputLimit)), "stream": mustProtocolValue(t, fmt.Sprint(isStream))}}
}

func (suite *liveSuite) direct(t *testing.T, id string, compiled *protocol.Compiled, request *protocol.Request, isStream bool) (liveCase, *protocol.Response) {
	t.Helper()
	result := liveCase{ID: id, Target: compiled.Identity().DefinitionID, Revision: compiled.Hash(), Stream: isStream, Status: "failed"}
	body, err := compiled.EncodeRequest(t.Context(), request, protocol.EvaluationContext{})
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.Wire = suite.exchange(t.Context(), compiled, body, isStream, nil)
	if result.Wire.Status != http.StatusOK {
		result.Status = "inconclusive"
		result.Reason = result.Wire.Error
		return result, nil
	}
	response, err := inspectLiveWire(compiled, &result)
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.UpstreamUsage = response.Usage
	result.Status = "passed"
	return result, response
}

type liveGateway struct {
	suite      *liveSuite
	server     *Server
	target     *protocol.Compiled
	completed  <-chan liveWireEvidence
	lastRecord string
}

func newLiveGateway(t *testing.T, suite *liveSuite, target *protocol.Compiled) *liveGateway {
	t.Helper()
	proxy, completed := suite.observer(target)
	t.Cleanup(proxy.Close)
	groups := presetGroup(t, "custom:"+target.Identity().DefinitionID, liveBase(proxy.URL, target))
	groups[0].MaxRetries = 1
	groups[0].Models[0].Name = suite.Model
	groups[0].Models[0].APIKey = "verification-observer-placeholder"
	server := newTestServerWithStore(t, groups, target.Definition(), verificationEnvelopeDefinition(t))
	server.protocolTransport.SetTimeout(liveRequestTimeout)
	return &liveGateway{suite: suite, server: server, target: target, completed: completed}
}

func (gateway *liveGateway) bind(t *testing.T, capabilities protocol.CapabilitySet) {
	t.Helper()
	service, err := gateway.server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := gateway.server.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		binding.Binding.Capabilities = capabilities
		binding.Combinations = verifyGatewayBinding(t.Context(), service.View(), gateway.target, capabilities)
		if err := gateway.server.store.SaveProtocolBinding(t.Context(), binding); err != nil {
			t.Fatal(err)
		}
	}
	gateway.server.invalidateRouteCache()
}

func (gateway *liveGateway) stored(t *testing.T) *usageRecord {
	t.Helper()
	deadline := time.Now().Add(liveUsageTimeout)
	for time.Now().Before(deadline) {
		_, items, err := gateway.server.store.QueryUsageLogs(t.Context(), storage.UsageQuery{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) > 0 && items[0].RequestID != gateway.lastRecord {
			gateway.lastRecord = items[0].RequestID
			raw, exists, err := gateway.server.store.GetUsageRecordJSON(t.Context(), gateway.lastRecord)
			if err != nil || !exists {
				t.Fatal("persisted request missing", err)
			}
			var saved usageRecord
			if err := json.Unmarshal(raw, &saved); err != nil {
				t.Fatal(err)
			}
			return &saved
		}
		time.Sleep(liveUsagePoll)
	}
	t.Fatal("usage writer did not settle live request")
	return nil
}

func (gateway *liveGateway) request(t *testing.T, id string, ingress *protocol.Compiled, request *protocol.Request, isStream bool) (liveCase, *protocol.Response) {
	t.Helper()
	result := liveCase{ID: id, Ingress: ingress.Identity().DefinitionID, Target: gateway.target.Identity().DefinitionID, Revision: gateway.target.Hash(), Stream: isStream, Status: "failed"}
	body, err := ingress.EncodeRequest(t.Context(), request, protocol.EvaluationContext{})
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	operation := liveOperation(ingress, isStream)
	path, err := protocol.ExpandOperationPath(operation.Path, map[string]string{"model": "grp"})
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	if ingress.Identity().DefinitionID == "chat-completions-api" || ingress.Identity().DefinitionID == "responses-api" {
		path = "/v1" + path
	}
	c, rec := adminProtocolContext("POST", path, string(body))
	if ingress.Identity().DefinitionID == "gemini-api" {
		c.Params = gin.Params{{Key: "action", Value: strings.TrimPrefix(path, "/v1beta/models/")}}
	}
	if ingress.Identity().DefinitionID == "responses-api" {
		gateway.server.responses(c)
	} else if ingress.Identity().DefinitionID == "chat-completions-api" || ingress.Identity().DefinitionID == "anthropic-api" || ingress.Identity().DefinitionID == "gemini-api" {
		gateway.server.chatCompletions(c)
	} else {
		service, _ := gateway.server.protocolService()
		gateway.server.serveProtocolRequest(c, service.View(), ingress, path, body)
	}
	result.DownstreamStatus = rec.Code
	result.DownstreamHash, result.DownstreamBytes = liveHash(rec.Body.Bytes()), rec.Body.Len()
	result.downstream = append([]byte(nil), rec.Body.Bytes()...)
	saved := gateway.stored(t)
	if saved.UpstreamRevision == "" {
		result.Reason = "gateway rejected before upstream"
	} else {
		select {
		case result.Wire = <-gateway.completed:
		case <-time.After(liveRequestTimeout):
			t.Fatal("observer did not finish; refusing overlapping paid requests")
		}
	}
	result.StoredUsage, result.StoredRequestID = saved.ProtocolUsage, saved.RequestID
	upstream, upstreamErr := inspectLiveWire(gateway.target, &result)
	if rec.Code != 200 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
		result.Reason = saved.Error
		if result.Reason == "" {
			result.Reason = rec.Body.String()
		}
		return result, nil
	}
	if upstreamErr != nil {
		result.Reason = "upstream decode: " + upstreamErr.Error()
		return result, nil
	}
	result.UpstreamUsage = upstream.Usage
	downstream, err := decodeLiveResponse(ingress, rec.Body.Bytes(), isStream)
	if err != nil {
		result.Reason = "downstream decode: " + err.Error()
		return result, nil
	}
	result.DownstreamUsage = downstream.Usage
	if err := compareLiveUsage(upstream.Usage, downstream.Usage); err != nil {
		result.Reason = "downstream " + err.Error()
		return result, nil
	}
	if err := compareLiveUsage(upstream.Usage, saved.ProtocolUsage); err != nil {
		result.Reason = "persistence " + err.Error()
		return result, nil
	}
	result.Status = "passed"
	return result, downstream
}

func compareLiveUsage(expected, actual *protocol.Usage) error {
	if expected == nil {
		if actual != nil {
			return fmt.Errorf("invented declared usage")
		}
		return nil
	}
	if actual == nil {
		return fmt.Errorf("usage missing")
	}
	for _, pair := range []struct {
		name          string
		first, second *protocol.Counter
	}{{"input", expected.Input, actual.Input}, {"output", expected.Output, actual.Output}, {"cacheRead", expected.CacheRead, actual.CacheRead}, {"cacheCreation", expected.CacheCreation, actual.CacheCreation}} {
		if (pair.first == nil) != (pair.second == nil) {
			return fmt.Errorf("%s presence changed", pair.name)
		}
		if pair.first != nil && pair.first.Count != pair.second.Count {
			return fmt.Errorf("%s changed: %d -> %d", pair.name, pair.first.Count, pair.second.Count)
		}
	}
	return nil
}

func liveCalls(nodes []protocol.Node) []protocol.Node {
	var calls []protocol.Node
	for _, node := range nodes {
		if node.Kind == protocol.ToolCallNode {
			calls = append(calls, node)
		}
		calls = append(calls, liveCalls(node.Children)...)
	}
	return calls
}

func TestProtocolLive(t *testing.T) {
	suite := openLiveSuite(t)
	if suite.Suite == "gemini-native-tools" {
		suite.geminiNativeToolControl(t, compileFixtureDefinition(t, presetDefinition(t, "gemini-api")))
		return
	}
	compiled := map[string]*protocol.Compiled{}
	available := map[string]map[bool]bool{}
	for _, id := range liveProtocolIDs {
		definition := presetDefinition(t, id)
		compiled[id] = compileFixtureDefinition(t, definition)
		if report := protocol.Verify(t.Context(), compiled[id]); !report.Passed {
			t.Fatal("preset offline evidence failed", id, report.Issues)
		}
		available[id] = map[bool]bool{}
		if filter := os.Getenv("ELYSIA_LIVE_TARGET"); filter != "" && id != filter {
			continue
		}
		if reuseLivePreflight(t, suite, compiled[id], available[id]) {
			continue
		}
		for _, isStream := range []bool{false, true} {
			result, _ := suite.direct(t, fmt.Sprintf("preflight/%s/%t", id, isStream), compiled[id], liveRequest(t, suite.Model, isStream), isStream)
			available[id][isStream] = result.Status == "passed"
			suite.record(t, result)
		}
	}
	if testing.Short() {
		return
	}
	for _, targetID := range liveProtocolIDs {
		if !available[targetID][false] {
			continue
		}
		t.Run(targetID, func(t *testing.T) {
			gateway := newLiveGateway(t, suite, compiled[targetID])
			if suite.Suite == "breakpoint" {
				definition := declaredChatCacheDefinition(t)
				service, err := gateway.server.protocolService()
				if err != nil {
					t.Fatal(err)
				}
				activateGatewayDefinition(t, gateway.server, definition)
				custom, exists := service.Pin(definition.ID)
				if !exists {
					t.Fatal("cache ingress missing")
				}
				gateway.bind(t, protocol.CapabilitySet{protocol.TextCapability: true, protocol.CacheBreakpointsCapability: true, protocol.UsageCapability: true})
				request := liveRequest(t, "grp", false)
				request.Content[0].Children[0].Payload = protocol.StringValue(fmt.Sprintf("Declared breakpoint %d.\n", suite.StartedAt.UnixNano()) + strings.Repeat("stable alpha beta gamma delta reference information.\n", 1024))
				request.Content[0].Children[0].Cache = []protocol.CacheIntent{{Kind: "breakpoint", Location: "block", Value: mustProtocolValue(t, `{"type":"ephemeral"}`), TTL: protocol.StringValue("1h")}}
				for repeat := range 3 {
					result, _ := gateway.request(t, fmt.Sprintf("cache/chat-breakpoint/%d", repeat), custom, request, false)
					suite.record(t, result)
				}
				return
			}
			if suite.Suite == "followup" || suite.Suite == "diagnostics" || suite.Suite == "custom" {
				gateway.bind(t, protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true})
				service, err := gateway.server.protocolService()
				if err != nil {
					t.Fatal(err)
				}
				custom, exists := service.Pin("verification-envelope")
				if !exists {
					t.Fatal("verified custom ingress missing")
				}
				for _, isStream := range []bool{false, true} {
					result, _ := gateway.request(t, fmt.Sprintf("custom/corrected/%t", isStream), custom, liveRequest(t, "grp", isStream), isStream)
					suite.record(t, result)
				}
				if targetID == "responses-api" && suite.Suite == "followup" {
					gateway.policyPairs(t, compiled[targetID])
				}
				if targetID == "gemini-api" && suite.Suite == "diagnostics" {
					gateway.cacheOrderControl(t, compiled[targetID])
				}
				return
			}
			if os.Getenv("ELYSIA_LIVE_SUITE") == "extended" {
				gateway.bind(t, gateway.target.Definition().Capabilities)
				for _, isStream := range []bool{false, true} {
					gateway.toolRoundTrip(t, fmt.Sprintf("tools-corrected/%t", isStream), compiled[targetID], isStream)
				}
				definition := compiled[targetID].Definition()
				definition.ID = "verification-copy-" + targetID
				copied := compileFixtureDefinition(t, definition)
				copyGateway := newLiveGateway(t, suite, copied)
				for _, isStream := range []bool{false, true} {
					result, _ := copyGateway.request(t, fmt.Sprintf("copy/text/%t", isStream), copied, liveRequest(t, "grp", isStream), isStream)
					suite.record(t, result)
				}
				gateway.policyPairs(t, compiled[targetID])
				return
			}
			if os.Getenv("ELYSIA_LIVE_SUITE") == "cache" {
				gateway.cachePairs(t, compiled[targetID])
				return
			}
			gateway.bind(t, protocol.CapabilitySet{protocol.TextCapability: true, protocol.FunctionToolsCapability: true, protocol.UsageCapability: true, protocol.ReasoningCapability: true, protocol.NativeExtensionsCapability: true})
			for _, sourceID := range liveProtocolIDs {
				for _, isStream := range []bool{false, true} {
					if !available[targetID][isStream] {
						continue
					}
					id := fmt.Sprintf("matrix/%s/%t", sourceID, isStream)
					result, _ := gateway.request(t, id, compiled[sourceID], liveRequest(t, "grp", isStream), isStream)
					suite.record(t, result)
					gateway.toolRoundTrip(t, id, compiled[sourceID], isStream)
				}
			}
			service, err := gateway.server.protocolService()
			if err != nil {
				t.Fatal(err)
			}
			custom, _ := service.Pin("verification-envelope")
			customResult, _ := gateway.request(t, "custom/declarative/text", custom, liveRequest(t, "grp", false), false)
			suite.record(t, customResult)
			if os.Getenv("ELYSIA_LIVE_SUITE") != "matrix" {
				gateway.cachePairs(t, compiled[targetID])
			}
		})
	}
	for _, result := range suite.Cases {
		if result.Status == "failed" {
			t.Errorf("%s -> %s failed: %s", result.ID, result.Target, result.Reason)
		}
	}
}

func (gateway *liveGateway) cacheOrderControl(t *testing.T, ingress *protocol.Compiled) {
	gateway.bind(t, gateway.target.Definition().Capabilities)
	for _, route := range []string{"gateway", "direct"} {
		request := liveRequest(t, gateway.suite.Model, false)
		request.Content[0].Children[0].Payload = protocol.StringValue(fmt.Sprintf("Reverse cache control %d %s.\n", gateway.suite.StartedAt.UnixNano(), route) + strings.Repeat("stable alpha beta gamma delta reference information.\n", 2048))
		if route == "gateway" {
			request.Model = protocol.StringValue("grp")
		}
		for repeat := range 3 {
			id := fmt.Sprintf("cache/reverse/%s/%d", route, repeat)
			var result liveCase
			if route == "gateway" {
				result, _ = gateway.request(t, id, ingress, request, false)
			} else {
				result, _ = gateway.suite.direct(t, id, ingress, request, false)
			}
			gateway.suite.record(t, result)
			if result.Status != "passed" {
				break
			}
		}
	}
}

func (gateway *liveGateway) toolRoundTrip(t *testing.T, id string, ingress *protocol.Compiled, isStream bool) {
	hasFollowup := false
	defer func() {
		if !hasFollowup {
			gateway.suite.record(t, liveCase{ID: id + "/tool-result", Ingress: ingress.Identity().DefinitionID, Target: gateway.target.Identity().DefinitionID, Revision: gateway.target.Hash(), Stream: isStream, Status: "not_run", Reason: "first tool phase did not provide a usable call"})
		}
	}()
	request := liveRequest(t, "grp", isStream)
	request.Content[0].Children[0].Payload = protocol.StringValue("API verification with a synthetic client-side function. Return a verify_echo function call when requested; the client supplies its result.")
	request.Tools = []protocol.Tool{{Kind: protocol.FunctionTool, Name: protocol.StringValue("verify_echo"), Description: protocol.StringValue("Return the provided integer unchanged."), InputSchema: mustProtocolValue(t, `{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`)}}
	request.ToolChoice = mustProtocolValue(t, `{"mode":"function","name":"verify_echo"}`)
	request.Content[1].Children[0].Payload = protocol.StringValue("Call verify_echo with value 7.")
	first, response := gateway.request(t, id+"/tool-call", ingress, request, isStream)
	if response == nil {
		gateway.suite.record(t, first)
		return
	}
	calls := liveCalls(response.Content)
	if len(calls) != 1 {
		first.Status = "failed"
		first.Reason = "expected exactly one function call"
		gateway.suite.record(t, first)
		return
	}
	call := calls[0]
	if string(call.Name.Bytes()) != `"verify_echo"` || call.CallID.IsZero() || call.Input == nil || call.Input.Kind != protocol.JSONInput {
		first.Status = "failed"
		first.Reason = "tool name, identity or JSON input lost"
		gateway.suite.record(t, first)
		return
	}
	var input struct {
		Value int `json:"value"`
	}
	if err := call.Input.Value.Decode(&input); err != nil || input.Value != 7 {
		first.Status = "failed"
		first.Reason = "tool argument differs from requested value"
		gateway.suite.record(t, first)
		return
	}
	first.ToolCalls = []string{string(call.CallID.Bytes())}
	gateway.suite.record(t, first)
	// The synthetic client authors this follow-up explicitly; the gateway does
	// not execute the business tool or rewrite the provider's response history.
	payload := protocol.StringValue("7")
	if ingress.Identity().Family == "gemini" {
		payload = mustProtocolValue(t, `{"value":7}`)
	}
	request.Content = append(request.Content, protocol.Node{Kind: protocol.MessageNode, Role: protocol.StringValue("assistant"), Children: []protocol.Node{{Kind: protocol.ToolCallNode, CallID: call.CallID, Name: call.Name, Input: call.Input}}}, protocol.Node{Kind: protocol.ToolResultNode, CallID: call.CallID, Name: call.Name, Payload: payload})
	request.ToolChoice = mustProtocolValue(t, `{"mode":"none"}`)
	hasFollowup = true
	second, _ := gateway.request(t, id+"/tool-result", ingress, request, isStream)
	gateway.suite.record(t, second)
}

func (gateway *liveGateway) policyPairs(t *testing.T, ingress *protocol.Compiled) {
	if ingress.Identity().Family == "gemini" {
		gateway.suite.record(t, liveCase{ID: "policy/explicit-resource", Target: ingress.Identity().DefinitionID, Status: "not_run", Reason: "no verified cache-resource creation operation or existing resource supplied"})
		return
	}
	for _, route := range []string{"direct", "gateway"} {
		request := liveRequest(t, gateway.suite.Model, false)
		request.Content[0].Children[0].Payload = protocol.StringValue(fmt.Sprintf("Policy verification %d %s.\n", gateway.suite.StartedAt.UnixNano(), route) + strings.Repeat("fixed prefix alpha beta gamma delta.\n", 1024))
		if ingress.Identity().Family == "claude" {
			request.Content[0].Children[0].Cache = []protocol.CacheIntent{{Kind: "breakpoint", Location: "block", Value: mustProtocolValue(t, `{"type":"ephemeral"}`), TTL: protocol.StringValue("1h")}}
			request.Tools = []protocol.Tool{{Kind: protocol.FunctionTool, Name: protocol.StringValue("verify_echo"), InputSchema: mustProtocolValue(t, `{"type":"object"}`), Cache: []protocol.CacheIntent{{Kind: "breakpoint", Location: "tool", Value: mustProtocolValue(t, `{"type":"ephemeral"}`), TTL: protocol.StringValue("1h")}}}}
		} else {
			request.Cache = []protocol.CacheIntent{{Kind: "key", Location: "request", Value: protocol.StringValue(fmt.Sprintf("verification-%d-%s", gateway.suite.StartedAt.UnixNano(), route))}, {Kind: "retention", Location: "request", Value: protocol.StringValue("24h")}}
		}
		if route == "gateway" {
			request.Model = protocol.StringValue("grp")
		}
		for repeat := 0; repeat < 3; repeat++ {
			var result liveCase
			id := fmt.Sprintf("policy/%s/%d", route, repeat)
			if route == "direct" {
				result, _ = gateway.suite.direct(t, id, ingress, request, false)
			} else {
				result, _ = gateway.request(t, id, ingress, request, false)
			}
			gateway.suite.record(t, result)
			if result.Status != "passed" {
				break
			}
		}
	}
	gateway.suite.record(t, liveCase{ID: "policy/expiry", Target: ingress.Identity().DefinitionID, Status: "not_run", Reason: "policy field acceptance and cache reads do not establish TTL expiry"})
}

func (gateway *liveGateway) cachePairs(t *testing.T, ingress *protocol.Compiled) {
	gateway.bind(t, gateway.target.Definition().Capabilities)
	for _, tokens := range []int{4096, 8192, 16384} {
		observedRead := map[string]bool{}
		for _, route := range []string{"direct", "gateway"} {
			prefix := fmt.Sprintf("Verification prefix %d %s %s.\n", gateway.suite.StartedAt.UnixNano(), ingress.Identity().DefinitionID, route) + strings.Repeat("stable alpha beta gamma delta reference information.\n", tokens/8)
			request := liveRequest(t, gateway.suite.Model, false)
			request.Content[0].Children[0].Payload = protocol.StringValue(prefix)
			if ingress.Identity().DefinitionID == "anthropic-api" {
				request.Content[0].Children[0].Cache = []protocol.CacheIntent{{Kind: "breakpoint", Location: "block", Value: mustProtocolValue(t, `{"type":"ephemeral"}`), TTL: protocol.StringValue("5m")}}
			}
			if route == "gateway" {
				request.Model = protocol.StringValue("grp")
			}
			var firstRequest []byte
			for repeat := 0; repeat < 3; repeat++ {
				if repeat == 2 {
					request.Content[1].Children[0].Payload = protocol.StringValue("Again reply with OK.")
				}
				id := fmt.Sprintf("cache/%s/%d/%d", route, tokens, repeat)
				var result liveCase
				if route == "direct" {
					result, _ = gateway.suite.direct(t, id, ingress, request, false)
				} else {
					result, _ = gateway.request(t, id, ingress, request, false)
				}
				if result.Status == "passed" {
					if !bytes.Contains(result.Wire.request, []byte(strings.ReplaceAll(prefix, "\n", `\n`))) {
						result.Status = "failed"
						result.Reason = "cache prefix changed on wire"
					}
					if repeat == 0 {
						firstRequest = result.Wire.request
					} else if repeat == 1 && !bytes.Equal(firstRequest, result.Wire.request) {
						result.Status = "failed"
						result.Reason = "identical cache request rendered differently"
					}
					if result.UpstreamUsage != nil && result.UpstreamUsage.CacheRead != nil && result.UpstreamUsage.CacheRead.Count > 0 {
						observedRead[route] = true
					}
				}
				gateway.suite.record(t, result)
				if result.Status != "passed" {
					break
				}
			}
		}
		if observedRead["direct"] && observedRead["gateway"] {
			break
		}
	}
}

func TestLiveBudgetStopsBeforeSending(t *testing.T) {
	budget := &liveBudget{path: t.TempDir() + "/budget.json", Calls: liveRequestLimit - 1}
	if err := budget.reserve(); err != nil {
		t.Fatal(err)
	}
	if err := budget.reserve(); err == nil {
		t.Fatal("request limit bypassed")
	}
	budget.Calls = 0
	if err := budget.stop("quota exhausted"); err != nil {
		t.Fatal(err)
	}
	if err := budget.reserve(); err == nil {
		t.Fatal("quota stop bypassed")
	}
}
