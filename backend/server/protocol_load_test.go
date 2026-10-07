package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

const loadRequests = 128
const loadRepeats = 3
const loadDelay = 10 * time.Millisecond
const loadFrameDelay = time.Millisecond
const loadStreamBytes = 64 << 10
const loadFrameBytes = 4 << 10
const loadWarmupRequests = 32

type loadMeasurement struct {
	Path                  string   `json:"path"`
	Workload              string   `json:"workload"`
	Delayed               bool     `json:"delayed"`
	Concurrency           int      `json:"concurrency"`
	Repeat                int      `json:"repeat"`
	Requests              int      `json:"requests"`
	ElapsedNS             int64    `json:"elapsedNS"`
	CPUPerRequestNS       float64  `json:"cpuPerRequestNS"`
	Throughput            float64  `json:"requestsPerSecond"`
	AllocatedPerRequest   float64  `json:"allocatedBytesPerRequest"`
	AllocationsPerRequest float64  `json:"allocationsPerRequest"`
	GC                    uint32   `json:"gcCycles"`
	GCPauseNS             uint64   `json:"gcPauseNS"`
	RetainedHeapDelta     int64    `json:"retainedHeapDeltaBytes"`
	LatencyNS             [3]int64 `json:"latencyP50P95P99NS"`
	FirstFrameNS          [3]int64 `json:"firstFrameP50P95P99NS"`
	Failures              int64    `json:"failures"`
}

// TestProtocolLoad is opt-in and never contacts an external host. Timed work
// includes loopback HTTP, routing, conversion and SQLite usage settlement. The
// load generator and deterministic upstream share the measured process; its CPU
// figure is the whole harness cost, not an isolated production server claim.
func TestProtocolLoad(t *testing.T) {
	if os.Getenv("ELYSIA_LOAD_TESTS") != "1" {
		t.Skip("local load verification requires explicit opt-in")
	}
	directory := os.Getenv("ELYSIA_VERIFY_DIR")
	if directory == "" {
		t.Fatal("evidence directory is required")
	}
	count := loadSetting(t, "ELYSIA_LOAD_REQUESTS", loadRequests)
	repeats := loadSetting(t, "ELYSIA_LOAD_REPEATS", loadRepeats)
	var measurements []loadMeasurement
	for _, targetID := range []string{"openai-chat-completions", "anthropic-messages", "openai-responses", "declarative"} {
		t.Run(targetID, func(t *testing.T) {
			upstreamID := targetID
			if targetID == "declarative" {
				upstreamID = "openai-chat-completions"
			}
			upstream := compileFixtureDefinition(t, presetDefinition(t, upstreamID))
			var isDelayed, isStream atomic.Bool
			frames, body := loadResponse(t, upstream)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if isDelayed.Load() {
					time.Sleep(loadDelay)
				}
				if !isStream.Load() {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(body)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, frame := range frames {
					_, _ = w.Write(frame)
					w.(http.Flusher).Flush()
					if isDelayed.Load() {
						time.Sleep(loadFrameDelay)
					}
				}
			}))
			t.Cleanup(provider.Close)
			var definitions []protocol.Definition
			if targetID == "declarative" {
				definitions = append(definitions, verificationEnvelopeDefinition(t))
			}
			server := newTestServerWithStore(t, presetGroup(t, "custom:"+upstreamID, loadBase(provider.URL, upstream)), definitions...)
			ingress := compileFixtureDefinition(t, presetDefinition(t, "openai-chat-completions"))
			path := "/v1/chat/completions"
			if targetID == "declarative" {
				service, err := server.protocolService()
				if err != nil {
					t.Fatal(err)
				}
				ingress, _ = service.Pin(definitions[0].ID)
				path = "/gateway/" + ingress.Identity().DefinitionID + "/generate"
				bindings, err := server.store.ListProtocolBindings(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, binding := range bindings {
					binding.Binding.Capabilities = protocol.CapabilitySet{protocol.TextCapability: true, protocol.FunctionToolsCapability: true, protocol.UsageCapability: true}
					binding.Combinations = verifyGatewayBinding(t.Context(), service.View(), upstream, binding.Binding.Capabilities)
					if err := server.store.SaveProtocolBinding(t.Context(), binding); err != nil {
						t.Fatal(err)
					}
				}
				server.invalidateRouteCache()
			}
			if err := server.store.ImportLegacyConfig(t.Context(), []storage.APIToken{{Name: "load", Token: "synthetic-local-load", Enabled: true, AllowedGroups: []string{"grp"}}}, nil, nil); err != nil {
				t.Fatal(err)
			}
			server.engine.POST("/v1/chat/completions", server.authMiddleware(), server.chatCompletions)
			server.engine.Any("/gateway/:protocolId/*path", server.authMiddleware(), server.gatewayProtocol)
			gateway := httptest.NewServer(server.engine)
			t.Cleanup(gateway.Close)
			client := &http.Client{Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64}, Timeout: time.Minute}
			t.Cleanup(client.CloseIdleConnections)
			for _, workload := range []string{"short", "prefix32k", "history256k", "tools32", "stream64k"} {
				if filter := os.Getenv("ELYSIA_LOAD_FILTER"); filter != "" && !strings.Contains(targetID+"/"+workload, filter) {
					continue
				}
				isStream.Store(workload == "stream64k")
				request := loadRequest(t, workload)
				wire, err := ingress.EncodeRequest(t.Context(), request, protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				for _, delayed := range []bool{false, true} {
					isDelayed.Store(delayed)
					for _, concurrency := range []int{1, 8, 32} {
						if !selectLoadScenario(targetID, workload, delayed, concurrency) {
							continue
						}
						server.startUsageWriter()
						warmup := measureGatewayLoad(t, server, client, gateway.URL+path, wire, loadWarmupRequests, concurrency)
						if warmup.Failures > 0 {
							t.Fatal("load warmup failed")
						}
						for repeat := 0; repeat < repeats; repeat++ {
							server.startUsageWriter()
							measurement := measureGatewayLoad(t, server, client, gateway.URL+path, wire, count, concurrency)
							measurement.Path, measurement.Workload, measurement.Delayed, measurement.Concurrency, measurement.Repeat = targetID, workload, delayed, concurrency, repeat
							measurements = append(measurements, measurement)
							raw, err := json.MarshalIndent(measurements, "", "  ")
							if err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(filepath.Join(directory, "load.json"), raw, 0600); err != nil {
								t.Fatal(err)
							}
							if measurement.Failures > 0 {
								t.Fatalf("%s/%s: %d failed requests", targetID, workload, measurement.Failures)
							}
						}
					}
				}
			}
		})
	}
	if len(measurements) == 0 {
		t.Fatal("load filters selected no scenarios")
	}
}

func selectLoadScenario(path, workload string, isDelayed bool, concurrency int) bool {
	selection := os.Getenv("ELYSIA_LOAD_SCENARIOS")
	if selection == "" {
		return true
	}
	key := fmt.Sprintf("%s/%s/%t/%d", path, workload, isDelayed, concurrency)
	for _, scenario := range strings.Split(selection, ",") {
		if scenario == key {
			return true
		}
	}
	return false
}

func loadSetting(t *testing.T, name string, fallback int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 10000 {
		t.Fatalf("invalid %s", name)
	}
	return value
}

func loadBase(origin string, compiled *protocol.Compiled) string {
	if path := compiled.Operations()["generate"].Path; path == "/chat/completions" || path == "/responses" {
		return origin + "/v1"
	}
	return origin
}

func loadRequest(t *testing.T, workload string) *protocol.Request {
	request := &protocol.Request{SchemaVersion: 1, Model: protocol.StringValue("grp"), Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("hello")}}}}, Parameters: protocol.Object{"max_output_tokens": mustProtocolValue(t, "128")}}
	switch workload {
	case "prefix32k":
		request.Content = append([]protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("system"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue(strings.Repeat("stable prefix. ", (32<<10)/15))}}}}, request.Content...)
	case "history256k":
		request.Content = nil
		for index := range 64 {
			role := "user"
			if index%2 == 1 {
				role = "assistant"
			}
			request.Content = append(request.Content, protocol.Node{Kind: protocol.MessageNode, Role: protocol.StringValue(role), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue(strings.Repeat("h", 4<<10))}}})
		}
	case "tools32":
		for index := range 32 {
			request.Tools = append(request.Tools, protocol.Tool{Kind: protocol.FunctionTool, Name: protocol.StringValue(fmt.Sprintf("lookup_%d", index)), InputSchema: mustProtocolValue(t, `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)})
		}
	case "stream64k":
		request.Parameters["stream"] = mustProtocolValue(t, "true")
	}
	return request
}

func loadResponse(t *testing.T, compiled *protocol.Compiled) ([][]byte, []byte) {
	usage := &protocol.Usage{Input: &protocol.Counter{Count: 100, Origin: protocol.ObservedCount}, Output: &protocol.Counter{Count: 5, Origin: protocol.ObservedCount}, Total: &protocol.Counter{Count: 105, Origin: protocol.ObservedCount}, CacheRead: &protocol.Counter{Count: 70, Origin: protocol.ObservedCount}}
	response := &protocol.Response{SchemaVersion: 1, ID: protocol.StringValue("r"), Status: protocol.StringValue("completed"), Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("assistant"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("ok")}}}}, Usage: usage, Attributes: protocol.Object{"finishReason": protocol.StringValue("stop")}}
	body, err := compiled.EncodeResponse(t.Context(), response, protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	event := protocol.Event{SchemaVersion: 1, ItemID: protocol.StringValue("text"), Index: &index, Type: protocol.ItemStarted, Item: &protocol.Node{Kind: protocol.TextNode, Payload: protocol.StringValue("")}}
	events := []protocol.Event{event}
	for range loadStreamBytes / loadFrameBytes {
		events = append(events, protocol.Event{SchemaVersion: 1, ItemID: event.ItemID, Index: &index, Type: protocol.ItemDelta, Delta: protocol.StringValue(strings.Repeat("x", loadFrameBytes))})
	}
	events = append(events, protocol.Event{SchemaVersion: 1, ItemID: event.ItemID, Index: &index, Type: protocol.ItemFinished}, protocol.Event{SchemaVersion: 1, Type: protocol.ResponseFinished, Response: &protocol.Response{SchemaVersion: 1, Status: protocol.StringValue("completed"), Content: []protocol.Node{}, Attributes: response.Attributes}}, protocol.Event{SchemaVersion: 1, Type: protocol.UsageUpdated, Usage: usage})
	options := protocol.EvaluationContext{State: protocol.NewEvaluationState()}
	var frames [][]byte
	write := func(values []protocol.Value, err error) {
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			var buffer bytes.Buffer
			if err := protocol.WriteFrame(&buffer, compiled.Operations()["stream"], value); err != nil {
				t.Fatal(err)
			}
			frames = append(frames, buffer.Bytes())
		}
	}
	for _, event := range events {
		write(compiled.EncodeFrames(t.Context(), event, options))
	}
	write(compiled.FinishEvents(t.Context(), options))
	return frames, body
}

func measureGatewayLoad(t *testing.T, server *Server, client *http.Client, url string, wire []byte, count, concurrency int) loadMeasurement {
	t.Helper()
	runtime.GC()
	var before, after, retained runtime.MemStats
	runtime.ReadMemStats(&before)
	cpuBefore, err := loadCPUTime()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	latency, firstFrame := make([]int64, count), make([]int64, count)
	var next, failures atomic.Int64
	var reportError sync.Once
	var workers sync.WaitGroup
	for range concurrency {
		workers.Go(func() {
			for {
				index := int(next.Add(1)) - 1
				if index >= count {
					return
				}
				request, err := http.NewRequestWithContext(t.Context(), "POST", url, bytes.NewReader(wire))
				if err != nil {
					failures.Add(1)
					continue
				}
				request.Header.Set("Authorization", "Bearer synthetic-local-load")
				request.Header.Set("Content-Type", "application/json")
				start := time.Now()
				response, err := client.Do(request)
				if err != nil {
					reportError.Do(func() { t.Log("load HTTP failure:", err) })
					failures.Add(1)
					continue
				}
				if response.StatusCode != 200 {
					failure, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
					reportError.Do(func() { t.Logf("load response %d: %s", response.StatusCode, failure) })
				}
				reader := bufio.NewReader(response.Body)
				firstErr := readLoadFrame(reader, response.Header.Get("Content-Type"))
				firstFrame[index] = time.Since(start).Nanoseconds()
				_, readErr := io.Copy(io.Discard, reader)
				response.Body.Close()
				latency[index] = time.Since(start).Nanoseconds()
				if firstErr != nil || readErr != nil || response.StatusCode != 200 || response.Trailer.Get(gatewayStreamErrorTrailer) != "" {
					failures.Add(1)
				}
			}
		})
	}
	workers.Wait()
	server.stopUsageWriter()
	elapsed := time.Since(started)
	if elapsed <= 0 {
		t.Fatal("measurement below clock resolution; increase ELYSIA_LOAD_REQUESTS")
	}
	cpuAfter, err := loadCPUTime()
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	runtime.GC()
	runtime.ReadMemStats(&retained)
	return loadMeasurement{Requests: count, ElapsedNS: elapsed.Nanoseconds(), CPUPerRequestNS: float64(cpuAfter-cpuBefore) / float64(count), Throughput: float64(count) / elapsed.Seconds(), AllocatedPerRequest: float64(after.TotalAlloc-before.TotalAlloc) / float64(count), AllocationsPerRequest: float64(after.Mallocs-before.Mallocs) / float64(count), GC: after.NumGC - before.NumGC, GCPauseNS: after.PauseTotalNs - before.PauseTotalNs, RetainedHeapDelta: int64(retained.HeapAlloc) - int64(before.HeapAlloc), LatencyNS: loadPercentiles(latency), FirstFrameNS: loadPercentiles(firstFrame), Failures: failures.Load()}
}

// readLoadFrame measures a complete first frame, not the first response byte.
// A non-streaming JSON response has one frame: its complete body.
func readLoadFrame(reader *bufio.Reader, contentType string) error {
	if !strings.Contains(contentType, "text/event-stream") {
		_, err := io.Copy(io.Discard, reader)
		return err
	}
	hasContent := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if strings.TrimSpace(line) == "" {
			if hasContent {
				return nil
			}
		} else {
			hasContent = true
		}
	}
}

func loadPercentiles(values []int64) [3]int64 {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return [3]int64{values[(len(values)-1)*50/100], values[(len(values)-1)*95/100], values[(len(values)-1)*99/100]}
}
