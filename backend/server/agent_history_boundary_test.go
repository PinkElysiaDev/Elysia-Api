package server

import (
	"net/http"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/storage"
)

func TestAgentHistoryRejectsInvalidBoundaryWithoutMutation(t *testing.T) {
	s := newAgentIntegrationServer(t)
	for _, query := range []string{"afterSeq=-1", "afterSeq=no", "afterSeq=", "afterSeq", "afterSeq=999999999999999999999999", "afterSeq=0&afterSeq=1", "afterSeq=1.5", "afterSeq=%20"} {
		t.Run(query, func(t *testing.T) {
			session, err := s.store.CreateAgentSession(t.Context(), storage.AgentSessionUpsert{Title: query})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.store.AppendMessage(t.Context(), session.ID, "user", map[string]string{"text": "keep history"}, "", nil); err != nil {
				t.Fatal(err)
			}
			status := agent.StatusWaitingApproval
			if err := s.store.UpdateSessionState(t.Context(), session.ID, agent.SessionStateUpdate{Status: &status, PendingAction: &agent.PendingAction{Kind: "question"}}); err != nil {
				t.Fatal(err)
			}
			before, err := s.store.GetAgentSession(t.Context(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			c, rec := agentContextWithID(http.MethodDelete, "/api/admin/agent/sessions/"+session.ID+"/messages?"+query, session.ID, "")
			s.adminClearAgentMessages(c)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
			}
			var failure struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := sonic.ConfigStd.Unmarshal(rec.Body.Bytes(), &failure); err != nil || failure.Error.Code != "invalid_after_seq" {
				t.Fatalf("unexpected error: %s", rec.Body.String())
			}
			after, err := s.store.GetAgentSession(t.Context(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			beforeJSON, _ := sonic.ConfigStd.Marshal(before)
			afterJSON, _ := sonic.ConfigStd.Marshal(after)
			messages, err := s.store.ListMessages(t.Context(), session.ID)
			if err != nil || len(messages) != 1 || string(beforeJSON) != string(afterJSON) {
				t.Fatalf("invalid boundary changed session/history: %v", err)
			}
		})
	}
}

func TestAgentHistoryValidBoundary(t *testing.T) {
	s := newAgentIntegrationServer(t)
	for _, test := range []struct {
		query     string
		remaining int
	}{{"", 0}, {"?afterSeq=0", 0}, {"?afterSeq=1", 1}} {
		t.Run(test.query, func(t *testing.T) {
			session, err := s.store.CreateAgentSession(t.Context(), storage.AgentSessionUpsert{Title: "truncate"})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := s.store.AppendMessage(t.Context(), session.ID, "user", map[string]string{"text": "history"}, "", nil); err != nil {
					t.Fatal(err)
				}
			}
			c, rec := agentContextWithID(http.MethodDelete, "/api/admin/agent/sessions/"+session.ID+"/messages"+test.query, session.ID, "")
			s.adminClearAgentMessages(c)
			messages, err := s.store.ListMessages(t.Context(), session.ID)
			if rec.Code != http.StatusOK || err != nil || len(messages) != test.remaining {
				t.Fatalf("truncate: %d %v messages=%d", rec.Code, err, len(messages))
			}
		})
	}
}
