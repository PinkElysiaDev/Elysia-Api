package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2acompat/a2av0"
	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/storage"
)

// A repeated message must use the existing task, not execute the model again.
func TestA2AOfficialClients(t *testing.T) {
	for _, version := range []a2a.ProtocolVersion{a2av0.Version, a2a.Version} {
		t.Run(string(version), func(t *testing.T) {
			s := newAgentIntegrationServer(t)
			fake := newFakeAgentModelServer(t, [][]string{{
				openAIChunk("reply", map[string]any{"role": "assistant", "content": "sdk reply"}, "", nil),
				openAIChunk("reply", map[string]any{}, "stop", nil), openAIDone(),
			}})
			seedAgentModel(t, s, fake.URL)
			session, err := s.createRemoteAgentSession(t.Context(), "SDK", agent.ModeCreate, "", &agent.Settings{ModelSourceID: "s1", ModelName: "fake-model"})
			if err != nil {
				t.Fatal(err)
			}
			token := storage.APIToken{Name: "a2a-sdk", Token: "a2a-sdk-key", Enabled: true, Scopes: []string{storage.TokenScopeAgent}}
			if err := s.store.UpsertAPIToken(t.Context(), token); err != nil {
				t.Fatal(err)
			}
			router := newTestEngine(t)
			router.POST("/a2a", s.agentRemoteGate(), s.agentRemoteAuth(), s.handleA2A)
			server := startTestServer(router)
			defer server.Close()
			var client a2aclient.Transport
			if version == a2av0.Version {
				client = a2av0.NewJSONRPCTransport(a2av0.JSONRPCTransportConfig{URL: server.URL + "/a2a", Client: server.Client()})
			} else {
				client = a2aclient.NewJSONRPCTransport(server.URL+"/a2a", server.Client())
			}
			defer client.Destroy()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			headers := a2aclient.ServiceParams{"Authorization": {"Bearer " + token.Token}, "A2A-Version": {string(version)}}
			message := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
			message.ContextID = session.ID
			request := &a2a.SendMessageRequest{Message: message}
			var taskID a2a.TaskID
			var completed, artifact bool
			for event, err := range client.SendStreamingMessage(ctx, headers, request) {
				if err != nil {
					t.Fatal(err)
				}
				taskID = event.TaskInfo().TaskID
				switch event := event.(type) {
				case *a2a.TaskArtifactUpdateEvent:
					artifact = event.Artifact.Parts[0].Text() == "sdk reply"
				case *a2a.TaskStatusUpdateEvent:
					completed = event.Status.State == a2a.TaskStateCompleted
				}
			}
			if !artifact || !completed || taskID == "" {
				t.Fatalf("missing result: task=%s artifact=%v completed=%v", taskID, artifact, completed)
			}
			task, err := client.GetTask(ctx, headers, &a2a.GetTaskRequest{ID: taskID})
			if err != nil || task.Status.State != a2a.TaskStateCompleted || len(task.Artifacts) != 1 {
				t.Fatalf("snapshot: %+v %v", task, err)
			}
			result, err := client.SendMessage(ctx, headers, request)
			if err != nil || result.TaskInfo().TaskID != taskID || fake.calls.Load() != 1 {
				t.Fatalf("deduplication: %v %v calls=%d", result, err, fake.calls.Load())
			}
			var replayed int
			for _, err := range client.SubscribeToTask(ctx, headers, &a2a.SubscribeToTaskRequest{ID: taskID}) {
				if err != nil {
					t.Fatal(err)
				}
				replayed++
			}
			if replayed == 0 {
				t.Fatal("empty task replay")
			}
			if _, err := client.CancelTask(ctx, headers, &a2a.CancelTaskRequest{ID: taskID}); !errors.Is(err, a2a.ErrTaskNotCancelable) {
				t.Fatalf("terminal cancel: %v", err)
			}
			if version == a2a.Version {
				page, err := client.ListTasks(ctx, headers, &a2a.ListTasksRequest{ContextID: session.ID, IncludeArtifacts: true, PageSize: 1})
				if err != nil || page.TotalSize != 1 || len(page.Tasks) != 1 || page.Tasks[0].ID != taskID {
					t.Fatalf("list: %+v %v", page, err)
				}
			}
			token.Enabled = false
			if err := s.store.UpsertAPIToken(ctx, token); err != nil {
				t.Fatal(err)
			}
			s.invalidateRouteCache()
			if _, err := client.GetTask(ctx, headers, &a2a.GetTaskRequest{ID: taskID}); err == nil {
				t.Fatal("revoked token accepted")
			}
		})
	}
}

func TestA2ASubscriptionsSurviveInputRequired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := (&Server{}).a2aService()
		entry := h.register("session:1", "session", false)
		entry.setStatus(a2a.TaskStateWorking, "started")
		var wg sync.WaitGroup
		states := make([][]a2a.TaskState, 2)
		for i := range states {
			wg.Go(func() {
				for event, err := range h.SubscribeToTask(t.Context(), &a2a.SubscribeToTaskRequest{ID: entry.id}) {
					if err != nil {
						t.Error(err)
						return
					}
					states[i] = append(states[i], event.(*a2a.TaskStatusUpdateEvent).Status.State)
				}
			})
		}
		synctest.Wait()
		entry.setStatus(a2a.TaskStateInputRequired, "approval")
		synctest.Wait()
		for i := range states {
			if len(states[i]) != 2 {
				t.Fatalf("subscriber %d missed input-required: %v", i, states[i])
			}
		}
		entry.setStatus(a2a.TaskStateWorking, "resumed")
		entry.setStatus(a2a.TaskStateCanceled, "canceled")
		entry.setStatus(a2a.TaskStateCompleted, "late completion")
		wg.Wait()
		for i := range states {
			if len(states[i]) != 4 || states[i][3] != a2a.TaskStateCanceled {
				t.Fatalf("subscriber %d: %v", i, states[i])
			}
		}
		if entry.snapshot().Status.State != a2a.TaskStateCanceled {
			t.Fatal("terminal state overwritten")
		}
	})
}

func TestA2ARegistryEvictionAndPagination(t *testing.T) {
	h := (&Server{}).a2aService()
	active := h.register("active:1", "active", false)
	for i := 0; i < a2aRegistryLimit; i++ {
		entry := h.register(a2a.TaskID(fmt.Sprintf("finished:%04d", i)), "finished", false)
		entry.setStatus(a2a.TaskStateCompleted, "")
		h.messages[fmt.Sprint(i)] = a2aRun{entry: entry}
	}
	if h.find(active.id) == nil || h.find("finished:0000") != nil || len(h.tasks) != a2aRegistryLimit || len(h.messages) != a2aRegistryLimit-1 {
		t.Fatal("eviction lost active task or retained orphan deduplication entry")
	}
	first, err := h.ListTasks(t.Context(), &a2a.ListTasksRequest{ContextID: "finished", Status: a2a.TaskStateCompleted, PageSize: 2})
	if err != nil || first.TotalSize != a2aRegistryLimit-1 || len(first.Tasks) != 2 || first.NextPageToken == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	second, err := h.ListTasks(t.Context(), &a2a.ListTasksRequest{ContextID: "finished", PageSize: 2, PageToken: first.NextPageToken})
	if err != nil || len(second.Tasks) != 2 || second.Tasks[0].ID == first.Tasks[1].ID {
		t.Fatalf("second page: %+v %v", second, err)
	}
	if _, err := h.ListTasks(t.Context(), &a2a.ListTasksRequest{PageToken: "invalid!"}); !errors.Is(err, a2a.ErrInvalidParams) {
		t.Fatalf("invalid page token: %v", err)
	}
}

func TestA2AHTTPBodyLimitAndVersionRouting(t *testing.T) {
	s := &Server{}
	for _, version := range []string{"0.3", "1.0"} {
		method := "tasks/get"
		if strings.HasPrefix(version, "1.") {
			method = "GetTask"
		}
		body := mcpRequest(1, method, map[string]any{"id": "missing"})
		c, rec := remoteContext(http.MethodPost, "/a2a", body, map[string]string{"A2A-Version": version})
		s.handleA2A(contextForTest(c), c)
		flushTestResponse(c)
		if !strings.Contains(rec.Body.String(), "task not found") {
			t.Fatalf("version %s: %s", version, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"reason":"TASK_NOT_FOUND"`) {
			t.Fatal("missing structured error reason", rec.Body.String())
		}
	}
	// The bounded reader is shared by both versions; test its oversized JSON
	// prefix once instead of duplicating a 40 MiB allocation for each binding.
	body := mcpRequest(1, "GetTask", map[string]any{"id": "missing"})
	c, rec := remoteContext(http.MethodPost, "/a2a", body+strings.Repeat(" ", agentMaxBodyBytes), map[string]string{"A2A-Version": "1.0"})
	c.Request.Header.SetContentLength(-1)
	s.handleA2A(contextForTest(c), c)
	flushTestResponse(c)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body accepted (%d)", rec.Code)
	}
	for _, tc := range []struct {
		body string
		code int
	}{{"{", -32700}, {"[]", -32600}} {
		c, rec := remoteContext(http.MethodPost, "/a2a", tc.body, map[string]string{"A2A-Version": "1.0"})
		s.handleA2A(contextForTest(c), c)
		flushTestResponse(c)
		if !strings.Contains(rec.Body.String(), fmt.Sprintf(`"code":%d`, tc.code)) {
			t.Fatal(rec.Body.String())
		}
	}
}

// closes the HTTP stream while the engine is still executing
// Empty new messages must be rejected before creating a session or running
// a model turn that cannot produce the user-message sequence used as task ID.
func TestA2ADisconnectAndConcurrentDuplicateSend(t *testing.T) {
	s := newAgentIntegrationServer(t)
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() {
		close(release)
	})
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			openAIChunk("reply", map[string]any{"role": "assistant", "content": "detached reply"}, "", nil),
			openAIChunk("reply", map[string]any{}, "stop", nil), openAIDone(),
		} {
			_, _ = w.Write([]byte(chunk))
		}
	}))
	defer func() {
		unblock()
		upstream.Close()
	}()
	seedAgentModel(t, s, upstream.URL)
	session, err := s.createRemoteAgentSession(t.Context(), "disconnect", agent.ModeCreate, "", &agent.Settings{ModelSourceID: "s1", ModelName: "fake-model"})
	if err != nil {
		t.Fatal(err)
	}
	router := newTestEngine(t)
	router.POST("/a2a", s.handleA2A)
	server := startTestServer(router)
	defer func() {
		unblock()
		server.Close()
	}()
	client := a2aclient.NewJSONRPCTransport(server.URL+"/a2a", server.Client())
	defer client.Destroy()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	headers := a2aclient.ServiceParams{"A2A-Version": {string(a2a.Version)}}
	message := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
	message.ContextID = session.ID
	req := &a2a.SendMessageRequest{Message: message, Config: &a2a.SendMessageConfig{ReturnImmediately: true}}
	var taskID a2a.TaskID
	for event, err := range client.SendStreamingMessage(ctx, headers, req) {
		if err != nil {
			t.Fatal(err)
		}
		taskID = event.TaskInfo().TaskID
		break
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("model did not start")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, err := client.SendMessage(ctx, headers, req)
			if err != nil {
				t.Error(err)
				return
			}
			if result.TaskInfo().TaskID != taskID {
				t.Errorf("duplicate message created task %s", result.TaskInfo().TaskID)
			}
		})
	}
	wg.Wait()
	unblock()
	completed := false
	for event, err := range client.SubscribeToTask(ctx, headers, &a2a.SubscribeToTaskRequest{ID: taskID}) {
		if err != nil {
			t.Fatal(err)
		}
		if status, ok := event.(*a2a.TaskStatusUpdateEvent); ok && status.Status.State == a2a.TaskStateCompleted {
			completed = true
		}
	}
	if !completed || calls.Load() != 1 {
		t.Fatalf("detached execution: completed=%v calls=%d", completed, calls.Load())
	}
	_, err = client.SendMessage(ctx, headers, &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"approved": true}))})
	if !errors.Is(err, a2a.ErrInvalidParams) || calls.Load() != 1 {
		t.Fatalf("empty new task: %v calls=%d", err, calls.Load())
	}
}
