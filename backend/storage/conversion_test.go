package storage

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func TestConversionRevisionCASAndSnapshot(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "conversion.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	policy := protocol.ConversionPolicy{SchemaVersion: 1, ID: "policy", Name: "policy", Rules: []protocol.ConversionRule{}}
	draft, err := s.SaveConversionDraft(t.Context(), policy, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveConversionDraft(t.Context(), policy, ""); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal(err)
	}
	if err = s.SaveConversionRevision(t.Context(), draft); err != nil {
		t.Fatal(err)
	}
	_, bindings, generation, err := s.ConversionSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ActivateConversion(t.Context(), policy.ID, draft.Hash, "", protocol.ConversionMatch{}, bindings, bindings, generation); err != nil {
		t.Fatal(err)
	}
	policies, _, next, err := s.ConversionSnapshot(t.Context())
	if err != nil || len(policies) != 1 || next <= generation {
		t.Fatal(policies, next, err)
	}
	if err = s.ActivateConversion(t.Context(), policy.ID, draft.Hash, draft.Hash, protocol.ConversionMatch{}, bindings, bindings, generation); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal("stale global generation accepted", err)
	}
	b := ProtocolBinding{Kind: "source", SourceID: "s", Unbound: true}
	if err = s.SaveProtocolBinding(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if err = s.ActivateConversion(t.Context(), policy.ID, draft.Hash, draft.Hash, protocol.ConversionMatch{}, bindings, bindings, next); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal("concurrent binding edit accepted", err)
	}
	// A malformed draft remains editable and never changes the active revision.
	policy.Mode = "unfinished"
	if _, err = s.SaveConversionDraft(t.Context(), policy, draft.Hash); err != nil {
		t.Fatal(err)
	}
	policies, _, _, err = s.ConversionSnapshot(t.Context())
	if err != nil || policies[0].Hash != draft.Hash || policies[0].Policy.Mode == "unfinished" {
		t.Fatal(policies, err)
	}
}

func TestContinuationQuotaExpiryAndSessionIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "continuations.db")
	key := []byte(strings.Repeat("k", 32))
	s, err := OpenWithKey(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	limits := protocol.ContinuationSettings{RetentionSeconds: 60, TurnsPerSession: 2, MaxBytes: 180, RecordBytes: 4096}
	codec, _ := protocol.NewContinuationCodec(key)
	_ = codec // Store accepts sealed envelopes; codec authenticity is checked on restore.
	for _, id := range []string{"one", "two", "three"} {
		r := StoredContinuation{ID: id, Owner: "owner", Session: "session", ScopeKey: "scope", Parent: id, Digest: "digest", Ciphertext: protocol.ContinuationPrefix + strings.Repeat("x", 50), ExpiresAt: time.Now().Unix() + 60}
		if err = s.SaveContinuation(t.Context(), r, limits); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := s.ContinuationStats(t.Context())
	if err != nil || stats["records"] != 2 || stats["bytes"] > limits.MaxBytes {
		t.Fatal(stats, err)
	}
	match := StoredContinuation{Owner: "owner", Session: "session", ScopeKey: "scope", Parent: "three", Digest: "digest"}
	found, err := s.FindContinuation(t.Context(), match)
	if err != nil || len(found) != 1 {
		t.Fatal(found, err)
	}
	match.Owner = "another"
	found, err = s.FindContinuation(t.Context(), match)
	if err != nil || len(found) != 0 {
		t.Fatal(found, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenWithKey(path, key)
	if err != nil {
		t.Fatal(err)
	}
	stats, err = s.ContinuationStats(t.Context())
	if err != nil || stats["records"] != 2 || stats["hits"] != 1 || stats["misses"] != 1 {
		t.Fatal(stats, err)
	}
	if err = s.ClearContinuations(t.Context(), ""); err == nil {
		t.Fatal("unscoped deletion accepted")
	}
	if err = s.ClearContinuations(t.Context(), "session"); err != nil {
		t.Fatal(err)
	}
	stats, err = s.ContinuationStats(t.Context())
	if err != nil || stats["records"] != 0 || stats["bytes"] != 0 {
		t.Fatal(stats, err)
	}
}
