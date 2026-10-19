package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

const flameToken = "synthetic-local-profile"

func validFlameHistory(messages []json.RawMessage, workload string) bool {
	if len(messages) != 1 {
		return false
	}
	var message struct {
		Role    string
		Content []struct{ Type, Text string }
	}
	if json.Unmarshal(messages[0], &message) != nil || message.Role != "user" {
		return false
	}
	count, width := 1, 1024
	if workload == "nodes" {
		count, width = 1800, (2<<20)/1800
	}
	if len(message.Content) != count {
		return false
	}
	text := strings.Repeat("x", width)
	for _, block := range message.Content {
		if block.Type != "text" || block.Text != text {
			return false
		}
	}
	return true
}

// The optional harness uses the normal authenticated routes. All credentials,
// providers and databases are local fixtures; no production config is loaded.
func configureFlameGateway(t *testing.T, server *Server) {
	t.Helper()
	server.config.SetEnablePprof(true)
	server.config.SetPanelAccessToken(flameToken)
	if err := server.store.ImportLegacyConfig(t.Context(), []storage.APIToken{{Name: "profile", Token: flameToken, Enabled: true, AllowedGroups: []string{"grp"}}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	server.setupRoutes()
}

func flameRequestBody(size int, stream bool, workload string) string {
	if workload == "image" {
		return fmt.Sprintf(`{"model":"grp","stream":%t,"max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"%s"}}]}]}]}`, stream, strings.Repeat("A", size<<20))
	}
	count, width := 1, 1024
	if workload == "nodes" {
		count, width = 1800, (2<<20)/1800
	}
	blocks := make([]map[string]string, count)
	for index := range blocks {
		blocks[index] = map[string]string{"type": "text", "text": strings.Repeat("x", width)}
	}
	body, _ := json.Marshal(map[string]any{"model": "grp", "stream": stream, "max_tokens": 2048, "messages": []any{map[string]any{"role": "user", "content": blocks}}})
	return string(body)
}

type flameMeasurement struct {
	Mode, Workload                     string
	SizeMiB, Workers, Repeat, Requests int
	Stream                             bool
	ElapsedMS                          float64
	Metrics                            conversionHTTPMetrics
}

// TestConversionFlamegraph measures only the isolated gateway child. CPU
// collection and memory diagnostics are intentionally separate from timing.
func TestConversionFlamegraph(t *testing.T) {
	workload := os.Getenv("ELYSIA_FLAME_WORKLOAD")
	if workload == "" {
		t.Skip("opt-in isolated profiling")
	}
	if workload != "image" && workload != "nodes" && workload != "events" && workload != "small" {
		t.Fatal("invalid workload")
	}
	mode, directory := os.Getenv("ELYSIA_FLAME_MODE"), os.Getenv("ELYSIA_FLAME_OUTPUT")
	if mode != "cpu" && mode != "memory" && mode != "measure" {
		t.Fatal("invalid mode")
	}
	if directory == "" {
		t.Fatal("output directory required")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	size := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_MIB", 2, 31)
	workers := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_WORKERS", 1, 32)
	seconds := conversionHTTPInt(t, "ELYSIA_FLAME_SECONDS", 60, 180)
	provider := startConversionHTTPChild(t, "provider", "")
	gateway := startConversionHTTPChild(t, "gateway", provider.URL)
	client := &http.Client{Timeout: 5 * time.Minute}
	t.Cleanup(client.CloseIdleConnections)
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
	saveProfile := func(name, endpoint string) error {
		request, _ := http.NewRequest("GET", gateway.URL+"/debug/pprof/"+endpoint, nil)
		request.Header.Set("Authorization", "Bearer "+flameToken)
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("profile HTTP %d", response.StatusCode)
		}
		file, err := os.Create(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		_, err = io.Copy(file, response.Body)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	var measurements []flameMeasurement
	for _, stream := range []bool{false, true} {
		if workload == "events" && !stream {
			continue
		}
		if mode != "measure" && stream != (workload == "events") {
			continue
		}
		body := flameRequestBody(size, stream, workload)
		path := "/v1/messages"
		var matrixSource *protocol.Compiled
		var verificationSource *protocol.Compiled
		if os.Getenv("ELYSIA_FLAME_VERIFY_EXACT") == "1" {
			verificationSource = flameMatrixProtocol(t, "anthropic-messages")
		}
		if sourceID := os.Getenv("ELYSIA_FLAME_SOURCE"); sourceID != "" {
			matrixSource = flameMatrixProtocol(t, sourceID)
			body, path = flameMatrixRequest(t, matrixSource, stream)
		}
		call := func() error {
			request, _ := http.NewRequest("POST", gateway.URL+path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+flameToken)
			response, err := client.Do(request)
			if err != nil {
				return err
			}
			output, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				return err
			}
			if matrixSource != nil {
				if response.StatusCode != 200 || response.Trailer.Get(gatewayStreamErrorTrailer) != "" {
					return fmt.Errorf("matrix response %d: %.400s", response.StatusCode, output)
				}
				if err := validateFlameMatrixOutput(t.Context(), matrixSource, output, stream); err != nil {
					_ = os.WriteFile(filepath.Join(directory, "failed-client-output.txt"), output, 0600)
					return err
				}
				return nil
			}
			if response.StatusCode != 200 || !bytes.Contains(output, []byte("complete")) {
				return fmt.Errorf("incomplete response %d: %.400s", response.StatusCode, output)
			}
			if stream && (!bytes.Contains(output, []byte("message_stop")) || response.Trailer.Get(gatewayStreamErrorTrailer) != "") {
				return fmt.Errorf("incomplete SSE")
			}
			if !bytes.Contains(output, []byte("input_tokens")) {
				return fmt.Errorf("usage missing")
			}
			if verificationSource != nil {
				expected := "complete"
				if workload == "events" {
					expected = strings.Repeat("chunk", 512) + expected
				}
				return validateFlameOutput(t.Context(), verificationSource, output, stream, expected, 10, 1)
			}
			return nil
		}
		if err := call(); err != nil {
			t.Fatal(err)
		}
		// Check the normal authentication boundary before any profiling.
		response, err := client.Get(gateway.URL + "/debug/pprof/heap")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("unauthenticated pprof accessible")
		}
		repeats := 1
		if mode == "measure" {
			repeats = 5
		}
		for repeat := 0; repeat < repeats; repeat++ {
			metrics(gateway.Metrics, "drain")
			// Formal timings keep the natural GC history of this gateway.
			// The older diagnostic reset explicitly collects twice; use it
			// only for the separate CPU/allocation diagnostic runs.
			reset := "reset"
			if mode == "measure" {
				reset = "reset-without-gc"
			}
			metrics(gateway.Metrics, reset)
			before := metrics(provider.Metrics, "")
			var profileDone chan error
			if mode == "cpu" {
				profileDone = make(chan error, 1)
				go func() { profileDone <- saveProfile("cpu.pprof", fmt.Sprintf("profile?seconds=%d", seconds)) }()
			} else if mode == "memory" {
				if err := saveProfile("alloc-before.pprof", "allocs"); err != nil {
					t.Fatal(err)
				}
				profileDone = make(chan error, 1)
				go func() { time.Sleep(250 * time.Millisecond); profileDone <- saveProfile("heap-active.pprof", "heap") }()
			}
			started := time.Now()
			requests := 0
			for {
				var group sync.WaitGroup
				failures := make(chan error, workers)
				for range workers {
					group.Go(func() {
						if err := call(); err != nil {
							failures <- err
						}
					})
				}
				group.Wait()
				close(failures)
				for err := range failures {
					t.Fatal(err)
				}
				requests += workers
				if mode != "cpu" || time.Since(started) >= time.Duration(seconds+1)*time.Second {
					break
				}
			}
			elapsed := time.Since(started)
			if profileDone != nil {
				if err := <-profileDone; err != nil {
					t.Fatal(err)
				}
			}
			sample := metrics(gateway.Metrics, "drain")
			if mode == "memory" {
				// Heap profiles can lag by two GC cycles. Only the memory run
				// forces collection, after all measured requests and logs drain.
				if err := saveProfile("heap-drained.pprof", "heap?gc=1"); err != nil {
					t.Fatal(err)
				}
				if err := saveProfile("heap-drained.pprof", "heap?gc=1"); err != nil {
					t.Fatal(err)
				}
				if err := saveProfile("alloc-after.pprof", "allocs"); err != nil {
					t.Fatal(err)
				}
			}
			after := metrics(provider.Metrics, "")
			if after.Calls-before.Calls != int64(requests) {
				t.Fatal("duplicate or missing generations")
			}
			measurements = append(measurements, flameMeasurement{Mode: mode, Workload: workload, SizeMiB: size, Workers: workers, Repeat: repeat, Requests: requests, Stream: stream, ElapsedMS: float64(elapsed) / float64(time.Millisecond), Metrics: sample})
			t.Logf("mode=%s stream=%t repeat=%d requests=%d elapsed_ms=%.3f heap_peak_MiB=%.3f", mode, stream, repeat, requests, float64(elapsed)/float64(time.Millisecond), float64(sample.PeakHeap)/(1<<20))
		}
	}
	raw, _ := json.MarshalIndent(measurements, "", "  ")
	if err := os.WriteFile(filepath.Join(directory, "measurements.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
