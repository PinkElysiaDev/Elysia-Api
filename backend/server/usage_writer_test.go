package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

func TestResetKeepsRecordsSubmittedAfterReturn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := storage.Open(filepath.Join(t.TempDir(), "reset-after.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	s := &Server{store: store}
	s.startUsageWriter()
	defer s.stopUsageWriter()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		id := fmt.Sprintf("inflight-%d", i)
		go func() {
			defer wg.Done()
			now := time.Now()
			s.recordUsage(&usageRecord{RequestID: id, StartedAt: now, EndedAt: now, ModelName: "m", StatusCode: 200})
		}()
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/reset", nil)
	s.resetUsage(ctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", rec.Code, rec.Body.String())
	}
	wg.Wait()

	now := time.Now()
	s.recordUsage(&usageRecord{RequestID: "after-reset", StartedAt: now, EndedAt: now, ModelName: "m", StatusCode: 200})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, items, err := store.QueryUsageLogs(ctx.Request.Context(), storage.UsageQuery{Limit: 50})
		if err != nil {
			t.Fatalf("logs: %v", err)
		}
		for _, item := range items {
			if item.RequestID == "after-reset" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("record written after reset returned must persist")
}

func TestResetUsageDropsQueuedWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := storage.Open(filepath.Join(t.TempDir(), "reset-queue.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	s := &Server{store: store}
	s.startUsageWriter()
	defer s.stopUsageWriter()

	started := time.Now().UTC()
	if err := s.saveUsageRecordToStore(&usageRecord{
		RequestID: "keep-before", StartedAt: started, EndedAt: started.Add(time.Millisecond),
		ModelName: "m", StatusCode: 200,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	queued := &usageRecord{
		RequestID: "queued-after-reset", StartedAt: started, EndedAt: started.Add(time.Millisecond),
		ModelName: "m", StatusCode: 200, writeGen: 0,
	}
	s.usageWriter.queue <- queued

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/reset", nil)
	s.resetUsage(ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", w.Code, w.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		totals, err := store.UsageTotals(ctx.Request.Context(), storage.UsageQuery{})
		if err != nil {
			t.Fatalf("totals: %v", err)
		}
		if totals["requests"].(int) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	totals, _ := store.UsageTotals(ctx.Request.Context(), storage.UsageQuery{})
	t.Fatalf("reset must remain empty after queued write, totals=%#v", totals)
}

func TestStopUsageWriterIdempotent(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "stop-writer.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	s := &Server{store: store}
	s.startUsageWriter()
	s.stopUsageWriter()
	s.stopUsageWriter()
	s.stopUsageWriter()
}

func TestDoShutdownIsIdempotentAndBlocksEnqueue(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "shutdown.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Windows 下 TempDir 清理无法删除仍被连接池持有的 sqlite 文件，必须显式关库。
	defer store.Close()
	s := &Server{store: store}
	s.startUsageWriter()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.doShutdown()
		}()
	}
	wg.Wait()

	if s.enqueueUsageRecord(&usageRecord{RequestID: "after"}) {
		t.Fatal("enqueue after shutdown must fail")
	}
}

func TestResetDrainDoesNotHangWhenQueueCloses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := storage.Open(filepath.Join(t.TempDir(), "reset-drain-close.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	s := &Server{store: store}
	s.startUsageWriter()

	for i := 0; i < 64; i++ {
		s.enqueueUsageRecord(&usageRecord{RequestID: "q", StartedAt: time.Now()})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/reset", nil)
		s.resetUsage(ctx)
	}()
	s.stopUsageWriter()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reset drain hung after queue close")
	}
}

func TestUsageSeqIncrementsOnPersistAndReset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := storage.Open(filepath.Join(t.TempDir(), "usage-seq.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	s := &Server{store: store}
	if s.usageSeq.Load() != 0 {
		t.Fatalf("seq start = %d", s.usageSeq.Load())
	}
	s.persistUsageRecord(&usageRecord{
		RequestID: "one", StartedAt: time.Now(), EndedAt: time.Now(),
		ModelName: "m", StatusCode: 200,
	})
	if s.usageSeq.Load() != 1 {
		t.Fatalf("seq after persist = %d", s.usageSeq.Load())
	}
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/reset", nil)
	s.resetUsage(ctx)
	if s.usageSeq.Load() != 2 {
		t.Fatalf("seq after reset = %d", s.usageSeq.Load())
	}
}

func TestEnqueueAfterStopDoesNotPanic(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "enqueue-stop.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	s := &Server{store: store}
	s.startUsageWriter()
	s.stopUsageWriter()
	if s.enqueueUsageRecord(&usageRecord{RequestID: "x"}) {
		t.Fatal("enqueue after stop must return false")
	}
}

func TestEnqueueConcurrentWithStopDoesNotPanic(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "enqueue-race.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	s := &Server{store: store}
	s.startUsageWriter()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.enqueueUsageRecord(&usageRecord{RequestID: "x", StartedAt: time.Now()})
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.stopUsageWriter()
	}()
	wg.Wait()
	s.stopUsageWriter()
}
