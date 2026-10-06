package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

const cachePrefixTokens = 4096 // Actual provider usage, not this estimate, decides cache eligibility.
const cacheScheduleTick = time.Second

type cacheLiveRun struct {
	suite      *liveSuite
	checkpoint cacheCheckpoint
	path       string
	compiled   map[string]*protocol.Compiled
	gateways   map[string]*liveGateway
}

func (suite *liveSuite) scope() protocol.Scope {
	return protocol.Scope{Provider: suite.Origin, Account: "verification-account", Model: suite.Model}
}

func (suite *liveSuite) runCacheGaps(t *testing.T) {
	run := &cacheLiveRun{suite: suite, path: filepath.Join(filepath.Dir(suite.path), "cache-checkpoint.json"), compiled: map[string]*protocol.Compiled{}, gateways: map[string]*liveGateway{}}
	run.open(t)
	available := map[string]bool{}
	for _, id := range []string{"anthropic-api", "gemini-api", "chat-completions-api", "responses-api"} {
		suite.selectTarget(t, id)
		run.compiled[id] = compileFixtureDefinition(t, presetDefinition(t, id))
		if report := protocol.Verify(t.Context(), run.compiled[id]); !report.Passed {
			t.Fatal(report.Issues)
		}
		isAvailable := true
		for _, stream := range []bool{false, true} {
			result := run.once(t, id, fmt.Sprintf("preflight/%t", stream), func() liveCase {
				result, _ := suite.direct(t, fmt.Sprintf("preflight/%t", stream), run.compiled[id], liveRequest(t, suite.Model, stream), stream)
				return result
			})
			isAvailable = isAvailable && result.Status == "passed"
		}
		available[id] = isAvailable
		if isAvailable {
			run.gateways[id] = newLiveGateway(t, suite, run.compiled[id])
			run.gateways[id].bind(t, run.compiled[id].Definition().Capabilities)
		}
	}
	if available["anthropic-api"] {
		run.prepareTTL(t)
	}
	if available["gemini-api"] {
		run.prepareResources(t)
	}
	for _, id := range liveProtocolIDs {
		if !available[id] {
			continue
		}
		run.cacheSequence(t, id)
		run.dueTasks(t)
	}
	for run.hasPending() && time.Now().Before(suite.budget.Deadline) && suite.budget.Stopped == "" {
		run.dueTasks(t)
		select {
		case <-t.Context().Done():
			run.cleanup(t)
			return
		case <-time.After(cacheScheduleTick):
		}
	}
	run.cleanup(t)
	for _, task := range run.checkpoint.Tasks {
		if task.State == "waiting" {
			suite.selectTarget(t, task.Target)
			suite.record(t, liveCase{ID: task.ID + "/timing", Target: task.Target, Revision: task.Revision, Status: "not_run", Reason: "run ended before scheduled observation"})
		}
	}
}

func (run *cacheLiveRun) open(t *testing.T) {
	suite := run.suite
	if len(suite.Targets) != len(liveProtocolIDs) {
		t.Fatal("cache gaps require four explicit target profiles")
	}
	if suite.budget.Limit != 0 && suite.budget.Limit != cacheValidationLimit {
		t.Fatal("refusing to reuse another experiment's budget")
	}
	raw, err := os.ReadFile(run.path)
	if err == nil {
		if err := json.Unmarshal(raw, &run.checkpoint); err != nil {
			t.Fatal(err)
		}
		if run.checkpoint.Compiler != protocol.CompilerVersion || run.checkpoint.Origin != suite.Origin {
			t.Fatal("checkpoint contract changed")
		}
		var prior liveSuite
		raw, err := os.ReadFile(suite.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &prior); err != nil {
			t.Fatal(err)
		}
		for id, profile := range suite.Targets {
			if prior.Targets[id] != profile {
				t.Fatal("checkpoint target profile changed", id)
			}
		}
		suite.Cases = prior.Cases
	} else if os.IsNotExist(err) {
		if suite.budget.Calls != 0 {
			t.Fatal("nonempty budget requires its original checkpoint; refusing to restart warmups")
		}
		run.checkpoint = cacheCheckpoint{SchemaVersion: 1, Compiler: protocol.CompilerVersion, Origin: suite.Origin, StartedAt: time.Now().UTC(), Attempts: map[string]string{}}
		suite.budget.Deadline = run.checkpoint.StartedAt.Add(cacheValidationWindow)
	} else {
		t.Fatal(err)
	}
	suite.StartedAt = run.checkpoint.StartedAt
	suite.Limit, suite.budget.Limit, suite.budget.CleanupReserve = cacheValidationLimit, cacheValidationLimit, cacheCleanupReserve
	if err := suite.budget.save(); err != nil {
		t.Fatal(err)
	}
	run.save(t)
}

func (run *cacheLiveRun) save(t *testing.T) {
	t.Helper()
	if err := saveCacheCheckpoint(run.path, &run.checkpoint); err != nil {
		t.Fatal(err)
	}
}

// A started submission without durable response evidence is uncertain. Resume
// never retries it, even if that means a timing cohort cannot be completed.
func (run *cacheLiveRun) once(t *testing.T, target, id string, send func() liveCase) liveCase {
	t.Helper()
	run.suite.selectTarget(t, target)
	for _, result := range run.suite.Cases {
		if result.ID == id && result.Target == target {
			if result.Wire.ResponseEvidence != "" {
				raw, err := os.ReadFile(filepath.Join(filepath.Dir(run.suite.path), result.Wire.ResponseEvidence))
				if err != nil {
					t.Fatal(err)
				}
				result.Wire.body = raw
			}
			return result
		}
	}
	key := target + "/" + id
	if run.checkpoint.Attempts[key] != "" {
		result := liveCase{ID: id, Target: target, Status: "inconclusive", Reason: "previous submission uncertain; not replayed"}
		run.suite.record(t, result)
		return result
	}
	run.checkpoint.Attempts[key] = "started"
	run.save(t)
	result := send()
	run.suite.record(t, result)
	run.checkpoint.Attempts[key] = "finished"
	run.save(t)
	return result
}

func (run *cacheLiveRun) prefix(id string) string {
	return fmt.Sprintf("Cache verification %d %s.\n", run.checkpoint.StartedAt.UnixNano(), id) + strings.Repeat("stable alpha beta gamma delta reference information.\n", cachePrefixTokens/8)
}

func (run *cacheLiveRun) generate(t *testing.T, target, route, id, prefix, ttl string, stream bool, resource string) liveCase {
	return run.once(t, target, id, func() liveCase {
		model, scope := run.suite.Model, run.suite.scope()
		if route == "gateway" {
			model, scope = "grp", run.gateways[target].scope
		}
		request := liveRequest(t, model, stream)
		if resource != "" {
			request.Content = request.Content[1:]
			request.Cache = []protocol.CacheIntent{{Kind: "resource", Location: "request", Resource: &protocol.Resource{Kind: "cache", ID: protocol.StringValue(resource), Scope: scope}}}
		} else {
			request.Content[0].Children[0].Payload = protocol.StringValue(prefix)
			if ttl != "" {
				request.Content[0].Children[0].Cache = []protocol.CacheIntent{{Kind: "breakpoint", Location: "block", Value: mustProtocolValue(t, `{"type":"ephemeral"}`), TTL: protocol.StringValue(ttl)}}
			}
		}
		var result liveCase
		if route == "direct" {
			result, _ = run.suite.direct(t, id, run.compiled[target], request, stream)
		} else {
			result, _ = run.gateways[target].request(t, id, run.compiled[target], request, stream)
		}
		if result.Wire.Status == 200 && resource == "" {
			var wire map[string]any
			if err := json.Unmarshal(result.Wire.request, &wire); err != nil {
				t.Fatal(err)
			}
			encodedPrefix, _ := json.Marshal(prefix)
			if !bytes.Contains(result.Wire.request, encodedPrefix) {
				result.Status = "failed"
				result.Reason = "outbound cache prefix changed"
			}
		}
		return result
	})
}

func (run *cacheLiveRun) cacheSequence(t *testing.T, target string) {
	ttl := ""
	if target == "anthropic-api" {
		ttl = "5m"
	}
	for _, route := range []string{"direct", "gateway"} {
		for index, step := range []struct {
			cohort string
			stream bool
		}{{"A", false}, {"A", true}, {"B", true}, {"B", true}} {
			id := fmt.Sprintf("cache/%s/%d", route, index)
			result := run.generate(t, target, route, id, run.prefix(target+"/"+route+"/"+step.cohort), ttl, step.stream, "")
			run.dueTasks(t)
			if result.Status != "passed" {
				break
			}
		}
	}
}

func (run *cacheLiveRun) prepareTTL(t *testing.T) {
	for _, policy := range []struct {
		ttl           string
		before, after time.Duration
	}{{"5m", 4 * time.Minute, 7 * time.Minute}, {"1h", 55 * time.Minute, 65 * time.Minute}} {
		for _, route := range []string{"direct", "gateway"} {
			for _, cohort := range []struct {
				name  string
				delay time.Duration
			}{{"retention", policy.before}, {"quiet", policy.after}} {
				id := "ttl/" + policy.ttl + "/" + route + "/" + cohort.name
				if run.hasTask(id) {
					continue
				}
				prefix := run.prefix(id)
				result := run.generate(t, "anthropic-api", route, id+"/warm", prefix, policy.ttl, false, "")
				state := "unavailable"
				if result.Status == "passed" {
					state = "waiting"
				}
				run.checkpoint.Tasks = append(run.checkpoint.Tasks, cacheCheckpointTask{ID: id, Target: "anthropic-api", Model: run.suite.Model, Revision: run.compiled["anthropic-api"].Hash(), Route: route, TTL: policy.ttl, Prefix: prefix, State: state, StartedAt: result.Wire.StartedAt, DueAt: result.Wire.StartedAt.Add(cohort.delay), DelaySeconds: int(cohort.delay.Seconds())})
				run.save(t)
			}
		}
	}
}

func (run *cacheLiveRun) hasTask(id string) bool {
	for _, task := range run.checkpoint.Tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}
func (run *cacheLiveRun) hasPending() bool {
	for _, task := range run.checkpoint.Tasks {
		if task.State == "waiting" {
			return true
		}
	}
	return false
}

func (run *cacheLiveRun) dueTasks(t *testing.T) {
	indices := []int{}
	for i, task := range run.checkpoint.Tasks {
		if task.State == "waiting" && !time.Now().Before(task.DueAt) {
			indices = append(indices, i)
		}
	}
	sort.Slice(indices, func(i, j int) bool {
		return run.checkpoint.Tasks[indices[i]].DueAt.Before(run.checkpoint.Tasks[indices[j]].DueAt)
	})
	for _, index := range indices {
		task := run.checkpoint.Tasks[index]
		if task.Revision != run.compiled[task.Target].Hash() || task.Model != run.suite.Targets[task.Target].Model {
			t.Fatal("scheduled contract changed")
		}
		if task.Resource != "" {
			run.expireResource(t, task)
		} else {
			result := run.generate(t, task.Target, task.Route, task.ID+"/probe", task.Prefix, task.TTL, true, "")
			run.suite.selectTarget(t, task.Target)
			assessment := assessLiveCache(result)
			assessment.Timing = classifyCacheTiming(task, result)
			result.ID += "/timing"
			result.Assessment = assessment
			result.Reason = fmt.Sprintf("%d seconds after warm request start; a post-TTL hit does not disprove a minimum lifetime", int(result.Wire.StartedAt.Sub(task.StartedAt).Seconds()))
			run.suite.record(t, result)
		}
		run.checkpoint.Tasks[index].State = "observed"
		run.save(t)
	}
}

// Completing the scheduled request does not establish the claimed lifetime.
// Classify observations separately from forwarding and counter fidelity.
func classifyCacheTiming(task cacheCheckpointTask, result liveCase) string {
	lifetime, err := time.ParseDuration(task.TTL)
	if err != nil || result.Status != "passed" || result.UpstreamUsage == nil || result.UpstreamUsage.CacheRead == nil {
		return "observation_inconclusive"
	}
	hasRead := result.UpstreamUsage.CacheRead.Count > 0
	if result.Wire.StartedAt.Sub(task.StartedAt) < lifetime {
		if hasRead {
			return "read_observed_before_minimum_lifetime"
		}
		return "no_read_observed_before_minimum_lifetime"
	}
	if hasRead {
		return "read_observed_after_minimum_lifetime"
	}
	if creation := result.UpstreamUsage.CacheCreation; creation != nil && creation.Count > 0 {
		return "recreation_observed_after_minimum_lifetime"
	}
	return "zero_read_without_observed_recreation_after_minimum_lifetime"
}
