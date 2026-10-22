package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
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
	if sonic.ConfigStd.Unmarshal(messages[0], &message) != nil || message.Role != "user" {
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
	configureReviewMapping(t, server)
	server.config.SetEnablePprof(true)
	server.config.SetPanelAccessToken(flameToken)
	if err := server.store.UpsertAPIToken(t.Context(), storage.APIToken{Name: "profile", Token: flameToken, Enabled: true, AllowedGroups: []string{"grp"}}); err != nil {
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
	body, _ := sonic.ConfigStd.Marshal(map[string]any{"model": "grp", "stream": stream, "max_tokens": 2048, "messages": []any{map[string]any{"role": "user", "content": blocks}}})
	return string(body)
}

type flameMeasurement struct {
	UpstreamCalls                      int64
	Reload                             *conversionHTTPMetrics `json:",omitempty"`
	ReloadMS                           float64                `json:",omitempty"`
	Mode, Workload                     string
	Phase                              string
	SizeMiB, Workers, Repeat, Requests int
	Stream                             bool
	ElapsedMS                          float64
	Metrics                            conversionHTTPMetrics
	Before                             conversionHTTPMetrics
	LatencyP50P95P99NS                 [3]int64
	FirstByteP50P95P99NS               [3]int64
	FirstFrameP50P95P99NS              [3]int64
	Observations                       []flameCallMeasurement
	Recovery                           []flameRecoverySample `json:",omitempty"`
}

type flameCallMeasurement struct {
	LatencyNS, FirstByteNS, FirstFrameNS int64
}

type flameRecoverySample struct {
	IdleMS  float64
	Metrics conversionHTTPMetrics
}

// Nearest-rank percentiles keep the maximum as P99 for small samples. Raw
// observations are retained; a small fixture run cannot establish a stable P99.
func flamePercentiles(values []int64) [3]int64 {
	slices.Sort(values)
	return [3]int64{values[(len(values)*50+99)/100-1], values[(len(values)*95+99)/100-1], values[(len(values)*99+99)/100-1]}
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
	requestCount := conversionHTTPInt(t, "ELYSIA_FLAME_REQUESTS", workers, 10000)
	warmup := conversionHTTPInt(t, "ELYSIA_FLAME_WARMUP", 1, 1000)
	recoveryMS := 0
	if os.Getenv("ELYSIA_FLAME_RECOVERY_MS") != "" {
		recoveryMS = conversionHTTPInt(t, "ELYSIA_FLAME_RECOVERY_MS", 1000, 60000)
	}
	streamSelection := os.Getenv("ELYSIA_FLAME_STREAM")
	if streamSelection != "" && streamSelection != "json" && streamSelection != "sse" {
		t.Fatal("invalid stream selection")
	}
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
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("metrics HTTP %d: %s", response.StatusCode, body)
		}
		var result conversionHTTPMetrics
		if err := sonic.ConfigStd.NewDecoder(response.Body).Decode(&result); err != nil {
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
		if streamSelection == "json" && stream || streamSelection == "sse" && !stream {
			continue
		}
		if workload == "events" && !stream {
			continue
		}
		if mode != "measure" && streamSelection == "" && stream != (workload == "events") {
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
		call := func() (observation flameCallMeasurement, result error) {
			request, _ := http.NewRequestWithContext(t.Context(), "POST", gateway.URL+path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+flameToken)
			started := time.Now()
			response, err := client.Do(request)
			if err != nil {
				return observation, err
			}
			defer response.Body.Close()
			var captured bytes.Buffer
			reader := bufio.NewReader(io.TeeReader(response.Body, &captured))
			_, err = reader.Peek(1)
			observation.FirstByteNS = time.Since(started).Nanoseconds()
			if err != nil {
				return observation, err
			}
			if err = readLoadFrame(reader, response.Header.Get("Content-Type")); err != nil {
				return observation, err
			}
			observation.FirstFrameNS = time.Since(started).Nanoseconds()
			if _, err = io.Copy(io.Discard, reader); err != nil {
				return observation, err
			}
			observation.LatencyNS = time.Since(started).Nanoseconds()
			output := captured.Bytes()
			if matrixSource != nil {
				if response.StatusCode != 200 || response.Trailer.Get(gatewayStreamErrorTrailer) != "" {
					return observation, fmt.Errorf("matrix response %d: %.400s", response.StatusCode, output)
				}
				if err := validateFlameMatrixOutput(t.Context(), matrixSource, output, stream); err != nil {
					_ = os.WriteFile(filepath.Join(directory, "failed-client-output.txt"), output, 0600)
					return observation, err
				}
				return observation, nil
			}
			if response.StatusCode != 200 || !bytes.Contains(output, []byte("complete")) {
				return observation, fmt.Errorf("incomplete response %d: %.400s", response.StatusCode, output)
			}
			if stream && (!bytes.Contains(output, []byte("message_stop")) || response.Trailer.Get(gatewayStreamErrorTrailer) != "") {
				return observation, fmt.Errorf("incomplete SSE")
			}
			if !bytes.Contains(output, []byte("input_tokens")) {
				return observation, fmt.Errorf("usage missing")
			}
			if verificationSource != nil {
				expected := "complete"
				if workload == "events" {
					expected = strings.Repeat("chunk", 512) + expected
				}
				return observation, validateFlameOutput(t.Context(), verificationSource, output, stream, expected, 10, 1)
			}
			return observation, nil
		}
		if mode == "measure" && os.Getenv("ELYSIA_FLAME_COLD") == "1" {
			before := metrics(gateway.Metrics, "reset-without-gc")
			providerBefore := metrics(provider.Metrics, "")
			started := time.Now()
			observation, err := call()
			elapsed := time.Since(started)
			if err != nil {
				t.Fatal(err)
			}
			after := metrics(gateway.Metrics, "settle")
			if metrics(provider.Metrics, "").Calls-providerBefore.Calls != 1 {
				t.Fatal("cold request did not produce exactly one generation")
			}
			measurements = append(measurements, flameMeasurement{Mode: mode, Workload: workload, Phase: "first-request", SizeMiB: size, Workers: 1, Requests: 1, Stream: stream, ElapsedMS: float64(elapsed) / float64(time.Millisecond), Before: before, Metrics: after, Observations: []flameCallMeasurement{observation}})
		}
		for range warmup {
			if _, err := call(); err != nil {
				t.Fatal(err)
			}
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
			repeats = conversionHTTPInt(t, "ELYSIA_FLAME_REPEATS", 5, 100)
		}
		for repeat := 0; repeat < repeats; repeat++ {
			metrics(gateway.Metrics, "settle")
			// Formal timings keep the natural GC history of this gateway.
			// The older diagnostic reset explicitly collects twice; use it
			// only for the separate CPU/allocation diagnostic runs.
			reset := "reset"
			if mode == "measure" {
				reset = "reset-without-gc"
			}
			initial := metrics(gateway.Metrics, reset)
			var reloaded *conversionHTTPMetrics
			var reloadMS float64
			if os.Getenv("ELYSIA_FLAME_RELOAD") == "1" {
				start := time.Now()
				snapshot := metrics(gateway.Metrics, "reload")
				reloaded, reloadMS = &snapshot, float64(time.Since(start))/float64(time.Millisecond)
				initial = metrics(gateway.Metrics, reset)
			}
			before := metrics(provider.Metrics, "")
			var profileDone chan error
			if mode == "cpu" {
				profileDone = make(chan error, 1)
				name, endpoint := "cpu.pprof", "profile"
				if os.Getenv("ELYSIA_REVIEW_TRACE") == "1" {
					name, endpoint = "execution.trace", "trace"
				}
				go func() { profileDone <- saveProfile(name, fmt.Sprintf("%s?seconds=%d", endpoint, seconds)) }()
			} else if mode == "memory" {
				if err := saveProfile("alloc-before.pprof", "allocs"); err != nil {
					t.Fatal(err)
				}
				profileDone = make(chan error, 1)
				go func() {
					// A fixed delay can photograph an already idle gateway. Record
					// activity on both sides of the snapshot instead of claiming peak.
					deadline := time.Now().Add(10 * time.Second)
					observe := func() (conversionHTTPMetrics, error) {
						var state conversionHTTPMetrics
						response, err := client.Get(gateway.Metrics)
						if err != nil {
							return state, err
						}
						defer response.Body.Close()
						if response.StatusCode != http.StatusOK {
							return state, fmt.Errorf("activity HTTP %d", response.StatusCode)
						}
						return state, sonic.ConfigStd.NewDecoder(response.Body).Decode(&state)
					}
					for time.Now().Before(deadline) {
						before, err := observe()
						if err != nil {
							profileDone <- err
							return
						}
						if before.ActiveRequests > 0 {
							err = saveProfile("heap-active.pprof", "heap")
							after, observationErr := observe()
							if err == nil {
								err = observationErr
							}
							raw, _ := sonic.ConfigStd.MarshalIndent(map[string]any{"before": before, "after": after, "activeOnBothSides": after.ActiveRequests > 0, "peakSnapshot": false}, "", "  ")
							_ = os.WriteFile(filepath.Join(directory, "heap-active-observation.json"), raw, 0600)
							profileDone <- err
							return
						}
						time.Sleep(time.Millisecond)
					}
					profileDone <- fmt.Errorf("no active request observed for heap snapshot")
				}()
			}
			started := time.Now()
			requests := 0
			var observations []flameCallMeasurement
			for {
				batch := make([]flameCallMeasurement, requestCount)
				var next atomic.Int64
				var group sync.WaitGroup
				start := make(chan struct{})
				failures := make(chan error, workers)
				for range workers {
					group.Go(func() {
						<-start
						for {
							index := int(next.Add(1)) - 1
							if index >= len(batch) {
								return
							}
							var err error
							batch[index], err = call()
							if err != nil {
								failures <- err
								return
							}
						}
					})
				}
				close(start)
				group.Wait()
				close(failures)
				for err := range failures {
					t.Fatal(err)
				}
				requests += len(batch)
				observations = append(observations, batch...)
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
			sample := metrics(gateway.Metrics, "settle")
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
			measurement := flameMeasurement{UpstreamCalls: after.Calls - before.Calls, Mode: mode, Workload: workload, Phase: "steady", SizeMiB: size, Workers: workers, Repeat: repeat, Requests: requests, Stream: stream, ElapsedMS: float64(elapsed) / float64(time.Millisecond), Before: initial, Metrics: sample, Observations: observations}
			if workers > 1 {
				measurement.Phase = "burst"
			}
			measurement.Reload, measurement.ReloadMS = reloaded, reloadMS
			latency, firstByte, firstFrame := make([]int64, requests), make([]int64, requests), make([]int64, requests)
			for i, observation := range observations {
				latency[i], firstByte[i], firstFrame[i] = observation.LatencyNS, observation.FirstByteNS, observation.FirstFrameNS
			}
			measurement.LatencyP50P95P99NS, measurement.FirstByteP50P95P99NS, measurement.FirstFrameP50P95P99NS = flamePercentiles(latency), flamePercentiles(firstByte), flamePercentiles(firstFrame)
			if mode == "measure" && recoveryMS > 0 {
				idle := time.Now()
				for _, fraction := range []int{0, 1, 5, 10} {
					time.Sleep(max(0, time.Duration(recoveryMS*fraction/10)*time.Millisecond-time.Since(idle)))
					point := metrics(gateway.Metrics, "")
					measurement.Recovery = append(measurement.Recovery, flameRecoverySample{IdleMS: float64(time.Since(idle)) / float64(time.Millisecond), Metrics: point})
				}
				// Retrieve the bounded sampler history only after allocation/latency
				// measurement, so serializing the history cannot inflate that result.
				history := metrics(gateway.Metrics, "&timeline=1")
				measurement.Metrics.Timeline, measurement.Metrics.DroppedSamples = history.Timeline, history.DroppedSamples
			}
			measurements = append(measurements, measurement)
			t.Logf("mode=%s stream=%t repeat=%d requests=%d elapsed_ms=%.3f heap_peak_MiB=%.3f", mode, stream, repeat, requests, float64(elapsed)/float64(time.Millisecond), float64(sample.PeakHeap)/(1<<20))
		}
	}
	raw, _ := sonic.ConfigStd.MarshalIndent(measurements, "", "  ")
	if err := os.WriteFile(filepath.Join(directory, "measurements.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
