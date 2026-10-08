package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
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

func TestSourceRefreshCoalescesSavesAndSharesSyncGate(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer old" {
			entered <- struct{}{}
			<-release
			w.Write([]byte(`{"data":[{"id":"stale"}]}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer newest" {
			t.Errorf("intermediate key fetched: %s", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"data":[{"id":"latest"}]}`))
	}))
	defer upstream.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, upstream.URL, []storage.SourceAPIKey{{Value: "old"}}, "original")
	if !s.launchSourceRefresh(src.ID) {
		t.Fatal("not started")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request not started")
	}
	if _, err := s.refreshSourceSync(t.Context(), src.ID); err == nil {
		t.Fatal("synchronous caller bypassed task deduplication")
	}
	for _, key := range []string{"intermediate", "newest"} {
		src.APIKeys = []storage.SourceAPIKey{{Value: key}}
		if err := s.saveSource(t.Context(), src); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	waitForSourceRefreshDone(t, s, src.ID)
	if calls.Load() != 2 || !slices.Equal(discoveryModelIDs(t, s), []string{"latest"}) {
		t.Fatalf("latest save lost or extra refreshes: calls=%d models=%v", calls.Load(), discoveryModelIDs(t, s))
	}
	if state := s.sourceRefreshStateOf(src.ID); state.LastError != "" || state.LastCount != 1 {
		t.Fatalf("wrong final state: %+v", state)
	}
}

func TestSourceRefreshSwitchToManualInvalidatesFetchedResult(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		w.Write([]byte(`{"data":[{"id":"stale"}]}`))
	}))
	defer upstream.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, upstream.URL, []storage.SourceAPIKey{{Value: "key", FetchedModels: []string{"original"}, AllowedModels: []string{"manual"}}}, "original")
	s.launchSourceRefresh(src.ID)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request not started")
	}
	src.AutoFetchModels = false
	src.ManualModels = []storage.Model{{ID: "manual"}}
	if err := s.saveSource(t.Context(), src); err != nil {
		t.Fatal(err)
	}
	if _, err := s.refreshSourceByID(t.Context(), src.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitForSourceRefreshDone(t, s, src.ID)
	saved := savedDiscoverySource(t, s)
	if calls.Load() != 1 || !slices.Equal(discoveryModelIDs(t, s), []string{"manual"}) || saved.APIKeys[0].FetchedModels != nil || !saved.APIKeys[0].KeyAllowsModel("manual") {
		t.Fatal("stale refresh overwrote manual configuration")
	}
}

func TestSourceRefreshLoadsLatestSnapshotAfterQueue(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new" {
			t.Error("queued job used stale credentials")
		}
		w.Write([]byte(`{"data":[{"id":"fresh"}]}`))
	}))
	defer upstream.Close()
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, upstream.URL, []storage.SourceAPIKey{{Value: "old"}}, "old")
	s.refreshSem = make(chan struct{}, 1)
	s.refreshSem <- struct{}{}
	s.launchSourceRefresh(src.ID)
	src.APIKeys = []storage.SourceAPIKey{{Value: "new"}}
	if err := s.saveSource(t.Context(), src); err != nil {
		t.Fatal(err)
	}
	<-s.refreshSem
	waitForSourceRefreshDone(t, s, src.ID)
	if !slices.Equal(discoveryModelIDs(t, s), []string{"fresh"}) {
		t.Fatal("queued refresh did not load new source")
	}
}

func TestSourceRefreshCanceledCallerKeepsPendingSave(t *testing.T) {
	entered := make(chan struct{}, 1)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer old" {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Write([]byte(`{"data":[{"id":"latest"}]}`))
	}))
	defer upstream.Close()
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, upstream.URL, []storage.SourceAPIKey{{Value: "old"}}, "original")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.refreshSourceSync(ctx, src.ID)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request not started")
	}
	src.APIKeys = []storage.SourceAPIKey{{Value: "new"}}
	if err := s.saveSource(t.Context(), src); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled caller should receive its error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled caller did not return")
	}
	waitForSourceRefreshDone(t, s, src.ID)
	if calls.Load() != 2 || !slices.Equal(discoveryModelIDs(t, s), []string{"latest"}) {
		t.Fatalf("caller cancellation lost the pending save: calls=%d models=%v", calls.Load(), discoveryModelIDs(t, s))
	}
}
