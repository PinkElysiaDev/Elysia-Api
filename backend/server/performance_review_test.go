package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

// Authored mappings are installed and verified before measurement. These
// fixtures use the real store/runtime binding boundary, not a bypass flag.
func configureReviewMapping(t *testing.T, server *Server) {
	mode := os.Getenv("ELYSIA_REVIEW_MAPPING")
	if mode == "" {
		return
	}
	hasAfter := mode == "after" || mode == "after-decode" || mode == "after-encode"
	if !hasAfter && mode != "json-rule" {
		t.Fatal("invalid review mapping")
	}
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := server.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	if hasAfter {
		definition := presetDefinition(t, "anthropic-messages")
		for _, direction := range []protocol.Direction{protocol.DecodeRequest, protocol.EncodeRequest} {
			if mode == "after-decode" && direction != protocol.DecodeRequest || mode == "after-encode" && direction != protocol.EncodeRequest {
				continue
			}
			mapping := definition.Directions[direction]
			mapping.After = &protocol.Expression{Op: "read", Path: ""}
			definition.Directions[direction] = mapping
		}
		compiled := activateGatewayDefinition(t, server, definition)
		for i := range bindings {
			if bindings[i].Binding.ProtocolID == definition.ID {
				bindings[i].Binding.RevisionHash = compiled.Hash()
				changed++
			}
		}
	} else {
		present := true
		for i := range bindings {
			if bindings[i].Binding.ProtocolID != "anthropic-messages" {
				continue
			}
			bindings[i].Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "review-json-boundary", Rules: []protocol.ConversionRule{{ID: "review-json-boundary", Order: 55, Enabled: true, Phase: protocol.ConversionRequest, Action: "set", Path: "/model", Value: protocol.StringValue("m"), Match: protocol.ConversionMatch{Path: "/model", Present: &present}}}}}
			changed++
		}
	}
	if changed == 0 {
		t.Fatal("mapping fixture did not change a binding")
	}
	bindings, err = server.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if err := server.store.SaveProtocolBinding(t.Context(), binding); err != nil {
			t.Fatal(err)
		}
	}
	server.invalidateRouteCache()
	raw, _ := json.MarshalIndent(map[string]any{"mapping": mode, "bindings": bindings}, "", "  ")
	if err := os.WriteFile(filepath.Join(os.Getenv("ELYSIA_FLAME_OUTPUT"), "mapping-proof.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

// The provider validates the actual second request, including call identity and
// arguments. A tool definition alone is not a completed tool-roundtrip fixture.
func reviewToolProvider(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Stream   bool
			Messages []json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		second := len(input.Messages) == 3
		if !second && len(input.Messages) != 1 {
			t.Error("unexpected tool history")
			w.WriteHeader(400)
			return
		}
		if second {
			var assistant struct {
				Role  string
				Calls []struct {
					ID       string
					Function struct{ Name, Arguments string }
				} `json:"tool_calls"`
			}
			var result struct {
				Role, Content string
				ID            string `json:"tool_call_id"`
			}
			_ = json.Unmarshal(input.Messages[1], &assistant)
			_ = json.Unmarshal(input.Messages[2], &result)
			if assistant.Role != "assistant" || len(assistant.Calls) != 1 || assistant.Calls[0].ID != "c1" || assistant.Calls[0].Function.Name != "lookup" || assistant.Calls[0].Function.Arguments != `{"q":"synthetic"}` || result.Role != "tool" || result.ID != "c1" || result.Content != "synthetic-result" {
				t.Error("tool history changed")
				w.WriteHeader(400)
				return
			}
		}
		const tool = `{"index":0,"id":"c1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"synthetic\"}"}}`
		message, finish := `{"role":"assistant","content":null,"tool_calls":[`+strings.Replace(tool, `"index":0,`, "", 1)+`]}`, "tool_calls"
		delta := `{"role":"assistant","tool_calls":[` + tool + `]}`
		if second {
			message, delta, finish = `{"role":"assistant","content":"tool-complete"}`, `{"role":"assistant","content":"tool-complete"}`, "stop"
		}
		if !input.Stream {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"r","object":"chat.completion","model":"m","created":1,"choices":[{"index":0,"message":%s,"finish_reason":%q}],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105}}`, message, finish)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, item := range []string{fmt.Sprintf(`{"id":"r","object":"chat.completion.chunk","model":"m","created":1,"choices":[{"index":0,"delta":%s,"finish_reason":null}]}`, delta), fmt.Sprintf(`{"id":"r","object":"chat.completion.chunk","model":"m","created":1,"choices":[{"index":0,"delta":{},"finish_reason":%q}]}`, finish), `{"id":"r","object":"chat.completion.chunk","model":"m","created":1,"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105}}`, `[DONE]`} {
			fmt.Fprintf(w, "data: %s\n\n", item)
			w.(http.Flusher).Flush()
		}
	})
}

func reviewMetrics(t *testing.T, address, action string) conversionHTTPMetrics {
	t.Helper()
	r, err := http.Get(address + "?action=" + action)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("metrics", r.Status)
	}
	var result conversionHTTPMetrics
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func reviewProfile(t *testing.T, gateway conversionHTTPAddress, file, endpoint string) {
	t.Helper()
	request, _ := http.NewRequest("GET", gateway.URL+"/debug/pprof/"+endpoint, nil)
	request.Header.Set("Authorization", "Bearer "+flameToken)
	r, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal(r.Status)
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("ELYSIA_FLAME_OUTPUT"), file), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func reviewResponse(t *testing.T, source *protocol.Compiled, raw []byte, stream bool) *protocol.Response {
	t.Helper()
	options := protocol.EvaluationContext{State: protocol.NewEvaluationState(), Scope: protocol.Scope{Model: "grp"}}
	if !stream {
		result, err := source.DecodeResponse(t.Context(), raw, options)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	collector, err := protocol.NewResponseCollector(protocol.Target{Protocol: source.Identity(), Direction: protocol.EncodeEvent, Capabilities: source.Capabilities(protocol.EncodeEvent)}, protocol.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	err = protocol.ReadFrames(t.Context(), bytes.NewReader(raw), source.Operations()["stream"], 8<<20, func(frame protocol.Value, _ protocol.Object) error {
		decoded, err := source.DecodeFrame(t.Context(), frame, options)
		if err != nil {
			return err
		}
		for _, event := range decoded.Events {
			if _, _, err := collector.Consume(event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestReviewToolRoundtrip(t *testing.T) {
	if os.Getenv("ELYSIA_REVIEW_TOOL") != "1" {
		t.Skip("opt-in isolated tool roundtrip")
	}
	provider := startConversionHTTPChild(t, "provider", "")
	gateway := startConversionHTTPChild(t, "gateway", provider.URL)
	source := flameMatrixProtocol(t, "openai-chat-completions")
	stream := os.Getenv("ELYSIA_FLAME_STREAM") == "sse"
	client := &http.Client{Timeout: time.Minute}
	defer client.CloseIdleConnections()
	round := func() int64 {
		started := time.Now()
		messages := `[{"role":"user","content":"lookup synthetic"}]`
		for turn := range 2 {
			body := fmt.Sprintf(`{"model":"grp","stream":%t,"stream_options":{"include_usage":true},"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}}}],"messages":%s}`, stream, messages)
			if !stream {
				body = strings.Replace(body, `,"stream_options":{"include_usage":true}`, "", 1)
			}
			request, _ := http.NewRequestWithContext(t.Context(), "POST", gateway.URL+"/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+flameToken)
			r, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(r.Body)
			r.Body.Close()
			if err != nil || r.StatusCode != 200 || r.Trailer.Get(gatewayStreamErrorTrailer) != "" {
				t.Fatalf("tool HTTP: %d %v %.300s", r.StatusCode, err, raw)
			}
			response := reviewResponse(t, source, raw, stream)
			if response.Usage == nil || response.Usage.Input == nil || response.Usage.Input.Count != 100 || response.Usage.Output == nil || response.Usage.Output.Count != 5 {
				t.Fatal("tool usage lost")
			}
			if turn == 0 {
				calls := 0
				var visit func([]protocol.Node)
				visit = func(nodes []protocol.Node) {
					for _, node := range nodes {
						if node.Kind == protocol.ToolCallNode {
							calls++
							if !node.CallID.IsString("c1") || !node.Name.IsString("lookup") || node.Input == nil || string(node.Input.Value.Bytes()) != `{"q":"synthetic"}` {
								t.Fatal("tool call changed")
							}
						}
						visit(node.Children)
					}
				}
				visit(response.Content)
				if calls != 1 {
					t.Fatal("tool call missing")
				}
				messages = `[{"role":"user","content":"lookup synthetic"},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"synthetic\"}"}}]},{"role":"tool","tool_call_id":"c1","content":"synthetic-result"}]`
			} else if flameRequestText(response.Content) != "tool-complete" {
				t.Fatal("tool follow-up incomplete")
			}
		}
		return time.Since(started).Nanoseconds()
	}
	for range 2 {
		round()
	}
	reviewMetrics(t, gateway.Metrics, "settle")
	before := reviewMetrics(t, gateway.Metrics, "reset-without-gc")
	providerBefore := reviewMetrics(t, provider.Metrics, "")
	var latencies []int64
	for range 64 {
		latencies = append(latencies, round())
	}
	after := reviewMetrics(t, gateway.Metrics, "settle")
	if reviewMetrics(t, provider.Metrics, "").Calls-providerBefore.Calls != 128 {
		t.Fatal("tool generation repeated or missing")
	}
	raw, _ := json.MarshalIndent(map[string]any{"roundtrips": 64, "httpRequests": 128, "stream": stream, "before": before, "after": after, "roundtripLatencyNS": latencies}, "", "  ")
	if err := os.WriteFile(filepath.Join(os.Getenv("ELYSIA_FLAME_OUTPUT"), "tool-roundtrip.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

// A deliberately held provider separates reachable request state from garbage
// that normal GC has not yet visited. Forced collections are diagnostic only.
func TestReviewHeldProfiles(t *testing.T) {
	if os.Getenv("ELYSIA_REVIEW_HELD") != "1" {
		t.Skip("opt-in held-stream profiles")
	}
	provider := startConversionHTTPChild(t, "provider", "")
	gateway := startConversionHTTPChild(t, "gateway", provider.URL)
	client := &http.Client{Timeout: 2 * time.Minute}
	defer client.CloseIdleConnections()
	var results []any
	for batch := range 5 {
		reviewMetrics(t, gateway.Metrics, "reset")
		var cancels []context.CancelFunc
		var responses []*http.Response
		for range 4 {
			ctx, cancel := context.WithCancel(t.Context())
			cancels = append(cancels, cancel)
			defer cancel()
			request, _ := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/messages", strings.NewReader(flameRequestBody(2, true, "image")))
			request.Header.Set("Authorization", "Bearer "+flameToken)
			request.Header.Set("Content-Type", "application/json")
			r, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			responses = append(responses, r)
			if r.StatusCode != 200 {
				t.Fatal(r.Status)
			}
			reader := bufio.NewReader(r.Body)
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(line, `"type":"content_block_delta"`) {
					break
				}
			}
		}
		held := reviewMetrics(t, gateway.Metrics, "reset")
		if held.ActiveRequests != 4 {
			t.Fatal("not four held streams", held.ActiveRequests)
		}
		if batch == 0 {
			reviewProfile(t, gateway, "heap-held.pprof", "heap")
			reviewProfile(t, gateway, "goroutine-held.pprof", "goroutine")
		}
		for i, cancel := range cancels {
			cancel()
			responses[i].Body.Close()
		}
		settled := reviewMetrics(t, gateway.Metrics, "reclaim")
		results = append(results, map[string]any{"batch": batch, "held": held, "cancelled": settled})
		progress, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(filepath.Join(os.Getenv("ELYSIA_FLAME_OUTPUT"), "held-progress.json"), progress, 0600); err != nil {
			t.Fatal(err)
		}
		if settled.ActiveRequests != 0 || settled.QueueDepth != 0 || settled.QueuedPayloadBytes != 0 {
			reviewProfile(t, gateway, "heap-cancelled.pprof", "heap")
			reviewProfile(t, gateway, "goroutine-cancelled.pprof", "goroutine")
			t.Fatalf("retained work after cancellation: active=%d queued=%d payloadBytes=%d heap=%d", settled.ActiveRequests, settled.QueueDepth, settled.QueuedPayloadBytes, settled.Heap)
		}
		if batch == 0 {
			reviewProfile(t, gateway, "heap-cancelled.pprof", "heap")
		}
	}
	raw, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(filepath.Join(os.Getenv("ELYSIA_FLAME_OUTPUT"), "held.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
