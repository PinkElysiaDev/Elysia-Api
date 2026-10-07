package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func TestCacheCheckpointNeverReplaysUncertainWarmup(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("ELYSIA_VERIFY_TEST_KEY", "synthetic-secret")
	suite := &liveSuite{Targets: map[string]liveTargetProfile{"anthropic-messages": {Model: "m", KeyEnv: "ELYSIA_VERIFY_TEST_KEY"}}, path: filepath.Join(directory, "live.json"), budget: &liveBudget{path: filepath.Join(directory, "budget.json")}}
	run := &cacheLiveRun{suite: suite, path: filepath.Join(directory, "checkpoint.json"), checkpoint: cacheCheckpoint{Attempts: map[string]string{"anthropic-messages/warm": "started"}}}
	result := run.once(t, "anthropic-messages", "warm", func() liveCase { t.Fatal("uncertain paid request retried"); return liveCase{} })
	if result.Status != "inconclusive" || suite.budget.Calls != 0 {
		t.Fatal("uncertain state changed", result)
	}
	if _, err := os.Stat(suite.path); err != nil {
		t.Fatal("uncertainty not checkpointed", err)
	}
	if err := saveCacheCheckpoint(run.path, &run.checkpoint); err != nil {
		t.Fatal(err)
	}
	var restored cacheCheckpoint
	raw, _ := os.ReadFile(run.path)
	if err := json.Unmarshal(raw, &restored); err != nil || restored.Attempts["anthropic-messages/warm"] != "started" {
		t.Fatal("attempt identity lost", err)
	}
}

func TestCacheTimingDistinguishesObservationFromCompletedProbe(t *testing.T) {
	start := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	task := cacheCheckpointTask{TTL: "1h", StartedAt: start}
	for _, fixture := range []struct {
		elapsed        time.Duration
		read, creation int64
		want           string
	}{
		{55 * time.Minute, 0, 4635, "no_read_observed_before_minimum_lifetime"},
		{55 * time.Minute, 4624, 11, "read_observed_before_minimum_lifetime"},
		{65 * time.Minute, 0, 4635, "recreation_observed_after_minimum_lifetime"},
		{65 * time.Minute, 4635, 0, "read_observed_after_minimum_lifetime"},
		{65 * time.Minute, 0, 0, "zero_read_without_observed_recreation_after_minimum_lifetime"},
	} {
		result := liveCase{Status: "passed", Wire: liveWireEvidence{StartedAt: start.Add(fixture.elapsed)}, UpstreamUsage: &protocol.Usage{CacheRead: &protocol.Counter{Count: fixture.read}, CacheCreation: &protocol.Counter{Count: fixture.creation}}}
		if got := classifyCacheTiming(task, result); got != fixture.want {
			t.Fatal(got, fixture.want)
		}
	}
	if got := classifyCacheTiming(task, liveCase{Status: "passed"}); got != "observation_inconclusive" {
		t.Fatal("HTTP success substituted for observed counters", got)
	}
}

func TestStandardCacheCreationReachesNativeCopiesAndStorage(t *testing.T) {
	for _, fixture := range cacheWireFixtures() {
		if fixture.id != "openai-chat-completions" && fixture.id != "openai-responses" {
			continue
		}
		for _, copyID := range []bool{false, true} {
			t.Run(fixture.id+"/copy="+map[bool]string{false: "false", true: "true"}[copyID], func(t *testing.T) {
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					wire := fixture.response
					if strings.Contains(string(body), `"stream":true`) {
						w.Header().Set("Content-Type", "text/event-stream")
						wire = fixture.stream
					}
					_, _ = io.WriteString(w, strings.ReplaceAll(wire, `"cached_tokens":70`, `"cached_tokens":70,"cache_write_tokens":20`))
				}))
				t.Cleanup(provider.Close)
				definition := presetDefinition(t, fixture.id)
				if copyID {
					definition.ID = "arbitrary-cache-copy-" + fixture.id
				}
				compiled := compileFixtureDefinition(t, definition)
				directory := t.TempDir()
				suite := &liveSuite{Model: "m", Origin: provider.URL, Compiler: protocol.CompilerVersion, StartedAt: time.Now(), key: "synthetic-secret", path: filepath.Join(directory, "live.json"), budget: &liveBudget{path: filepath.Join(directory, "budget.json")}, client: provider.Client()}
				gateway := newLiveGateway(t, suite, compiled)
				for _, stream := range []bool{false, true} {
					result, _ := gateway.request(t, "creation", compiled, liveRequest(t, "grp", stream), stream)
					if result.Status != "passed" || result.StoredUsage.CacheCreation == nil || result.StoredUsage.CacheCreation.Count != 20 || result.DownstreamUsage.CacheCreation.Count != 20 {
						t.Fatalf("creation mapping lost: %+v", result)
					}
					if result.StoredTotals["cacheHitRate"].(float64) != 0.7 {
						t.Fatal("aggregate denominator changed", result.StoredTotals)
					}
				}
			})
		}
	}
}
