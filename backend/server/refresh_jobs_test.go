package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elysia-api/backend/storage"
)

// waitForSourceRefreshDone 轮询等待指定源的后台拉取任务结束（带超时兜底）。
func waitForSourceRefreshDone(t *testing.T, s *Server, sourceID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if !s.sourceRefreshStateOf(sourceID).Refreshing {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("source %s refresh job did not finish in time", sourceID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 任务去重：进行中的源重复触发返回 false；完成后可再次启动。
// 上游故意放慢（300ms），保证任务确实在飞行中做二次触发。
func TestSourceRefreshJobDedup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer srv.Close()

	s := newKeyPermissionTestServer(t)
	ctx := context.Background()
	source := storage.ModelSource{
		ID: "src1", Name: "slow", BaseURL: srv.URL, Platform: "openai",
		Enabled: true, AutoFetchModels: true, APIKey: "k",
	}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if started, already, err := s.startSourceRefreshByID(ctx, "src1"); !started || already || err != nil {
		t.Fatalf("first start should succeed: started=%v already=%v err=%v", started, already, err)
	}
	// 任务进行中：重复触发被去重。
	if started, already, _ := s.startSourceRefreshByID(ctx, "src1"); started || !already {
		t.Fatalf("second start must be deduped: started=%v already=%v", started, already)
	}
	waitForSourceRefreshDone(t, s, "src1")

	state := s.sourceRefreshStateOf("src1")
	if state.LastCount != 1 || state.LastError != "" || state.LastFinishedAt == "" {
		t.Fatalf("completion state wrong: %+v", state)
	}
	// 完成后可再次启动。
	if started, already, err := s.startSourceRefreshByID(ctx, "src1"); !started || already || err != nil {
		t.Fatalf("restart after completion should succeed: %v %v %v", started, already, err)
	}
	waitForSourceRefreshDone(t, s, "src1")
}

// 失败结果也记录到状态：上游 401 → LastError 填写，refreshing 清除。
func TestSourceRefreshJobRecordsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	s := newKeyPermissionTestServer(t)
	ctx := context.Background()
	source := storage.ModelSource{
		ID: "src1", Name: "unauthorized", BaseURL: srv.URL, Platform: "openai",
		Enabled: true, AutoFetchModels: true, APIKey: "bad-key",
	}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if started, _, err := s.startSourceRefreshByID(ctx, "src1"); !started || err != nil {
		t.Fatalf("start: %v %v", started, err)
	}
	waitForSourceRefreshDone(t, s, "src1")

	state := s.sourceRefreshStateOf("src1")
	if state.LastError == "" {
		t.Fatalf("error must be recorded: %+v", state)
	}
	if state.LastFinishedAt == "" {
		t.Fatalf("finish time must be recorded even on failure")
	}
}
