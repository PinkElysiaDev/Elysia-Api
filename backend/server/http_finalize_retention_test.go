package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

const finalizeRetentionBodyMaxKB = 64 << 10

type heldFinalizeWriter struct {
	network.ExtWriter
	ctx     context.Context
	entered chan<- struct{}
	release <-chan struct{}
}

func (writer *heldFinalizeWriter) Finalize() error {
	writer.entered <- struct{}{}
	select {
	case <-writer.release:
		return writer.ExtWriter.Finalize()
	case <-writer.ctx.Done():
		return writer.ctx.Err()
	}
}

type finalizeRetentionSample struct {
	Batch            int    `json:"batch"`
	Phase            string `json:"phase"`
	ForcedGC         bool   `json:"forcedGC"`
	Heap             uint64 `json:"heap"`
	HeapInuse        uint64 `json:"heapInuse"`
	Allocated        uint64 `json:"allocated"`
	Goroutines       int    `json:"goroutines"`
	ActiveRequests   int    `json:"activeRequests"`
	QueueBytes       int64  `json:"queueBytes"`
	PreparedLogBytes int64  `json:"preparedLogBytes"`
	CaptureBytes     int    `json:"captureBytes"`
}

// This diagnostic deliberately runs client and HTTP capture in one process.
// It isolates ownership across Finalize; it is not a gateway-only peak or a
// formal latency benchmark. The ordinary writer gate also works in the baseline.
func TestHTTPUsageSlowFinalizeRetention(t *testing.T) {
	if os.Getenv("ELYSIA_HTTP_FINALIZE_RETENTION") != "1" {
		t.Skip("opt-in finalization ownership diagnostic")
	}
	size := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_MIB", 2, 31)
	workers := conversionHTTPInt(t, "ELYSIA_CONVERSION_HTTP_WORKERS", 1, 4)
	shouldCancel := os.Getenv("ELYSIA_HTTP_FINALIZE_CANCEL") == "1"
	mode := os.Getenv("ELYSIA_HTTP_FINALIZE_LOG")
	if mode == "" {
		mode = "off"
	}
	if mode != "off" && mode != "body" && mode != "media" {
		t.Fatal("invalid log mode")
	}
	server, _ := newProtocolAdminTestServer(t)
	limit := 0
	if mode != "off" {
		limit = finalizeRetentionBodyMaxKB
	}
	externalize := mode == "media"
	server.config.SetUsageLogConfig(config.UsageLogConfig{BodyMaxKB: &limit, ExternalizeMedia: &externalize})
	server.startUsageWriter()
	defer server.stopUsageWriter()
	server.engine.Use(server.requestLifecycle())
	const batches = 5
	type gate struct {
		entered chan struct{}
		release chan struct{}
		unblock func()
	}
	gates := make([]gate, batches)
	for index := range gates {
		release := make(chan struct{})
		gates[index] = gate{entered: make(chan struct{}, workers), release: release, unblock: sync.OnceFunc(func() { close(release) })}
	}
	prepared := make(chan finalizeRetentionSample, workers)
	// The only version adapter is at the existing settlement call. There is no
	// fallback after failure; the baseline lacks the completion method entirely.
	completion, hasCompletion := any(server).(interface {
		recordHTTPUsage(*app.RequestContext, *usageRecord)
	})
	server.engine.POST("/hold/:batch", func(ctx context.Context, c *app.RequestContext) {
		var batch int
		if _, err := fmt.Sscanf(c.Param("batch"), "%d", &batch); err != nil || batch < 0 || batch >= len(gates) {
			c.AbortWithMsg("invalid batch", 400)
			return
		}
		body, err := protocol.ReadBoundedBody(requestBody(c), protocol.DefaultRequestBodyBytes)
		if err != nil {
			t.Error(err)
			c.AbortWithMsg("invalid body", 400)
			return
		}
		record := server.initUsageRecord(ctx, c, time.Now(), body, "")
		body = nil
		capture := installDownstreamCapture(c, record, downstreamCaptureLimit(server.usageLogConfig()))
		writer := startStream(c)
		_, _ = writer.Write([]byte(`{"synthetic":"complete"}`))
		if err := writer.Flush(); err != nil {
			t.Error(err)
			return
		}
		if hasCompletion {
			completion.recordHTTPUsage(c, record)
		} else {
			server.recordUsage(record)
		}
		prepared <- finalizeRetentionSample{PreparedLogBytes: usageRetainedPayloadBytes(record), CaptureBytes: len(capture.buf)}
		stream := currentResponseStream(c)
		stream.ExtWriter = &heldFinalizeWriter{ExtWriter: stream.ExtWriter, ctx: ctx, entered: gates[batch].entered, release: gates[batch].release}
	})
	web := startTestServer(server.engine)
	defer web.Close()
	defer func() {
		for _, gate := range gates {
			gate.unblock()
		}
	}()
	client := web.Client()
	client.Timeout = 30 * time.Second
	input := `{"image_url":{"url":"data:image/png;base64,` + strings.Repeat("A", size<<20) + `"}}`
	var samples []finalizeRetentionSample
	sample := func(batch int, phase string, forced bool, preparedBytes int64, captureBytes int) {
		if forced {
			runtime.GC()
		}
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		server.httpRequests.mu.Lock()
		active := len(server.httpRequests.active)
		server.httpRequests.mu.Unlock()
		samples = append(samples, finalizeRetentionSample{Batch: batch, Phase: phase, ForcedGC: forced, Heap: memory.HeapAlloc, HeapInuse: memory.HeapInuse, Allocated: memory.TotalAlloc, Goroutines: runtime.NumGoroutine(), ActiveRequests: active, QueueBytes: server.usageWriterStats().RetainedBytes, PreparedLogBytes: preparedBytes, CaptureBytes: captureBytes})
	}
	awaitDrain := func(shouldWaitHTTP bool, expectedWrites uint64) {
		deadline := time.Now().Add(5 * time.Second)
		for {
			server.httpRequests.mu.Lock()
			active := len(server.httpRequests.active)
			server.httpRequests.mu.Unlock()
			stats := server.usageWriterStats()
			if (!shouldWaitHTTP || active == 0) && stats.Queued == 0 && stats.RetainedBytes == 0 && stats.Written >= expectedWrites {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("HTTP or persistence owner did not drain", active, stats, "expected writes", expectedWrites)
			}
			time.Sleep(time.Millisecond)
		}
	}
	for batch := range batches {
		sample(batch, "before", false, 0, 0)
		var cancels []context.CancelFunc
		done := make(chan error, workers)
		for range workers {
			ctx, cancel := context.WithCancel(t.Context())
			cancels = append(cancels, cancel)
			defer cancel()
			request, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/hold/%d", web.URL, batch), strings.NewReader(input))
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 {
				t.Fatal(response.Status)
			}
			go func() { _, err := io.Copy(io.Discard, response.Body); response.Body.Close(); done <- err }()
		}
		var preparedBytes int64
		var captureBytes int
		for range workers {
			select {
			case <-gates[batch].entered:
			case <-time.After(5 * time.Second):
				t.Fatal("Finalize gate was not reached")
			}
			captured := <-prepared
			preparedBytes += captured.PreparedLogBytes
			captureBytes += captured.CaptureBytes
		}
		if hasCompletion && captureBytes != 0 {
			t.Fatal("completion retained raw capture bytes", captureBytes)
		}
		bound := int64(workers) * (int64(limit) << 11)
		if externalize {
			bound += int64(workers) * int64(size<<20) * 3 / 4
		}
		if preparedBytes > bound {
			t.Fatal("prepared log payload exceeded its documented body/media bound", preparedBytes, bound)
		}
		// Baseline settlement may already be queued. Compare stable held
		// owners after that queue drains, without releasing the HTTP gate.
		expectedWrites := uint64((batch + 1) * workers)
		heldWrites := expectedWrites
		if hasCompletion {
			heldWrites -= uint64(workers)
		}
		awaitDrain(false, heldWrites)
		sample(batch, "held", false, preparedBytes, captureBytes)
		sample(batch, "held", true, preparedBytes, captureBytes)
		if shouldCancel {
			for _, cancel := range cancels {
				cancel()
			}
		} else {
			gates[batch].unblock()
		}
		for range workers {
			select {
			case err := <-done:
				if !shouldCancel && err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("client did not finish")
			}
		}
		awaitDrain(true, expectedWrites)
		if count, err := server.store.CountUsageRecords(t.Context()); err != nil || count != int64((batch+1)*workers) {
			t.Fatal("missing or duplicated completion settlement", count, err)
		}
		sample(batch, "released", false, 0, 0)
		sample(batch, "released", true, 0, 0)
	}
	output := map[string]any{"completionSettlement": hasCompletion, "mode": mode, "bodyMaxKB": limit, "sizeMiB": size, "workers": workers, "cancel": shouldCancel, "scope": "in-process HTTP capture/finalization ownership; includes client and shared input", "samples": samples}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(encoded))
	if directory := os.Getenv("ELYSIA_FLAME_OUTPUT"); directory != "" {
		if err := os.WriteFile(filepath.Join(directory, "slow-finalize-retention.json"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
