package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This test driver also compiles unchanged against the previous implementation.
// Separate processes keep client/provider request buffers out of gateway heap.
// Forced GC while streams are held is retention diagnosis, never throughput.
func TestGatewayHeldStreamRetention(t *testing.T) {
	if os.Getenv("ELYSIA_HELD_STREAM_PROFILE") != "1" {
		t.Skip("opt-in retained preparation diagnosis")
	}
	provider := startConversionHTTPChild(t, "provider", "")
	gateway := startConversionHTTPChild(t, "gateway", provider.URL)
	client := &http.Client{Timeout: 2 * time.Minute}
	metrics := func(action string) conversionHTTPMetrics {
		r, err := client.Get(gateway.Metrics + "?action=" + action)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var m conversionHTTPMetrics
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	size := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_MIB", 2, 31)
	workers := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_WORKERS", 4, 8)
	body := fmt.Sprintf(`{"model":"grp","stream":true,"max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"%s"}}]}]}]}`, strings.Repeat("A", size<<20))
	for batch := range 3 {
		baseline := metrics("reset")
		var cancels []context.CancelFunc
		var responses []*http.Response
		for range workers {
			ctx, cancel := context.WithCancel(t.Context())
			cancels = append(cancels, cancel)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/messages", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			responses = append(responses, response)
			if response.StatusCode != 200 {
				t.Fatal(response.Status)
			}
			reader := bufio.NewReader(response.Body)
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
		held := metrics("reset") // Two GCs, no drain: every stream remains active.
		if held.ActiveRequests != workers || held.Preparation.Active != 0 || held.Preparation.ReservedBodyBytes != 0 {
			t.Fatal("did not hold completed preparation", held)
		}
		for index, cancel := range cancels {
			cancel()
			responses[index].Body.Close()
		}
		settled := metrics("reclaim")
		if settled.ActiveRequests != 0 || settled.QueueDepth != 0 || settled.QueuedPayloadBytes != 0 {
			t.Fatal("requests did not drain", settled)
		}
		result := struct {
			Baseline, Held, Settled conversionHTTPMetrics
			Workers, SizeMiB        int
		}{baseline, held, settled, workers, size}
		t.Logf("batch=%d workers=%d size_MiB=%d held_heap_MiB=%.3f held_RSS_MiB=%.3f settled_heap_MiB=%.3f", batch, workers, size, float64(held.Heap)/(1<<20), float64(held.WorkingSet)/(1<<20), float64(settled.Heap)/(1<<20))
		if out := os.Getenv("ELYSIA_CONVERSION_HTTP_OUTPUT"); out != "" {
			raw, _ := json.MarshalIndent(result, "", "  ")
			if err := os.WriteFile(filepath.Join(out, fmt.Sprintf("held-%d.json", batch)), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
