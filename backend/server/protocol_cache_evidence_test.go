package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

const cacheValidationLimit = 96
const cacheCleanupReserve = 4
const cacheValidationWindow = 2 * time.Hour

type liveTargetProfile struct {
	Model  string `json:"model"`
	KeyEnv string `json:"keyEnv"`
}

func readLiveTargetProfiles(raw string) (map[string]liveTargetProfile, error) {
	if raw == "" {
		return nil, nil
	}
	profiles := map[string]liveTargetProfile{}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&profiles); err != nil {
		return nil, err
	}
	for id, profile := range profiles {
		if !slices.Contains(liveProtocolIDs, id) || profile.Model == "" || !strings.HasPrefix(profile.KeyEnv, "ELYSIA_VERIFY_") || strings.ContainsAny(profile.KeyEnv, "=\n\r ") {
			return nil, fmt.Errorf("invalid live target profile %q", id)
		}
	}
	return profiles, nil
}

func (suite *liveSuite) selectTarget(t *testing.T, id string) {
	t.Helper()
	profile, exists := suite.Targets[id]
	if !exists {
		t.Fatal("live target profile missing", id)
	}
	key := os.Getenv(profile.KeyEnv)
	if key == "" {
		t.Fatal("credential environment is absent for", id)
	}
	suite.Model, suite.key = profile.Model, key
}

// A successful forwarding assertion is independent of observing a cache hit.
type liveCacheAssessment struct {
	Availability string         `json:"availability"`
	Fidelity     string         `json:"fidelity"`
	Read         string         `json:"read"`
	Creation     string         `json:"creation"`
	Timing       string         `json:"timing"`
	Contract     string         `json:"contract"`
	BillingUsage map[string]any `json:"billingUsage,omitempty"`
	Conflicts    []string       `json:"conflicts,omitempty"`
}

func assessLiveCache(result liveCase) *liveCacheAssessment {
	a := &liveCacheAssessment{Availability: "unconfirmed", Fidelity: "unconfirmed", Read: "missing", Creation: "missing", Timing: "not_run", Contract: "standard_wire"}
	if result.Wire.Status == 200 {
		a.Availability = "available"
	}
	if result.Status == "passed" {
		a.Fidelity = "passed"
	}
	if result.Status == "failed" {
		a.Fidelity = "failed"
	}
	observe := func(counter *protocol.Counter) string {
		if counter == nil {
			return "missing"
		}
		if counter.Count == 0 {
			return "observed_zero"
		}
		return "observed_nonzero"
	}
	if result.UpstreamUsage != nil {
		a.Read, a.Creation = observe(result.UpstreamUsage.CacheRead), observe(result.UpstreamUsage.CacheCreation)
	}
	merged := mergeLiveUsageFrames(result.Wire.RawUsage)
	if billing, ok := merged["billing_usage"].(map[string]any); ok {
		a.BillingUsage = billing
		if billing["semantic"] == "openai" {
			if counters, ok := billing["openai_usage"].(map[string]any); ok {
				u, err := referenceLiveUsage("openai-chat-completions", []map[string]any{counters})
				if err != nil {
					a.Conflicts = append(a.Conflicts, "invalid_billing_usage")
				} else if result.ReferenceUsage != nil {
					for _, pair := range []struct {
						name            string
						native, billing *protocol.Counter
					}{{"input", result.ReferenceUsage.Input, u.Input}, {"cacheRead", result.ReferenceUsage.CacheRead, u.CacheRead}} {
						if pair.native != nil && pair.billing != nil && pair.native.Count != pair.billing.Count {
							a.Conflicts = append(a.Conflicts, pair.name+"_disagrees_with_billing")
						}
					}
				}
			}
		}
	}
	return a
}

// Merge wire components before normalization. Cache fields can arrive in a
// different frame from the uncached subtotal; absence never overwrites zero.
func mergeLiveUsageFrames(frames []map[string]any) map[string]any {
	merged := map[string]any{}
	for _, frame := range frames {
		for key, value := range frame {
			if object, ok := value.(map[string]any); ok {
				before, _ := merged[key].(map[string]any)
				merged[key] = mergeLiveUsageFrames([]map[string]any{before, object})
			} else {
				merged[key] = value
			}
		}
	}
	return merged
}

type cacheCheckpoint struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Compiler      string                `json:"compiler"`
	Origin        string                `json:"origin"`
	StartedAt     time.Time             `json:"startedAt"`
	Tasks         []cacheCheckpointTask `json:"tasks"`
	Attempts      map[string]string     `json:"attempts"`
}

type cacheCheckpointTask struct {
	ID           string    `json:"id"`
	Target       string    `json:"target"`
	Model        string    `json:"model"`
	Revision     string    `json:"revision"`
	Route        string    `json:"route"`
	TTL          string    `json:"ttl,omitempty"`
	Prefix       string    `json:"prefix,omitempty"`
	State        string    `json:"state"`
	StartedAt    time.Time `json:"startedAt,omitempty"`
	DueAt        time.Time `json:"dueAt,omitempty"`
	Resource     string    `json:"resource,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	DelaySeconds int       `json:"delaySeconds,omitempty"`
}

func saveCacheCheckpoint(path string, checkpoint *cacheCheckpoint) error {
	raw, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func TestCacheValidationBudgetReservesCleanupAndPersistsAcrossTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.json")
	budget := &liveBudget{path: path, Limit: cacheValidationLimit, CleanupReserve: cacheCleanupReserve, Calls: 91}
	if err := budget.reserve(); err != nil {
		t.Fatal(err)
	}
	if err := budget.reserve(); err == nil {
		t.Fatal("generation consumed cleanup reserve")
	}
	raw, _ := os.ReadFile(path)
	restored := &liveBudget{path: path}
	if err := json.Unmarshal(raw, restored); err != nil {
		t.Fatal(err)
	}
	for range cacheCleanupReserve {
		if err := restored.reserveOperation(true); err != nil {
			t.Fatal(err)
		}
	}
	if err := restored.reserveOperation(true); err == nil {
		t.Fatal("cleanup exceeded total allowance")
	}
	budget.Calls = 0
	budget.Deadline = time.Now().Add(-time.Second)
	if err := budget.reserve(); err == nil {
		t.Fatal("expired run sent a request")
	}
}

func TestCacheEvidenceDistinguishesBillingConflictAndUnknownCreation(t *testing.T) {
	fields := []map[string]any{{"input_tokens": json.Number("4421"), "cache_read_input_tokens": json.Number("4224"), "billing_usage": map[string]any{"semantic": "openai", "openai_usage": map[string]any{"prompt_tokens": json.Number("4421")}}}}
	u, err := referenceLiveUsage("anthropic-messages", fields)
	if err != nil {
		t.Fatal(err)
	}
	a := assessLiveCache(liveCase{Status: "passed", Wire: liveWireEvidence{Status: 200, RawUsage: fields}, UpstreamUsage: u, ReferenceUsage: u})
	if a.Read != "observed_nonzero" || a.Creation != "missing" || len(a.Conflicts) != 1 || a.Timing != "not_run" {
		t.Fatalf("%+v", a)
	}
}

func TestLiveTargetProfilesContainOnlyCredentialReferences(t *testing.T) {
	profiles, err := readLiveTargetProfiles(`{"anthropic-messages":{"model":"claude-haiku-4-5","keyEnv":"ELYSIA_VERIFY_ANTHROPIC_KEY"}}`)
	if err != nil || profiles["anthropic-messages"].Model != "claude-haiku-4-5" {
		t.Fatal(err)
	}
	if _, err := readLiveTargetProfiles(`{"anthropic-messages":{"model":"m","key":"secret"}}`); err == nil {
		t.Fatal("secret config accepted")
	}
}
