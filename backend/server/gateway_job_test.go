package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func jobGatewayRequest(t *testing.T, s *Server, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer gateway-test-token")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	s.engine.ServeHTTP(response, request)
	return response
}

func TestGatewayJobExplicitWaitTimeoutPolicies(t *testing.T) {
	for _, policy := range []string{"cancel", "continue"} {
		t.Run(policy, func(t *testing.T) {
			s, _ := newProtocolAdminTestServer(t)
			synchronous := loadGatewayDefinition(t, "job-alpha")
			synchronous.ID, synchronous.Name = "sync-jobs", "sync-jobs"
			delete(synchronous.Capabilities, protocol.AsyncJobsCapability)
			synchronous.TaskSamples = nil
			synchronous.Operations = map[string]protocol.Operation{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: protocol.HTTPJSON, Auth: protocol.Credential{Location: "none"}}}
			activateGatewayDefinition(t, s, synchronous)
			upstream := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "job-alpha"))
			var cancellations atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				state := "queued"
				if r.Method == http.MethodDelete {
					state = "cancelled"
					cancellations.Add(1)
				}
				_, _ = w.Write([]byte(`{"ticket":"provider-id","phase":"` + state + `"}`))
			}))
			defer provider.Close()
			setupGatewayModel(t, s, upstream, provider.URL)
			t.Cleanup(s.gatewayJobs.stop)
			bindings, err := s.store.ListProtocolBindings(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			binding := bindings[0]
			binding.Binding.Operation = "submit"
			binding.Binding.Wait = &protocol.JobWait{TimeoutMillis: 60, OnTimeout: policy}
			if issues := protocol.CheckBinding(binding.Binding, upstream); len(issues) > 0 {
				t.Fatal(issues)
			}
			if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
				t.Fatal(err)
			}
			body := `{"deployment":"group","turns":[{"actor":"user","segments":[{"text":"hello"}]}]}`
			response := jobGatewayRequest(t, s, "POST", "/gateway/sync-jobs/generate", body, "")
			if response.Code != 504 {
				t.Fatalf("wait: %d %s", response.Code, response.Body)
			}
			location := response.Header().Get("Location")
			id := location[strings.LastIndex(location, "/")+1:]
			job, err := s.store.ReadGenerationJob(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if job.IsCancelRequested != (policy == "cancel") {
				t.Fatalf("wrong timeout action: %+v", job)
			}
			// Stop the background loop before deterministically advancing recovery.
			s.gatewayJobs.stop()
			job.NextAttempt = time.Now().Add(-time.Second)
			if _, err := s.store.UpdateGenerationJob(t.Context(), job, job.Revision); err != nil {
				t.Fatal(err)
			}
			if err := s.gatewayJobs.coordinator.Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			if (cancellations.Load() == 1) != (policy == "cancel") {
				t.Fatalf("cancel calls: %d", cancellations.Load())
			}
		})
	}
}

func TestGatewayJobsIndependentMappingsPinnedRevisionAndUsage(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	ingress := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "job-alpha"))
	definition := loadGatewayDefinition(t, "job-beta")
	upstream := activateGatewayDefinition(t, s, definition)
	var submits, results atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-New-Revision") != "" {
			t.Error("pending job used a newly activated revision")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			submits.Add(1)
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte(`"deployment":"upstream-model"`)) || r.Header.Get("Idempotency-Key") == "" {
				t.Errorf("submission lost model/idempotency: %s", body)
			}
			_, _ = w.Write([]byte(`{"task_ref":"provider-1","state":"queued"}`))
			return
		}
		if r.URL.Query().Get("task_ref") != "provider-1" {
			t.Error("provider control identity was not mapped")
		}
		if strings.HasSuffix(r.URL.Path, "/result") {
			results.Add(1)
			_, _ = w.Write([]byte(`{"requestId":"r","answer":[{"actor":"assistant","segments":[{"text":"done"}]}],"meter":{"input":{"count":100,"origin":"observed"},"output":{"count":5,"origin":"observed"},"cacheRead":{"count":40,"origin":"observed"},"cacheCreation":{"count":0,"origin":"observed"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"task_ref":"provider-1","state":"completed"}`))
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	t.Cleanup(s.gatewayJobs.stop)
	body := `{"deployment":"group","turns":[{"actor":"user","segments":[{"text":"hello"}]}]}`
	submission := jobGatewayRequest(t, s, "POST", "/gateway/job-alpha/jobs", body, "one")
	if submission.Code != 202 {
		t.Fatalf("submit: %d %s", submission.Code, submission.Body)
	}
	location := submission.Header().Get("Location")
	if !strings.Contains(location, "/_jobs/") {
		t.Fatalf("no durable location: %s", location)
	}
	duplicate := jobGatewayRequest(t, s, "POST", "/gateway/job-alpha/jobs", body, "one")
	if duplicate.Code != 202 || submits.Load() != 1 || duplicate.Header().Get("Location") != location {
		t.Fatalf("duplicate: %d %s", duplicate.Code, duplicate.Body)
	}
	// New definitions cannot change polling/result mapping for an accepted job.
	definition.Version = "2"
	for name, operation := range definition.Operations {
		operation.Headers = map[string]string{"X-New-Revision": "yes"}
		definition.Operations[name] = operation
	}
	activateGatewayDefinition(t, s, definition)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		response := jobGatewayRequest(t, s, "GET", location+"/result", "", "")
		if response.Code == 200 {
			if !strings.Contains(response.Body.String(), `"done"`) {
				t.Fatalf("result: %s", response.Body)
			}
			break
		}
		if response.Code != 202 {
			t.Fatalf("result polling: %d %s", response.Code, response.Body)
		}
		select {
		case <-deadline.C:
			t.Fatal("job never completed")
		case <-ticker.C:
		}
	}
	for range 3 {
		if response := jobGatewayRequest(t, s, "GET", location+"/result", "", ""); response.Code != 200 {
			t.Fatal(response.Body)
		}
	}
	s.gatewayJobs.stop()
	_, logs, err := s.store.QueryUsageLogs(t.Context(), storage.UsageQuery{Limit: 10})
	if err != nil || len(logs) != 1 || logs[0].CacheHitTokens != 40 || submits.Load() != 1 || results.Load() != 1 {
		t.Fatalf("settlements/calls: %+v %v submit=%d result=%d", logs, err, submits.Load(), results.Load())
	}
	payload, _, err := s.store.GetUsageRecordJSON(t.Context(), logs[0].RequestID)
	if err != nil {
		t.Fatal(err)
	}
	var record usageRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatal(err)
	}
	if record.IngressRevision != ingress.Hash() || record.UpstreamRevision != upstream.Hash() || record.ProtocolUsage.CacheCreation == nil || record.ProtocolUsage.CacheCreation.Count != 0 {
		t.Fatalf("pinned accounting: %s", payload)
	}
}

func TestGatewayJobUncertainOutcomeIsDurableAndNotRetried(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	compiled := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "job-alpha"))
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"unknown":"accepted but unreadable"}`))
	}))
	defer provider.Close()
	setupGatewayModel(t, s, compiled, provider.URL)
	t.Cleanup(s.gatewayJobs.stop)
	body := `{"deployment":"group","turns":[{"actor":"user","segments":[{"text":"hello"}]}]}`
	first := jobGatewayRequest(t, s, "POST", "/gateway/job-alpha/jobs", body, "same")
	if first.Code != 202 || !strings.Contains(first.Body.String(), `"uncertain"`) {
		t.Fatalf("uncertain lost: %d %s", first.Code, first.Body)
	}
	second := jobGatewayRequest(t, s, "POST", "/gateway/job-alpha/jobs", body, "same")
	if second.Code != 202 || calls.Load() != 1 {
		t.Fatal("uncertain submission replayed")
	}
	conflict := jobGatewayRequest(t, s, "POST", "/gateway/job-alpha/jobs", strings.Replace(body, "hello", "changed", 1), "same")
	if conflict.Code != 409 || calls.Load() != 1 {
		t.Fatal("different request reused idempotency identity")
	}
	coordinator, err := s.generationJobs()
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("worker retried uncertain generation")
	}
}
