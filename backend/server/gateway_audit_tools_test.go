package server

import (
	"encoding/json"
	"fmt"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayAuditToolRoundTrips(t *testing.T)         { auditGatewayToolRoundTrips(t, false) }
func TestGatewayAuditParallelToolRoundTrips(t *testing.T) { auditGatewayToolRoundTrips(t, true) }
func auditGatewayToolRoundTrips(t *testing.T, parallel bool) {
	ids := []string{protocol.PresetChatCompletionsID, protocol.PresetResponsesID, protocol.PresetAnthropicID, protocol.PresetGeminiID}
	for _, upstreamID := range ids {
		t.Run(upstreamID, func(t *testing.T) {
			s, _ := newProtocolAdminTestServer(t)
			activateDiscoveryPresets(t, s)
			service, _ := s.protocolService()
			upstream, _ := service.Pin(upstreamID)
			streaming, second := false, false
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if second && !strings.Contains(string(raw), "900719925474099312345") {
					t.Error("tool result integer lost", string(raw))
				}
				if parallel && !second {
					wire := auditParallelToolFixture(t, upstreamID, streaming)
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
					} else {
						w.Header().Set("Content-Type", "application/json")
					}
					_, _ = io.WriteString(w, wire)
					return
				}
				if parallel && second {
					decoded, e := upstream.DecodeRequest(t.Context(), raw, protocol.EvaluationContext{Scope: protocol.Scope{Model: "m"}})
					if e != nil {
						t.Error(e)
						w.WriteHeader(400)
						return
					}
					results := map[protocol.Value]protocol.Value{}
					var visit func([]protocol.Node)
					visit = func(nodes []protocol.Node) {
						for _, n := range nodes {
							if n.Kind == protocol.ToolResultNode {
								results[n.CallID] = n.Payload
							}
							visit(n.Children)
						}
					}
					visit(decoded.Content)
					calls := liveCalls(decoded.Content)
					if len(calls) != 2 || len(results) != 2 {
						t.Error("parallel association missing", string(raw))
					}
					for _, call := range calls {
						if results[call.CallID].IsZero() {
							t.Error("result lost its call ID", call.CallID)
						}
						var callID string
						_ = call.CallID.Decode(&callID)
						if !strings.Contains(string(results[call.CallID].Bytes()), callID) {
							t.Error("result payload associated with the other parallel call", callID)
						}
					}
				}
				wanted := "tool-decode-response"
				if second {
					wanted = "text-decode-response"
				}
				if streaming {
					wanted = "stream-tool-decode"
					if second {
						wanted = "stream-text-decode"
					}
				}
				for _, sample := range upstream.Definition().Samples {
					if sample.ID != wanted {
						continue
					}
					if !streaming {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write(sample.Input.Bytes())
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					var frames []protocol.Value
					_ = sample.Input.Decode(&frames)
					for _, frame := range frames {
						fields, _ := frame.ReadObject()
						if v := fields["type"]; !v.IsZero() {
							var kind string
							_ = v.Decode(&kind)
							fmt.Fprintf(w, "event: %s\n", kind)
						}
						fmt.Fprintf(w, "data: %s\n\n", frame.Bytes())
					}
					if upstreamID == protocol.PresetChatCompletionsID {
						_, _ = io.WriteString(w, "data: [DONE]\n\n")
					}
					return
				}
				t.Error("missing tool fixture")
			}))
			defer provider.Close()
			setupGatewayModel(t, s, upstream, provider.URL)
			source := storage.ModelSource{ID: "gateway-source", Name: "gateway-source", BaseURL: provider.URL, Platform: "custom:" + upstreamID, Enabled: true}
			if err := s.store.ReplaceSourceModels(t.Context(), source, []storage.Model{{ID: "upstream-model", Name: "upstream-model", Available: true, Enabled: true, ToolsCapable: true}}); err != nil {
				t.Fatal(err)
			}
			if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
				t.Fatal(err)
			}
			s.invalidateRouteCache()
			for _, id := range ids {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/stream=%v", id, stream), func(t *testing.T) {
						streaming, second = stream, false
						ingress, _ := service.Pin(id)
						request := &protocol.Request{SchemaVersion: 1, Model: protocol.StringValue("group"), Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("lookup")}}}}, Tools: []protocol.Tool{{Kind: protocol.FunctionTool, Name: protocol.StringValue("lookup"), InputSchema: mustProtocolValue(t, `{"type":"object","properties":{"n":{"type":"integer"}}}`)}}, Parameters: protocol.Object{"max_output_tokens": mustProtocolValue(t, "32"), "stream": mustProtocolValue(t, fmt.Sprint(stream))}}
						if id == protocol.PresetResponsesID {
							request.Parameters["store"] = mustProtocolValue(t, "false")
						}
						if id == protocol.PresetChatCompletionsID && stream {
							request.Parameters["stream_options"] = mustProtocolValue(t, `{"include_usage":true}`)
						}
						perform := func() *protocol.Response {
							body, e := ingress.EncodeRequest(t.Context(), request, protocol.EvaluationContext{})
							if e != nil {
								t.Fatal(e)
							}
							opName := "generate"
							if stream {
								opName = "stream"
							}
							op := ingress.Operations()[opName]
							path, e := protocol.ExpandOperationPath(op.Path, map[string]string{"model": "group"})
							if e != nil {
								t.Fatal(e)
							}
							r := httptest.NewRequest("POST", "/gateway/"+id+path, strings.NewReader(string(body)))
							r.Header.Set("Authorization", "Bearer gateway-test-token")
							rec := httptest.NewRecorder()
							before := calls
							s.engine.ServeHTTP(rec, r)
							if rec.Code != 200 || calls != before+1 || rec.Result().Trailer.Get("X-Elysia-Stream-Error") != "" {
								t.Fatalf("second=%v status=%d calls=%d trailer=%v body=%s", second, rec.Code, calls-before, rec.Result().Trailer, rec.Body)
							}
							if dir := os.Getenv("ELYSIA_AUDIT_CAPTURE"); dir != "" {
								if e := os.MkdirAll(dir, 0700); e != nil {
									t.Fatal(e)
								}
								fixture, _ := json.Marshal(map[string]any{"upstream": upstreamID, "ingress": id, "stream": stream, "tool": !second, "body": rec.Body.String()})
								name := fmt.Sprintf("tool-%s-%s-%v-%v.json", upstreamID, id, stream, second)
								if parallel {
									name = "parallel-" + name
								}
								if e := os.WriteFile(filepath.Join(dir, name), fixture, 0600); e != nil {
									t.Fatal(e)
								}
							}
							if !stream {
								response, e := ingress.DecodeResponse(t.Context(), rec.Body.Bytes(), protocol.EvaluationContext{})
								if e != nil {
									t.Fatal(e)
								}
								return response
							}
							collector, e := protocol.NewResponseCollector(protocol.Target{Protocol: ingress.Identity(), Direction: protocol.EncodeEvent, Capabilities: ingress.Capabilities(protocol.EncodeEvent)}, protocol.DefaultLimits())
							if e != nil {
								t.Fatal(e)
							}
							opts := protocol.EvaluationContext{State: protocol.NewEvaluationState()}
							for _, line := range strings.Split(rec.Body.String(), "\n") {
								if !strings.HasPrefix(line, "data: {") {
									continue
								}
								v, e := protocol.ParseValue([]byte(strings.TrimPrefix(line, "data: ")))
								if e != nil {
									t.Fatal(e)
								}
								frame, e := ingress.DecodeFrame(t.Context(), v, opts)
								if e != nil {
									t.Fatal(e)
								}
								for _, event := range frame.Events {
									if _, _, e = collector.Consume(event); e != nil {
										t.Fatal(e)
									}
								}
							}
							response, e := collector.Finish()
							if e != nil {
								t.Fatal(e)
							}
							return response
						}
						response := perform()
						tools := liveCalls(response.Content)
						expected := 1
						if parallel {
							expected = 2
						}
						if len(tools) != expected {
							t.Fatalf("expected %d tools, got %d", expected, len(tools))
						}
						request.Content = append(request.Content, response.Content...)
						result := protocol.StringValue(`{"n":900719925474099312345}`)
						if id == protocol.PresetGeminiID {
							result = mustProtocolValue(t, `{"n":900719925474099312345}`)
						}
						for _, call := range tools {
							if parallel {
								result, _ = protocol.EncodeValue(protocol.Object{"n": mustProtocolValue(t, "900719925474099312345"), "for": call.CallID})
								if id != protocol.PresetGeminiID {
									result = protocol.StringValue(string(result.Bytes()))
								}
							}
							request.Content = append(request.Content, protocol.Node{Kind: protocol.ToolResultNode, CallID: call.CallID, Name: call.Name, Payload: result})
						}
						second = true
						response = perform()
						raw, _ := protocol.EncodeValue(response)
						if !strings.Contains(string(raw.Bytes()), "hi") {
							t.Fatal("second answer missing", string(raw.Bytes()))
						}
					})
				}
			}
		})
	}
}

// Independent complete provider fixtures include two same-name calls with
// different IDs and mixed text. Argument fragments arrive in reverse call order.
func auditParallelToolFixture(t *testing.T, id string, stream bool) string {
	t.Helper()
	type obj = map[string]any
	encode := func(v any) string {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	var frames []any
	var response any
	switch id {
	case protocol.PresetChatCompletionsID:
		calls := []any{}
		for i, cid := range []string{"c1", "c2"} {
			calls = append(calls, obj{"id": cid, "type": "function", "function": obj{"name": "lookup", "arguments": fmt.Sprintf(`{"n":%d}`, i+1)}})
		}
		response = obj{"id": "r", "object": "chat.completion", "created": 1, "model": "m", "choices": []any{obj{"index": 0, "message": obj{"role": "assistant", "content": "hi", "tool_calls": calls}, "finish_reason": "tool_calls"}}}
		chunk := func(d any, finish any) any {
			return obj{"id": "r", "object": "chat.completion.chunk", "created": 1, "model": "m", "choices": []any{obj{"index": 0, "delta": d, "finish_reason": finish}}}
		}
		frames = append(frames, chunk(obj{"role": "assistant", "content": "hi"}, nil))
		starts := []any{}
		for i, cid := range []string{"c1", "c2"} {
			starts = append(starts, obj{"index": i, "id": cid, "type": "function", "function": obj{"name": "lookup", "arguments": ""}})
		}
		frames = append(frames, chunk(obj{"tool_calls": starts}, nil))
		for _, i := range []int{1, 0} {
			frames = append(frames, chunk(obj{"tool_calls": []any{obj{"index": i, "function": obj{"arguments": fmt.Sprintf(`{"n":%d}`, i+1)}}}}, nil))
		}
		frames = append(frames, chunk(obj{}, "tool_calls"))
	case protocol.PresetGeminiID:
		response = obj{"responseId": "r", "modelVersion": "m", "candidates": []any{obj{"index": 0, "content": obj{"role": "model", "parts": []any{obj{"text": "hi"}, obj{"functionCall": obj{"id": "c1", "name": "lookup", "args": obj{"n": 1}}}, obj{"functionCall": obj{"id": "c2", "name": "lookup", "args": obj{"n": 2}}}}}, "finishReason": "STOP"}}}
		frames = []any{response}
	case protocol.PresetAnthropicID:
		content := []any{obj{"type": "text", "text": "hi"}, obj{"type": "tool_use", "id": "c1", "name": "lookup", "input": obj{"n": 1}}, obj{"type": "tool_use", "id": "c2", "name": "lookup", "input": obj{"n": 2}}}
		usage := obj{"input_tokens": 3, "output_tokens": 2}
		response = obj{"id": "r", "type": "message", "model": "m", "role": "assistant", "content": content, "stop_reason": "tool_use", "stop_sequence": nil, "usage": usage}
		frames = append(frames, obj{"type": "message_start", "message": obj{"id": "r", "type": "message", "model": "m", "role": "assistant", "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": obj{"input_tokens": 3, "output_tokens": 0}}})
		frames = append(frames, obj{"type": "content_block_start", "index": 0, "content_block": obj{"type": "text", "text": ""}}, obj{"type": "content_block_delta", "index": 0, "delta": obj{"type": "text_delta", "text": "hi"}}, obj{"type": "content_block_stop", "index": 0})
		for i, cid := range []string{"c1", "c2"} {
			frames = append(frames, obj{"type": "content_block_start", "index": i + 1, "content_block": obj{"type": "tool_use", "id": cid, "name": "lookup", "input": obj{}}})
		}
		for _, i := range []int{2, 1} {
			frames = append(frames, obj{"type": "content_block_delta", "index": i, "delta": obj{"type": "input_json_delta", "partial_json": fmt.Sprintf(`{"n":%d}`, i)}}, obj{"type": "content_block_stop", "index": i})
		}
		frames = append(frames, obj{"type": "message_delta", "delta": obj{"stop_reason": "tool_use", "stop_sequence": nil}, "usage": usage}, obj{"type": "message_stop"})
	case protocol.PresetResponsesID:
		text := obj{"type": "output_text", "text": "hi", "annotations": []any{}}
		message := obj{"type": "message", "id": "msg1", "status": "completed", "role": "assistant", "content": []any{text}}
		output := []any{message}
		for i, cid := range []string{"c1", "c2"} {
			output = append(output, obj{"type": "function_call", "id": "item_" + cid, "call_id": cid, "name": "lookup", "arguments": fmt.Sprintf(`{"n":%d}`, i+1), "status": "completed"})
		}
		response = obj{"id": "r", "object": "response", "created_at": 1, "model": "m", "status": "completed", "output": output}
		frames = append(frames, obj{"type": "response.created", "response": obj{"id": "r", "object": "response", "created_at": 1, "model": "m", "status": "in_progress", "output": []any{}}})
		frames = append(frames, obj{"type": "response.output_item.added", "output_index": 0, "item": obj{"type": "message", "id": "msg1", "status": "in_progress", "role": "assistant", "content": []any{}}}, obj{"type": "response.content_part.added", "output_index": 0, "item_id": "msg1", "content_index": 0, "part": obj{"type": "output_text", "text": "", "annotations": []any{}}}, obj{"type": "response.output_text.delta", "output_index": 0, "item_id": "msg1", "content_index": 0, "delta": "hi"}, obj{"type": "response.content_part.done", "output_index": 0, "item_id": "msg1", "content_index": 0, "part": text}, obj{"type": "response.output_item.done", "output_index": 0, "item": message})
		for i, cid := range []string{"c1", "c2"} {
			frames = append(frames, obj{"type": "response.output_item.added", "output_index": i + 1, "item": obj{"type": "function_call", "id": "item_" + cid, "call_id": cid, "name": "lookup", "arguments": "", "status": "in_progress"}})
		}
		for _, i := range []int{2, 1} {
			cid := fmt.Sprintf("c%d", i)
			frames = append(frames, obj{"type": "response.function_call_arguments.delta", "output_index": i, "item_id": "item_" + cid, "delta": fmt.Sprintf(`{"n":%d}`, i)}, obj{"type": "response.output_item.done", "output_index": i, "item": output[i]})
		}
		frames = append(frames, obj{"type": "response.completed", "response": response})
		for i, f := range frames {
			f.(obj)["sequence_number"] = i
		}
	default:
		t.Fatal(id)
	}
	if !stream {
		return encode(response)
	}
	var out strings.Builder
	for _, frame := range frames {
		if kind, ok := frame.(obj)["type"].(string); ok {
			fmt.Fprintf(&out, "event: %s\n", kind)
		}
		fmt.Fprintf(&out, "data: %s\n\n", encode(frame))
	}
	if id == protocol.PresetChatCompletionsID {
		out.WriteString("data: [DONE]\n\n")
	}
	return out.String()
}
