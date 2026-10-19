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
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
)

type conversionHTTPAddress struct {
	URL, Metrics string
	PID          int
}
type conversionHTTPMetrics struct {
	Allocated, Heap, PeakHeap, WorkingSet, PeakWorkingSet uint64
	Calls                                                 int64
}

// The load generator, gateway and mock provider run in different processes so
// gateway heap/working set never includes the 31 MiB client or provider buffers.
func TestConversionHTTPIsolated(t *testing.T) {
	if os.Getenv("ELYSIA_CONVERSION_HTTP") != "1" {
		t.Skip("opt-in isolated local HTTP performance")
	}
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
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for pair := range repeats {
		for _, stream := range []bool{false, true} {
			body := fmt.Sprintf(`{"model":"grp","stream":%t,"max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"%s"}}]}]}]}`, stream, strings.Repeat("A", 31<<20))
			before := metrics(provider.Metrics, "")
			metrics(gateway.Metrics, "reset")
			started := time.Now()
			request, _ := http.NewRequest("POST", gateway.URL+"/v1/messages", io.NopCloser(strings.NewReader(body)))
			request.Header.Set("Content-Type", "application/json")
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			output, err := io.ReadAll(response.Body)
			response.Body.Close()
			elapsed := time.Since(started)
			if err != nil || response.StatusCode != 200 || !bytes.Contains(output, []byte("complete")) {
				t.Fatalf("incomplete HTTP response: status=%d error=%v", response.StatusCode, err)
			}
			if stream && (!bytes.Contains(output, []byte("message_stop")) || response.Trailer.Get(gatewayStreamErrorTrailer) != "") {
				t.Fatal("incomplete SSE")
			}
			after := metrics(provider.Metrics, "")
			if after.Calls != before.Calls+1 {
				t.Fatal("unexpected upstream generation count", after.Calls-before.Calls)
			}
			sample := metrics(gateway.Metrics, "")
			reclaimed := metrics(gateway.Metrics, "reclaim")
			t.Logf("pair=%d stream=%t elapsed_ms=%.3f gateway_allocated_MiB=%.3f gateway_peak_heap_MiB=%.3f gateway_peak_working_set_MiB=%.3f gateway_reclaimed_heap_MiB=%.3f upstream_calls=1", pair, stream, float64(elapsed)/float64(time.Millisecond), float64(sample.Allocated)/(1<<20), float64(sample.PeakHeap)/(1<<20), float64(sample.PeakWorkingSet)/(1<<20), float64(reclaimed.Heap)/(1<<20))
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
			if json.Unmarshal(raw, &address) == nil {
				return address
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("child did not publish local address", role)
	return conversionHTTPAddress{}
}

func TestConversionHTTPProcess(t *testing.T) {
	role := os.Getenv("ELYSIA_CONVERSION_HTTP_ROLE")
	if role == "" {
		t.Skip("subprocess helper")
	}
	var calls atomic.Int64
	var gateway *httptest.Server
	if role == "provider" {
		gateway = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			var input struct {
				Model    string
				Stream   bool
				Messages []json.RawMessage
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			if input.Model != "m" || len(input.Messages) != 2 || !bytes.Contains(input.Messages[1], []byte(strings.Repeat("A", 31<<20))) {
				t.Error("tool/image history changed")
				w.WriteHeader(500)
				return
			}
			if !input.Stream {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"complete"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, frame := range []string{
				`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"complete"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
				`{"type":"message_stop"}`,
			} {
				fmt.Fprintf(w, "data: %s\n\n", frame)
			}
		}))
	} else if role == "gateway" {
		group := config.ModelGroupConfig{ID: "g", Name: "grp", Enabled: true, Strategy: "round-robin", Models: []config.ModelRef{{ID: "m", Name: "m", BaseURL: os.Getenv("ELYSIA_CONVERSION_HTTP_UPSTREAM"), Platform: "claude", APIKey: "synthetic", ToolsCapable: true, VisionCapable: true}}}
		server := newTestServerWithStore(t, []config.ModelGroupConfig{group})
		server.engine.POST("/v1/messages", server.chatCompletions)
		gateway = httptest.NewServer(server.engine)
	} else {
		t.Fatal("unknown child role")
	}
	defer gateway.Close()
	var mutex sync.Mutex
	var startAlloc, peakHeap, peakWorkingSet uint64
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
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				working := conversionWorkingSetBytes()
				mutex.Lock()
				peakHeap = max(peakHeap, memory.HeapAlloc)
				peakWorkingSet = max(peakWorkingSet, working)
				mutex.Unlock()
			}
		}
	}()
	defer func() { close(stop); <-done }()
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("action")
		if action == "reset" || action == "reclaim" {
			runtime.GC()
			runtime.GC()
		}
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		working := conversionWorkingSetBytes()
		mutex.Lock()
		if action == "reset" {
			startAlloc, peakHeap, peakWorkingSet = memory.TotalAlloc, memory.HeapAlloc, working
		}
		result := conversionHTTPMetrics{Allocated: memory.TotalAlloc - startAlloc, Heap: memory.HeapAlloc, PeakHeap: max(peakHeap, memory.HeapAlloc), WorkingSet: working, PeakWorkingSet: max(peakWorkingSet, working), Calls: calls.Load()}
		mutex.Unlock()
		json.NewEncoder(w).Encode(result)
	}))
	defer metrics.Close()
	address, _ := json.Marshal(conversionHTTPAddress{URL: gateway.URL, Metrics: metrics.URL, PID: os.Getpid()})
	if err := os.WriteFile(os.Getenv("ELYSIA_CONVERSION_HTTP_READY"), address, 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
