package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/config"
)

type conversionHTTPAddress struct {
	URL, Metrics string
	PID          int
}
type conversionHTTPMetrics struct {
	Allocated, Heap, PeakHeap, WorkingSet, PeakWorkingSet uint64
	Calls                                                 int64
	Allocations, GCPauseNS                                uint64
	GCCycles                                              uint32
	CPUTimeNS                                             int64
	GCCPUSeconds                                          float64
	Goroutines, ActiveRequests, QueueDepth                int
	QueuedPayloadBytes                                    int64
	Timeline                                              []conversionMemorySample `json:",omitempty"`
	DroppedSamples                                        int
}

type conversionMemorySample struct {
	ElapsedMS                                    float64
	Heap, HeapInuse, HeapIdle, HeapReleased, RSS uint64
	Goroutines, ActiveRequests                   int
}

func conversionGCCPUSeconds() float64 {
	samples := []metrics.Sample{{Name: "/cpu/classes/gc/total:cpu-seconds"}}
	metrics.Read(samples)
	return samples[0].Value.Float64()
}

// The load generator, gateway and mock provider run in different processes so
// gateway heap/working set never includes the 31 MiB client or provider buffers.
func TestConversionHTTPIsolated(t *testing.T) {
	if os.Getenv("ELYSIA_CONVERSION_HTTP") != "1" {
		t.Skip("opt-in isolated local HTTP performance")
	}
	size := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_MIB", 31, 31)
	workers := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_WORKERS", 1, 32)
	repeats := 5
	if text := os.Getenv("ELYSIA_CONVERSION_HTTP_REPEATS"); text != "" {
		var err error
		repeats, err = strconv.Atoi(text)
		if err != nil || repeats < 1 {
			t.Fatal("invalid repeats")
		}
	}
	provider := startConversionHTTPChild(t, "provider", "")
	gateway := startConversionHTTPChild(t, "gateway", provider.URL)
	client := &http.Client{Timeout: 5 * time.Minute}
	metrics := func(address, action string) conversionHTTPMetrics {
		response, err := client.Get(address + "?action=" + action)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result conversionHTTPMetrics
		if err := sonic.ConfigStd.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for pair := range repeats {
		for _, stream := range []bool{false, true} {
			body := fmt.Sprintf(`{"model":"grp","stream":%t,"max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"%s"}}]}]}]}`, stream, strings.Repeat("A", size<<20))
			before := metrics(provider.Metrics, "")
			metrics(gateway.Metrics, "reset")
			started := time.Now()

			var group sync.WaitGroup
			for range workers {
				group.Go(func() {
					request, _ := http.NewRequest("POST", gateway.URL+"/v1/messages", io.NopCloser(strings.NewReader(body)))
					request.Header.Set("Content-Type", "application/json")
					response, err := client.Do(request)
					if err != nil {
						t.Error(err)
						return
					}
					output, err := io.ReadAll(response.Body)
					response.Body.Close()
					if err != nil || response.StatusCode != http.StatusOK || !bytes.Contains(output, []byte("complete")) {
						t.Errorf("incomplete HTTP response: status=%d error=%v", response.StatusCode, err)
						return
					}
					if stream && (!bytes.Contains(output, []byte("message_stop")) || response.Trailer.Get(gatewayStreamErrorTrailer) != "") {
						t.Error("incomplete SSE")
					}
				})
			}
			group.Wait()
			elapsed := time.Since(started)
			if t.Failed() {
				return
			}

			after := metrics(provider.Metrics, "")
			if after.Calls != before.Calls+int64(workers) {
				t.Fatal("unexpected upstream generation count", after.Calls-before.Calls)
			}
			sample := metrics(gateway.Metrics, "drain")
			reclaimed := metrics(gateway.Metrics, "reclaim")
			t.Logf("pair=%d stream=%t size_MiB=%d workers=%d elapsed_ms=%.3f gateway_allocated_MiB=%.3f gateway_peak_heap_MiB=%.3f gateway_peak_working_set_MiB=%.3f gateway_reclaimed_heap_MiB=%.3f upstream_calls=%d", pair, stream, size, workers, float64(elapsed)/float64(time.Millisecond), float64(sample.Allocated)/(1<<20), float64(sample.PeakHeap)/(1<<20), float64(sample.PeakWorkingSet)/(1<<20), float64(reclaimed.Heap)/(1<<20), workers)
		}
	}
}

func startConversionHTTPChild(t *testing.T, role, upstream string) conversionHTTPAddress {
	t.Helper()
	directory := t.TempDir()
	ready := filepath.Join(directory, "ready.json")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-test.run=^TestConversionHTTPProcess$", "-test.timeout=40m")
	command.Env = append(os.Environ(), "ELYSIA_CONVERSION_HTTP_ROLE="+role, "ELYSIA_CONVERSION_HTTP_READY="+ready, "ELYSIA_CONVERSION_HTTP_UPSTREAM="+upstream)
	log, err := os.Create(filepath.Join(directory, "child.log"))
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = log, log
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		input.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s child: %v", role, err)
			}
		case <-time.After(30 * time.Second):
			command.Process.Kill()
			<-done
			t.Errorf("%s child failed to stop", role)
		}
		log.Close()
	})
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(ready); err == nil {
			var address conversionHTTPAddress
			if sonic.ConfigStd.Unmarshal(raw, &address) == nil {
				return address
			}
		}
		time.Sleep(20 * time.Millisecond)
		select {
		case err := <-done:
			done <- err // Cleanup must still observe the child exit.
			output, _ := os.ReadFile(filepath.Join(directory, "child.log"))
			t.Fatalf("%s child exited before ready: %v\n%.8192s", role, err, output)
		default:
		}
	}
	t.Fatal("child did not publish local address", role)
	return conversionHTTPAddress{}
}

func TestConversionHTTPProcess(t *testing.T) {
	role := os.Getenv("ELYSIA_CONVERSION_HTTP_ROLE")
	if role == "" {
		t.Skip("subprocess helper")
	}
	size := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_MIB", 31, 31)
	candidates := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_CANDIDATES", 1, 16)
	var active atomic.Int64
	var drain func()
	var settle func() error
	var reload func(context.Context) error
	var usageStats func() (int, int64)
	var completed atomic.Int64
	var calls atomic.Int64
	var gateway *httptest.Server
	if role == "provider" {
		if targetID := os.Getenv("ELYSIA_FLAME_TARGET"); targetID != "" {
			handler := flameMatrixProvider(t, flameMatrixProtocol(t, targetID))
			gateway = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); handler.ServeHTTP(w, r) }))
		} else {
			gateway = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var input struct {
					Model    string
					Stream   bool
					Messages []json.RawMessage
				}
				if err := sonic.ConfigStd.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				isCandidateModel := input.Model == "m"
				for index := 1; index < candidates; index++ {
					isCandidateModel = isCandidateModel || input.Model == fmt.Sprintf("m%d", index)
				}
				isHistoryValid := len(input.Messages) == 2 && bytes.Contains(input.Messages[1], []byte(strings.Repeat("A", size<<20)))
				if workload := os.Getenv("ELYSIA_FLAME_WORKLOAD"); workload != "" && workload != "image" {
					isHistoryValid = validFlameHistory(input.Messages, workload)
				}
				if !isCandidateModel || !isHistoryValid {
					t.Error("tool/image history changed")
					w.WriteHeader(500)
					return
				}
				if !input.Stream {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"id":"msg_1","type":"message","role":"assistant","model":%q,"content":[{"type":"text","text":"complete"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}`, input.Model)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				frames := []string{
					fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":%q,"content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}`, input.Model),
					`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
					`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"complete"}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
					`{"type":"message_stop"}`,
				}
				if os.Getenv("ELYSIA_FLAME_WORKLOAD") == "events" {
					prefix := append([]string(nil), frames[:2]...)
					for range 512 {
						prefix = append(prefix, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"chunk"}}`)
					}
					frames = append(prefix, frames[2:]...)
				}
				for _, frame := range frames {
					fmt.Fprintf(w, "data: %s\n\n", frame)
					if os.Getenv("ELYSIA_FLAME_WORKLOAD") == "events" {
						w.(http.Flusher).Flush()
					}
					if os.Getenv("ELYSIA_CONVERSION_HTTP_CANCEL") == "1" && strings.Contains(frame, `"type":"content_block_delta"`) {
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						return
					}
				}
			}))
		}
	} else if role == "gateway" {
		group := config.ModelGroupConfig{ID: "g", Name: "grp", Enabled: true, Strategy: "round-robin", Models: []config.ModelRef{{ID: "m", Name: "m", BaseURL: os.Getenv("ELYSIA_CONVERSION_HTTP_UPSTREAM"), Platform: "claude", APIKey: "synthetic", ToolsCapable: true, VisionCapable: true}}}
		if targetID := os.Getenv("ELYSIA_FLAME_TARGET"); targetID != "" {
			group.Models[0].Platform = "custom:" + targetID
			group.Models[0].BaseURL = loadBase(group.Models[0].BaseURL, flameMatrixProtocol(t, targetID))
		}

		for index := 1; index < candidates; index++ {
			model := group.Models[0]
			model.ID = fmt.Sprintf("m%d", index)
			group.Models = append(group.Models, model)
		}
		server := newTestServerWithStore(t, []config.ModelGroupConfig{group})
		mode := os.Getenv("ELYSIA_CONVERSION_HTTP_LOG")
		if mode != "" && mode != "off" && mode != "body" && mode != "media" {
			t.Fatal("unknown log mode")
		}
		bodyKB, externalize := 0, mode == "media"
		if mode == "body" || mode == "media" {
			bodyKB = 64 << 10
		}
		server.config.SetUsageLogConfig(config.UsageLogConfig{BodyMaxKB: &bodyKB, ExternalizeMedia: &externalize})
		server.startUsageWriter()
		t.Cleanup(server.stopUsageWriter)
		reload = func(ctx context.Context) error {
			if err := server.reloadProtocolRuntime(ctx); err != nil {
				return err
			}
			server.invalidateRouteCache()
			return nil
		}
		// Unlike drain, settle leaves the writer and its buffers alive. Memory
		// recovery measurements must observe the normal long-lived service.
		settle = func() error {
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				stats := server.usageWriterStats()
				if active.Load() == 0 && stats.Written >= uint64(completed.Load()) && stats.Queued == 0 && stats.RetainedBytes == 0 {
					return nil
				}
				if stats.Failures > 0 {
					return fmt.Errorf("usage persistence failed: %s", stats.LastError)
				}
				time.Sleep(time.Millisecond)
			}
			return fmt.Errorf("gateway requests or usage writes did not settle")
		}
		usageStats = func() (int, int64) {
			stats := server.usageWriterStats()
			return stats.Queued, stats.RetainedBytes
		}
		drain = func() {
			deadline := time.Now().Add(30 * time.Second)
			for active.Load() != 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if active.Load() != 0 {
				t.Error("gateway requests did not drain")
			}
			server.stopUsageWriter()
			server.startUsageWriter()
		}
		if os.Getenv("ELYSIA_FLAME_WORKLOAD") != "" {
			configureFlameGateway(t, server)
		} else {
			server.engine.POST("/v1/messages", server.chatCompletions)
		}
		gateway = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/debug/pprof/") {
				server.engine.ServeHTTP(w, r)
				return
			}
			active.Add(1)
			defer func() { completed.Add(1); active.Add(-1) }()
			server.engine.ServeHTTP(w, r)
		}))

	} else {
		t.Fatal("unknown child role")
	}
	defer gateway.Close()
	var mutex sync.Mutex
	var startAlloc, peakHeap, peakWorkingSet uint64
	var startMallocs, startPause uint64
	var startGC uint32
	var startCPU time.Duration
	var startGCCPU float64
	var timeline []conversionMemorySample
	var droppedSamples int
	started := time.Now()
	collectTimeline := os.Getenv("ELYSIA_FLAME_TIMELINE") == "1"
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				sampledAt := time.Now()
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				working := conversionWorkingSetBytes()
				mutex.Lock()
				if sampledAt.Before(started) {
					mutex.Unlock()
					continue // A reset overtook this sample; do not mix windows.
				}
				peakHeap = max(peakHeap, memory.HeapAlloc)
				peakWorkingSet = max(peakWorkingSet, working)
				if collectTimeline {
					if len(timeline) < 9000 {
						timeline = append(timeline, conversionMemorySample{ElapsedMS: float64(time.Since(started)) / float64(time.Millisecond), Heap: memory.HeapAlloc, HeapInuse: memory.HeapInuse, HeapIdle: memory.HeapIdle, HeapReleased: memory.HeapReleased, RSS: working, Goroutines: runtime.NumGoroutine(), ActiveRequests: int(active.Load())})
					} else {
						droppedSamples++
					}
				}
				mutex.Unlock()
			}
		}
	}()
	defer func() { close(stop); <-done }()
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("action")
		if action == "reload" && reload != nil {
			if err := reload(r.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if action == "settle" && settle != nil {
			if err := settle(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if (action == "drain" || action == "reclaim") && drain != nil {
			drain()
		}
		if action == "reset" || action == "reclaim" {
			runtime.GC()
			runtime.GC()
		}
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		working := conversionWorkingSetBytes()
		cpu, err := loadCPUTime()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		gcCPU := conversionGCCPUSeconds()
		mutex.Lock()
		if action == "reset" || action == "reset-without-gc" {
			startAlloc, peakHeap, peakWorkingSet = memory.TotalAlloc, memory.HeapAlloc, working
			startMallocs, startPause, startGC, startCPU, startGCCPU = memory.Mallocs, memory.PauseTotalNs, memory.NumGC, cpu, gcCPU
			started = time.Now()
			timeline, droppedSamples = nil, 0
		}
		result := conversionHTTPMetrics{Allocated: memory.TotalAlloc - startAlloc, Heap: memory.HeapAlloc, PeakHeap: max(peakHeap, memory.HeapAlloc), WorkingSet: working, PeakWorkingSet: max(peakWorkingSet, working), Calls: calls.Load()}
		result.Allocations, result.GCPauseNS, result.GCCycles = memory.Mallocs-startMallocs, memory.PauseTotalNs-startPause, memory.NumGC-startGC
		result.CPUTimeNS, result.GCCPUSeconds = int64(cpu-startCPU), gcCPU-startGCCPU
		result.Goroutines, result.ActiveRequests = runtime.NumGoroutine(), int(active.Load())
		if r.URL.Query().Get("timeline") == "1" {
			result.Timeline = append([]conversionMemorySample(nil), timeline...)
			result.DroppedSamples = droppedSamples
		}
		mutex.Unlock()
		if usageStats != nil {
			result.QueueDepth, result.QueuedPayloadBytes = usageStats()
		}
		sonic.ConfigStd.NewEncoder(w).Encode(result)
	}))
	defer metrics.Close()
	address, _ := sonic.ConfigStd.Marshal(conversionHTTPAddress{URL: gateway.URL, Metrics: metrics.URL, PID: os.Getpid()})
	if err := os.WriteFile(os.Getenv("ELYSIA_CONVERSION_HTTP_READY"), address, 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func conversionHTTPInt(t *testing.T, name string, fallback, maximum int) int {
	t.Helper()
	text := os.Getenv(name)
	if text == "" {
		return fallback
	}
	value, err := strconv.Atoi(text)
	if err != nil || value < 1 || value > maximum {
		t.Fatalf("invalid %s", name)
	}
	return value
}

// Cancellation retention is measured separately from successful-response
// timing. Each client waits for a real event before cancelling its own stream.
func TestConversionHTTPCancellationIsolated(t *testing.T) {
	if os.Getenv("ELYSIA_CONVERSION_HTTP_CANCEL") != "1" {
		t.Skip("opt-in isolated stream cancellation retention")
	}
	size := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_MIB", 2, 31)
	workers := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_WORKERS", 4, 32)
	provider := startConversionHTTPChild(t, "provider", "")
	gateway := startConversionHTTPChild(t, "gateway", provider.URL)
	client := &http.Client{Timeout: 2 * time.Minute}
	metrics := func(address, action string) conversionHTTPMetrics {
		response, err := client.Get(address + "?action=" + action)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result conversionHTTPMetrics
		if err := sonic.ConfigStd.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	body := fmt.Sprintf(`{"model":"grp","stream":true,"max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"%s"}}]}]}]}`, strings.Repeat("A", size<<20))
	for batch := range 5 {
		before := metrics(provider.Metrics, "")
		metrics(gateway.Metrics, "reset")
		var group sync.WaitGroup
		for range workers {
			group.Go(func() {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				request, err := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/messages", strings.NewReader(body))
				if err != nil {
					t.Error(err)
					return
				}
				request.Header.Set("Content-Type", "application/json")
				response, err := client.Do(request)
				if err != nil {
					t.Error(err)
					return
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					t.Errorf("unexpected cancellation response: %d", response.StatusCode)
					return
				}
				reader := bufio.NewReader(response.Body)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						t.Error("stream ended before cancellation", err)
						return
					}
					if strings.HasPrefix(line, "data:") {
						cancel()
						return
					}
				}
			})
		}
		group.Wait()
		if t.Failed() {
			return
		}
		sample := metrics(gateway.Metrics, "drain")
		reclaimed := metrics(gateway.Metrics, "reclaim")
		after := metrics(provider.Metrics, "")
		if after.Calls-before.Calls != int64(workers) {
			t.Fatal("cancellation caused repeated generation", after.Calls-before.Calls)
		}
		t.Logf("batch=%d size_MiB=%d workers=%d cancelled_calls=%d gateway_peak_heap_MiB=%.3f gateway_reclaimed_heap_MiB=%.3f", batch, size, workers, workers, float64(sample.PeakHeap)/(1<<20), float64(reclaimed.Heap)/(1<<20))
		if directory := os.Getenv("ELYSIA_CONVERSION_HTTP_OUTPUT"); directory != "" {
			raw, err := sonic.ConfigStd.MarshalIndent(struct {
				Workers, SizeMiB   int
				Settled, Reclaimed conversionHTTPMetrics
			}{workers, size, sample, reclaimed}, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("cancellation-%d.json", batch)), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
