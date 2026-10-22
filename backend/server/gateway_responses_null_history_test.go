package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

// Use the observed shape, never the private encrypted state from the log export.
const nullReasoningHistory = `{"type":"reasoning","id":"synthetic-thought","summary":[],"content":null,"encrypted_content":"synthetic-private-state"}`

func TestGatewayResponsesNullReasoningHistory(t *testing.T) {
	for _, mode := range []string{"compatible", "strict"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", mode, stream), func(t *testing.T) {
				s, _ := newProtocolAdminTestServer(t)
				s.engine.Use(s.requestLifecycle())
				activateDiscoveryPresets(t, s)
				service, err := s.protocolService()
				if err != nil {
					t.Fatal(err)
				}
				upstream, _ := service.Pin(protocol.PresetResponsesID)
				var calls atomic.Int32
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					round := calls.Add(1)
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					var request struct {
						Model string
						Input []json.RawMessage
					}
					if err := sonic.ConfigStd.Unmarshal(raw, &request); err != nil || len(request.Input) < 41 {
						t.Errorf("invalid outbound history: %v, items=%d", err, len(request.Input))
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if request.Model != "upstream-model" || string(request.Input[40]) != nullReasoningHistory || !bytes.Contains(raw, []byte(`"counter":9007199254740993`)) {
						t.Error("model rewrite changed native reasoning or an unknown large integer")
					}
					if round == 2 && (len(request.Input) != 43 || !bytes.Contains(request.Input[41], []byte(`"call_id":"call"`)) || string(request.Input[42]) != `{"type":"function_call_output","call_id":"call","output":"synthetic tool result"}`) {
						t.Error("tool follow-up lost call association or history order")
					}
					writeNullHistoryResponse(t, w, upstream, stream, round == 1)
				}))
				defer provider.Close()
				setupGatewayModel(t, s, upstream, provider.URL)
				source := storage.ModelSource{ID: "gateway-source", Name: "gateway-source", BaseURL: provider.URL, Platform: "custom:" + upstream.Identity().DefinitionID, Enabled: true}
				if err := s.store.ReplaceSourceModels(t.Context(), source, []storage.Model{{ID: "upstream-model", Name: "upstream-model", Enabled: true, Available: true, ToolsCapable: true}}); err != nil {
					t.Fatal(err)
				}
				if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
					t.Fatal(err)
				}
				bindings, err := s.store.ListProtocolBindings(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for i := range bindings {
					bindings[i].Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "null-history", Mode: mode, Rules: []protocol.ConversionRule{}}}
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
				web := startTestServer(s.engine)
				defer web.Close()
				web.Client().Timeout = 10 * time.Second
				history := make([]string, 40, 43)
				for i := range history {
					history[i] = fmt.Sprintf(`{"role":"user","content":"synthetic history %d"}`, i)
				}
				history = append(history, nullReasoningHistory)
				call := func(items []string, wantStatus int) []byte {
					t.Helper()
					body := fmt.Sprintf(`{"model":"group","store":false,"stream":%v,"input":[%s],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"opaque_extension":{"counter":9007199254740993}}`, stream, strings.Join(items, ","))
					req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, web.URL+"/gateway/openai-responses/responses", strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer gateway-test-token")
					req.Header.Set("Content-Type", "application/json")
					response, err := web.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					raw, err := io.ReadAll(response.Body)
					if err != nil || response.StatusCode != wantStatus || response.Trailer.Get(gatewayStreamErrorTrailer) != "" {
						t.Fatalf("status=%d, read=%v, trailer=%v, body=%s", response.StatusCode, err, response.Trailer, raw)
					}
					return raw
				}
				first := readNullHistoryResponse(t, upstream, call(history, http.StatusOK), stream)
				if calls.Load() != 1 || len(first.Output) != 1 || !bytes.Contains(first.Output[0], []byte(`"arguments":"{\"n\":9007199254740993}"`)) {
					t.Fatal("first response lost tool arguments or repeated generation", calls.Load())
				}
				history = append(history, string(first.Output[0]), `{"type":"function_call_output","call_id":"call","output":"synthetic tool result"}`)
				second := readNullHistoryResponse(t, upstream, call(history, http.StatusOK), stream)
				if calls.Load() != 2 || len(second.Output) != 1 || !bytes.Contains(second.Output[0], []byte(`"text":"hi"`)) {
					t.Fatal("tool follow-up incomplete or repeated generation", calls.Load())
				}
				history[40] = strings.Replace(nullReasoningHistory, `"content":null`, `"content":{}`, 1)
				failure := call(history, http.StatusBadRequest)
				if calls.Load() != 2 || !bytes.Contains(failure, []byte("/input/40/content")) || !bytes.Contains(failure, []byte("decode_request")) || bytes.Contains(failure, []byte("synthetic-private-state")) {
					t.Fatal("local rejection sent upstream or lost safe diagnostics", calls.Load(), string(failure))
				}
				checkNullHistoryRecords(t, s)
			})
		}
	}
}

func checkNullHistoryRecords(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var records []storage.UsageLogItem
	for {
		_, items, err := s.store.QueryUsageLogs(t.Context(), storage.UsageQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 3 {
			records = items
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want exactly three settled calls, got %d", len(items))
		}
		time.Sleep(10 * time.Millisecond)
	}
	successes, failures, totalTokens := 0, 0, int64(0)
	for _, item := range records {
		var record usageRecord
		if err := sonic.ConfigStd.Unmarshal([]byte(storedRecordJSON(t, s.store, item.RequestID)), &record); err != nil {
			t.Fatal(err)
		}
		if record.RetryCount != 0 {
			t.Fatal("history handling triggered retries")
		}
		if record.StatusCode == http.StatusOK {
			successes++
			if record.ProtocolUsage == nil {
				t.Fatal("missing observed usage")
			}
			totalTokens += record.ProtocolUsage.Total.Count
		} else {
			failures++
			if record.StatusCode != http.StatusBadRequest || record.UpstreamHTTPStatus != 0 || len(record.ConversionIssues) != 1 || record.ConversionIssues[0].Stage != "decode_request" {
				t.Fatal("incorrect local decode attribution", record.StatusCode, record.ConversionIssues)
			}
		}
		diagnostics, err := sonic.ConfigStd.Marshal(record.ConversionIssues)
		if err != nil || bytes.Contains(diagnostics, []byte("synthetic-private-state")) {
			t.Fatal("diagnostics leaked encrypted history", err)
		}
	}
	if successes != 2 || failures != 1 || totalTokens != 44 {
		t.Fatal("unexpected settlement count or usage", successes, failures, totalTokens)
	}
}

func writeNullHistoryResponse(t *testing.T, w http.ResponseWriter, compiled *protocol.Compiled, stream, tool bool) {
	t.Helper()
	sampleID := "stream-text-decode"
	if tool {
		sampleID = "stream-tool-decode"
	}
	for _, sample := range compiled.Definition().Samples {
		if sample.ID != sampleID {
			continue
		}
		var frames []protocol.Value
		if err := sample.Input.Decode(&frames); err != nil {
			t.Error(err)
			return
		}
		last, _ := frames[len(frames)-1].ReadObject()
		response, _ := last["response"].ReadObject()
		response["usage"] = mustProtocolValue(t, `{"input_tokens":20,"output_tokens":2,"total_tokens":22}`)
		last["response"], _ = protocol.EncodeValue(response)
		frames[len(frames)-1], _ = protocol.EncodeValue(last)
		if !stream {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(last["response"].Bytes())
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range frames {
			fmt.Fprintf(w, "data: %s\n\n", frame.Bytes())
			w.(http.Flusher).Flush()
		}
		return
	}
	t.Error("missing preset fixture", sampleID)
}

type nullHistoryResponse struct {
	Status string
	Output []json.RawMessage
	Usage  struct {
		Input  int `json:"input_tokens"`
		Output int `json:"output_tokens"`
		Total  int `json:"total_tokens"`
	}
}

func readNullHistoryResponse(t *testing.T, compiled *protocol.Compiled, raw []byte, stream bool) nullHistoryResponse {
	t.Helper()
	var result nullHistoryResponse
	if stream {
		validator, err := compiled.NewWireStreamValidation(protocol.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "data: {") {
				continue
			}
			frame := mustProtocolValue(t, strings.TrimPrefix(line, "data: "))
			if err := validator.Consume(t.Context(), frame); err != nil {
				t.Fatal(err)
			}
			fields, _ := frame.ReadObject()
			if fields["type"].IsString("response.completed") {
				if err := fields["response"].Decode(&result); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := validator.Finish(); err != nil {
			t.Fatal(err)
		}
	} else if err := sonic.ConfigStd.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || result.Usage.Input != 20 || result.Usage.Output != 2 || result.Usage.Total != 22 {
		t.Fatalf("response not fully consumed or usage changed: %+v", result)
	}
	return result
}
