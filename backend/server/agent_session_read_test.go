package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/elysia-api/backend/storage"
)

func TestAgentSessionReadErrorsKeepStorageFailure(t *testing.T) {
	s := newAgentIntegrationServer(t)
	session, err := s.store.CreateAgentSession(t.Context(), storage.AgentSessionUpsert{Title: "read failures"})
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"detail", "restore"} {
		for _, failure := range []string{"missing", "cancelled", "closed"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				id := session.ID
				want := http.StatusInternalServerError
				if failure == "missing" {
					id, want = "absent-session", http.StatusNotFound
				}
				c, rec := agentContextWithID(http.MethodGet, "/api/admin/agent/sessions/"+id, id, "")
				if failure == "cancelled" {
					ctx, cancel := context.WithCancel(c.Request.Context())
					cancel()
					c.Request = c.Request.WithContext(ctx)
				}
				if failure == "closed" {
					// Use an independent instance so later missing/cancelled cases still read a live store.
					isolated := newAgentIntegrationServer(t)
					if err := isolated.store.Close(); err != nil {
						t.Fatal(err)
					}
					if operation == "detail" {
						isolated.adminGetAgentSession(c)
					} else {
						isolated.adminRestoreAgentDraft(c)
					}
				} else if operation == "detail" {
					s.adminGetAgentSession(c)
				} else {
					s.adminRestoreAgentDraft(c)
				}
				if rec.Code != want {
					t.Fatalf("got %d want %d: %s", rec.Code, want, rec.Body.String())
				}
				if failure != "missing" && (!strings.Contains(rec.Body.String(), "session_read_failed") || strings.Contains(rec.Body.String(), `"code":"not_found"`)) {
					t.Fatal(rec.Body.String())
				}
			})
		}
	}
}
