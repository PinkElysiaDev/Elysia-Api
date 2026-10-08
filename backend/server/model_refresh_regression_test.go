package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/elysia-api/backend/storage"
)

func seedDiscoverySource(t *testing.T, s *Server, url string, keys []storage.SourceAPIKey, ids ...string) storage.ModelSource {
	t.Helper()
	src := storage.ModelSource{ID: "discovery", Name: "discovery", BaseURL: url, Platform: "openai", Enabled: true, AutoFetchModels: true, APIKeys: keys}
	if err := s.store.UpsertSource(t.Context(), src); err != nil {
		t.Fatal(err)
	}
	models := make([]storage.Model, 0, len(ids))
	for _, id := range ids {
		models = append(models, storage.Model{ID: id, Name: id})
	}
	if _, err := s.store.MergeSourceModels(t.Context(), src, models); err != nil {
		t.Fatal(err)
	}
	return src
}

func discoveryModelIDs(t *testing.T, s *Server) []string {
	t.Helper()
	models, err := s.store.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	slices.Sort(ids)
	return ids
}

func savedDiscoverySource(t *testing.T, s *Server) storage.ModelSource {
	t.Helper()
	sources, err := s.store.ListSources(t.Context())
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%d error=%v", len(sources), err)
	}
	return sources[0]
}

func TestDiscoveryRegressionPartialFailureDoesNotDeleteModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer a" {
			w.Write([]byte(`{"data":[{"id":"alpha"}]}`))
		} else {
			w.WriteHeader(503)
		}
	}))
	defer upstream.Close()
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, upstream.URL, []storage.SourceAPIKey{{Value: "a", FetchedModels: []string{"alpha"}}, {Value: "b", FetchedModels: []string{"beta"}}}, "alpha", "beta")
	if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "g", Name: "g", Enabled: true, Models: []string{"discovery:beta"}}); err != nil {
		t.Fatal(err)
	}
	summary, err := s.refreshSourceByID(t.Context(), src.ID)
	if err == nil {
		t.Error("partial discovery must fail the refresh")
	}
	ids := discoveryModelIDs(t, s)
	groups, err := s.store.ListGroups(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []string{"alpha", "beta"}) || !slices.Equal(groups[0].Models, []string{"discovery:beta"}) || summary.Count != 0 || len(summary.Removed) != 0 {
		t.Fatalf("partial discovery changed stored data: models=%v groups=%v summary=%+v", ids, groups, summary)
	}
	if len(summary.Keys) != 2 || summary.Keys[1].Error == "" {
		t.Fatalf("missing per-key error: %+v", summary.Keys)
	}
}

func TestDiscoveryRegressionSaveUsesDiscoveredModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"alpha"},{"id":"beta"}]}`))
	}))
	defer upstream.Close()
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, upstream.URL, []storage.SourceAPIKey{{Value: "a", FetchedModels: []string{"alpha", "beta"}, AllowedModels: []string{"alpha"}}, {Value: "b", AllowedModels: []string{}}}, "alpha", "beta")
	s.engine.PUT("/discovery/source/:id", s.adminUpsertSource)
	payload, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/discovery/source/"+src.ID, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	s.engine.ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("save status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	waitForSourceRefreshDone(t, s, src.ID)
	keys := savedDiscoverySource(t, s).APIKeys
	if !keys[0].KeyAllowsModel("beta") || !keys[1].KeyAllowsModel("alpha") || keys[0].AllowedModels != nil || keys[1].AllowedModels != nil {
		t.Error("automatic source keys must serve their discovered models")
	}
}

func TestDiscoveryRegressionInFlightRefreshPreservesEditedKeyPool(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.Write([]byte(`{"data":[{"id":"alpha"}]}`))
	}))
	t.Cleanup(upstream.Close)
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, upstream.URL, []storage.SourceAPIKey{{Value: "old-a"}, {Value: "old-b"}}, "alpha")
	if !s.launchSourceRefresh(src.ID) {
		t.Fatal("refresh not started")
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("requests not started")
		}
	}
	edited := src
	edited.APIKeys = []storage.SourceAPIKey{{Value: "new-a"}, {Value: "old-b", Disabled: true}}
	if err := s.store.UpsertSource(t.Context(), edited); err != nil {
		t.Fatal(err)
	}
	if s.launchSourceRefresh(edited.ID) {
		t.Error("started a duplicate refresh")
	}
	close(release)
	waitForSourceRefreshDone(t, s, src.ID)
	saved := savedDiscoverySource(t, s)
	if saved.APIKeys[0].Value != "new-a" || !saved.APIKeys[1].Disabled {
		t.Error("old refresh overwrote newly edited key pool")
	}
}

func TestDiscoveryRegressionDeletedSourceStaysDeleted(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.Write([]byte(`{"data":[{"id":"resurrected"}]}`))
	}))
	t.Cleanup(upstream.Close)
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, upstream.URL, []storage.SourceAPIKey{{Value: "a"}}, "alpha")
	if !s.launchSourceRefresh(src.ID) {
		t.Fatal("refresh not started")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request not started")
	}
	if err := s.deleteSourceCascade(t.Context(), s.store, src.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitForSourceRefreshDone(t, s, src.ID)
	sources, err := s.store.ListSources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ids := discoveryModelIDs(t, s)
	if len(sources) != 0 || len(ids) != 0 {
		t.Error("completed refresh recreated models after source deletion")
	}
}

func TestDiscoveryRegressionEmptyFetchPreservesData(t *testing.T) {
	for _, single := range []bool{true, false} {
		name := "multi"
		if single {
			name = "single"
		}
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":[]}`)) }))
			defer upstream.Close()
			s := newKeyPermissionTestServer(t)
			keys := []storage.SourceAPIKey{{Value: "a", FetchedModels: []string{"alpha"}}}
			if !single {
				keys = append(keys, storage.SourceAPIKey{Value: "b", FetchedModels: []string{"alpha"}})
			}
			src := seedDiscoverySource(t, s, upstream.URL, keys, "alpha")
			before := savedDiscoverySource(t, s)
			_, err := s.refreshSourceByID(t.Context(), src.ID)
			if err == nil {
				t.Fatal("expected empty-list failure")
			}
			saved := savedDiscoverySource(t, s)
			ids := discoveryModelIDs(t, s)
			if !reflect.DeepEqual(before, saved) || !slices.Equal(ids, []string{"alpha"}) {
				t.Error("failed empty refresh changed source or models")
			}
		})
	}
}

func TestDiscoveryRegressionMissingContinuationIsError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"alpha"}],"has_more":true,"last_id":""}`))
	}))
	defer upstream.Close()
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, upstream.URL, []storage.SourceAPIKey{{Value: "a"}}, "alpha", "beta")
	src.Platform = "claude"
	if err := s.store.UpsertSource(t.Context(), src); err != nil {
		t.Fatal(err)
	}
	summary, err := s.refreshSourceByID(t.Context(), src.ID)
	if err == nil || summary.Count != 0 || !slices.Equal(discoveryModelIDs(t, s), []string{"alpha", "beta"}) {
		t.Fatal("incomplete pagination must fail and retain existing models")
	}
}

func TestDiscoveryRegressionOptionalMetadataDoesNotBlockIDs(t *testing.T) {
	for _, body := range []string{
		`{"models":[{"name":"models/alpha","displayName":null}]}`,
		`{"models":[{"name":"models/alpha"}],"nextPageToken":null}`,
	} {
		t.Run(body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer upstream.Close()
			s := newKeyPermissionTestServer(t)
			models, err := s.fetchModelsFromSource(t.Context(), storage.ModelSource{BaseURL: upstream.URL, Platform: "gemini"}, "a")
			if err != nil || len(models) != 1 || models[0].ID != "alpha" {
				t.Fatalf("model discovery with optional metadata: models=%v err=%v", models, err)
			}
		})
	}
}

func TestDiscoveryRegressionEmptyKeyCatalogStaysEmpty(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer a" {
			w.Write([]byte(`{"data":[]}`))
		} else {
			w.Write([]byte(`{"data":[{"id":"alpha"}]}`))
		}
	}))
	defer upstream.Close()
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, upstream.URL, []storage.SourceAPIKey{{Value: "a"}, {Value: "b"}}, "alpha")
	if _, err := s.refreshSourceByID(t.Context(), src.ID); err != nil {
		t.Fatal(err)
	}
	saved := savedDiscoverySource(t, s)
	if saved.APIKeys[0].FetchedModels == nil || saved.APIKeys[0].KeyAllowsModel("alpha") {
		t.Error("empty discovered list became unrestricted after persistence")
	}
}

func TestDiscoveryRegressionDisabledPoolDoesNotUseLegacyKey(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"data":[{"id":"alpha"}]}`)) }))
	defer upstream.Close()
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, upstream.URL, []storage.SourceAPIKey{{Value: "old", Disabled: true}}, "alpha")
	src.APIKey = "old"
	if err := s.store.UpsertSource(t.Context(), src); err != nil {
		t.Fatal(err)
	}
	_, err := s.refreshSourceByID(t.Context(), src.ID)
	if err == nil || calls != 0 {
		t.Fatalf("disabled key pool must reject discovery: calls=%d err=%v", calls, err)
	}
}

func TestDiscoveryRegressionDisabledPoolBlocksAllConsumers(t *testing.T) {
	s := newKeyPermissionTestServer(t)
	src := seedDiscoverySource(t, s, "https://example.invalid", []storage.SourceAPIKey{{Value: "disabled", Disabled: true}}, "alpha")
	src.APIKey = "legacy"
	if err := s.store.UpsertSource(t.Context(), src); err != nil {
		t.Fatal(err)
	}
	model := storage.Model{ID: "alpha", Name: "alpha", SourceID: src.ID, APIKey: "snapshot", Enabled: true, Available: true}
	if _, ok := s.resolveModelSource(model, collectSourceKeys([]storage.ModelSource{src})); ok {
		t.Fatal("routing/health probe fell back to a disabled credential")
	}
	if err := applyAgentPermittedKey(t.Context(), s.store, &model); err == nil {
		t.Fatal("agent fell back to a disabled credential")
	}
	src.APIKeys = nil
	src.APIKey = ""
	if err := s.store.UpsertSource(t.Context(), src); err != nil {
		t.Fatal(err)
	}
	ref, ok := s.resolveModelSource(model, collectSourceKeys([]storage.ModelSource{src}))
	if !ok || ref.APIKey != "" {
		t.Fatal("anonymous source should stay usable without snapshot credentials")
	}
	if err := applyAgentPermittedKey(t.Context(), s.store, &model); err != nil || model.APIKey != "" {
		t.Fatal("anonymous agent source reused snapshot credentials", err)
	}
}

func TestDiscoveryRegressionOfficialGeminiPaginationKeepsIDs(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("pageToken") == "" {
			w.Write([]byte(`{"models":[{"name":"models/Gemini-X/custom-suffix","displayName":"Unrelated label","inputTokenLimit":1000000}],"nextPageToken":"second"}`))
		} else {
			if r.URL.Query().Get("pageToken") != "second" {
				t.Error("wrong pagination cursor")
			}
			w.Write([]byte(`{"models":[{"name":"gemini-bare-id","displayName":null}]}`))
		}
	}))
	defer upstream.Close()
	s := newKeyPermissionTestServer(t)
	models, err := s.fetchModelsFromSource(t.Context(), storage.ModelSource{Platform: "gemini", BaseURL: upstream.URL}, "synthetic-key")
	if err != nil || calls != 2 || len(models) != 2 {
		t.Fatalf("pagination: calls=%d models=%v err=%v", calls, models, err)
	}
	if models[0].ID != "Gemini-X/custom-suffix" || models[0].Name != models[0].ID || models[0].MaxTokens != 1000000 || models[1].ID != "gemini-bare-id" || models[1].Name != models[1].ID {
		t.Fatal("model identity or metadata changed", models)
	}
}
