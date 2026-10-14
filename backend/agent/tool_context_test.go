package agent

import (
	"context"
	"errors"
	"testing"
)

type failingSessionStore struct {
	Store
	failure error
}

func (s failingSessionStore) UpdateSessionState(context.Context, string, SessionStateUpdate) error {
	return s.failure
}

func TestToolTargetChangesOnlyAfterPersistence(t *testing.T) {
	session := &Session{ID: "session", TestBaseURL: "https://before.invalid", TestAPIKey: "before-secret"}
	failure := errors.New("injected write failure")
	toolContext := &engineToolContext{ctx: t.Context(), session: session, store: failingSessionStore{failure: failure}}
	if err := toolContext.SetTestTarget("https://after.invalid", "after-secret"); !errors.Is(err, failure) {
		t.Fatalf("persistence cause lost: %v", err)
	}
	if baseURL, apiKey := toolContext.TestTarget(); baseURL != "https://before.invalid" || apiKey != "before-secret" {
		t.Fatal("failed save published an uncommitted target")
	}
	storedSession := *session
	store := newFakeStore().seed(&storedSession)
	toolContext.store = store
	if err := toolContext.SetTestTarget("https://after.invalid", ""); err != nil {
		t.Fatal(err)
	}
	if baseURL, apiKey := toolContext.TestTarget(); baseURL != storedSession.TestBaseURL || apiKey != storedSession.TestAPIKey || apiKey != "before-secret" {
		t.Fatal("partial target update did not retain omitted credentials")
	}
}
