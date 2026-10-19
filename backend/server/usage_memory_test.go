package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/storage"
)

func TestUsageBodyMemoryDifferential(t *testing.T) {
	inputs := []string{
		`{"number":9007199254740993,"decimal":1.00,"token":"synthetic","headers":{"X-Codex-Turn-State":"synthetic-state"}}`,
		`{"nested":"{\"x-codex-turn-state\":\"synthetic-state\"}"}`,
		`{"\u0078-codex-turn-state":"synthetic-state","source":{"type":"base64","media_type":"image/png","data":"` + strings.Repeat("A", 1024) + `"}}`,
		`{"plain":"` + strings.Repeat("A", 4096) + `"}`,
		"data: {\"headers\":{\"x-codex-turn-state\":\"synthetic-state\"}}\n\ndata: [DONE]\n",
		`{"x-codex-turn-state":"synthetic-truncated`,
		`{"valid":"prefix"} trailing`,
		`null`, `"plain"`, ` [ ] `,
	}
	results := map[string]usageBody{}
	for index, input := range inputs {
		for _, externalize := range []bool{false, true} {
			for _, limit := range []int{16, 8192} {
				for _, downstream := range []bool{false, true} {
					record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: limit, externalize: externalize}, assets: newAssetSink("synthetic")}
					key := fmt.Sprintf("input=%d/external=%t/limit=%d/downstream=%t", index, externalize, limit, downstream)
					if downstream {
						results[key] = record.finalizeDownstreamBody(usageBody{Content: input})
					} else {
						results[key] = record.sanitizeBody([]byte(input))
					}
				}
			}
		}
	}
	actual, _ := json.MarshalIndent(results, "", "  ")
	if path := os.Getenv("ELYSIA_LOG_MEMORY_BASELINE"); path != "" {
		if err := os.WriteFile(path, actual, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	expected, err := os.ReadFile(filepath.Join("testdata", "usage_memory_baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, actual) {
		t.Fatal("log redaction/media/truncation differs from 8bc0cea")
	}
}

func TestUsageQueuePayloadBudgetAndDrain(t *testing.T) {
	writer := &usageWriterState{queue: make(chan *usageRecord, usageQueueCapacity)}
	server := &Server{usageWriter: writer}
	record := &usageRecord{IncomingBody: usageBody{Content: "body"}, assets: newAssetSink("synthetic")}
	record.assets.register("image/png", "", strings.Repeat("A", 1024))
	// Repeated references inside one record count as a single immutable asset.
	record.assets.items = append(record.assets.items, record.assets.items[0])
	size := int64(4 + record.assets.items[0].payload.size())
	if measured := usageRetainedPayloadBytes(record); measured != size {
		t.Fatalf("shared asset counted twice: %d vs %d", measured, size)
	}
	writer.retainedBytes = usageQueuePayloadBytes - size
	if !server.enqueueUsageRecord(record) {
		t.Fatal("exact budget boundary was rejected")
	}
	if server.enqueueUsageRecord(&usageRecord{IncomingBody: usageBody{Content: "x"}}) {
		t.Fatal("over-budget payload entered asynchronous queue")
	}
	queued := <-writer.queue
	if queued.assets.items[0].payload != record.assets.items[0].payload {
		t.Fatal("queue copied immutable media")
	}
	// Removing the item from the channel is not completion: in-flight writer
	// memory must remain reserved until persistence (including failure) ends.
	if server.enqueueUsageRecord(&usageRecord{IncomingBody: usageBody{Content: "x"}}) {
		t.Fatal("in-flight payload was released before persistence")
	}
	writer.queue <- queued
	writer.mu.Lock()
	server.drainUsageQueueFrom(writer)
	writer.mu.Unlock()
	if writer.retainedBytes != usageQueuePayloadBytes-size {
		t.Fatal("reset did not release the drained reservation")
	}
	writer.retainedBytes = 0
	writer.closed = true
	if server.enqueueUsageRecord(record) {
		t.Fatal("closed writer accepted payload")
	}
}

func TestUsageMaterializationReleasesRawFramesAndTruncationStorage(t *testing.T) {
	record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: 128}}
	record.appendStreamEvent(`{"message":"hello"}`)
	record.materializeStreamEvents()
	if len(record.pendingStreamEvents) != 0 || record.ProviderResponse.Content == "" {
		t.Fatal("raw frames retained after successful materialization")
	}
	first := record.ProviderResponse
	record.materializeStreamEvents()
	if record.ProviderResponse != first {
		t.Fatal("materialization was not idempotent")
	}
	large := strings.Repeat("x", 2<<20)
	small := truncateUsageBody(large, 128)
	if reflect.ValueOf(small.Content).Pointer() == reflect.ValueOf(large).Pointer() {
		t.Fatal("truncated log retained the full body")
	}
}

func TestUsageQueueReleasesPayloadAfterPersistenceFailure(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "closed.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}
	server.startUsageWriter()
	writer := server.usageWriterSnapshot()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if !server.enqueueUsageRecord(&usageRecord{RequestID: "synthetic", StartedAt: time.Now(), IncomingBody: usageBody{Content: strings.Repeat("x", 1<<20)}}) {
		t.Fatal("fixture did not enqueue")
	}
	server.stopUsageWriter()
	if writer.retainedBytes != 0 {
		t.Fatal("failed persistence retained its reservation", writer.retainedBytes)
	}
}
