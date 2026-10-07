package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

const cacheFollowupAllowance = 20

func (suite *liveSuite) runCacheFollowup(t *testing.T) {
	parent := os.Getenv("ELYSIA_CACHE_PARENT")
	raw, err := os.ReadFile(filepath.Join(parent, "cache-checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var prior cacheCheckpoint
	if err := json.Unmarshal(raw, &prior); err != nil {
		t.Fatal(err)
	}
	if prior.Origin != suite.Origin || prior.StartedAt.IsZero() || suite.budget.Limit != cacheValidationLimit || suite.budget.CleanupReserve != cacheCleanupReserve {
		t.Fatal("followup budget or target does not match its parent")
	}
	for _, task := range prior.Tasks {
		if task.State == "waiting" {
			t.Fatal("parent still has pending timing observations")
		}
	}
	if suite.budget.Stopped != "" || !time.Now().Before(suite.budget.Deadline) {
		t.Fatal("parent budget is stopped or expired")
	}
	suite.Limit = cacheValidationLimit
	suite.PreflightEvidence = filepath.Join(parent, "live.json")
	raw, err = os.ReadFile(suite.PreflightEvidence)
	if err != nil {
		t.Fatal(err)
	}
	suite.PreflightHash = liveHash(raw)
	var evidence liveSuite
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	for id, profile := range suite.Targets {
		if evidence.Targets[id] != profile {
			t.Fatal("followup cannot change parent targets", id)
		}
	}
	if suite.budget.FollowupCallsAtStart != nil {
		t.Fatal("followup was already started; refusing another allowance or uncertain replay")
	}
	start := suite.budget.Calls
	suite.budget.FollowupCallsAtStart = &start
	if err := suite.budget.save(); err != nil {
		t.Fatal(err)
	}
	run := &cacheLiveRun{suite: suite, path: filepath.Join(filepath.Dir(suite.path), "cache-checkpoint.json"), compiled: map[string]*protocol.Compiled{}, gateways: map[string]*liveGateway{}, checkpoint: cacheCheckpoint{SchemaVersion: 1, Compiler: protocol.CompilerVersion, Origin: suite.Origin, StartedAt: time.Now().UTC(), Attempts: map[string]string{}}}
	run.save(t)
	for _, target := range liveProtocolIDs {
		suite.selectTarget(t, target)
		compiled := compileFixtureDefinition(t, presetDefinition(t, target))
		run.compiled[target] = compiled
		if report := protocol.Verify(t.Context(), compiled); !report.Passed {
			t.Fatal(report.Issues)
		}
		gateway := newLiveGateway(t, suite, compiled)
		run.gateways[target] = gateway
		definition := compiled.Definition()
		definition.ID = "cache-followup-copy-" + target
		activateGatewayDefinition(t, gateway.server, definition)
		service, err := gateway.server.protocolService()
		if err != nil {
			t.Fatal(err)
		}
		copy, ok := service.Pin(definition.ID)
		if !ok {
			t.Fatal("custom ingress missing")
		}
		gateway.bind(t, compiled.Definition().Capabilities)
		levels := []int{1}
		if target == "google-generate-content" {
			levels = []int{2, 4}
		}
		for _, level := range levels {
			for _, route := range []string{"direct", "gateway"} {
				for repeat := range 2 {
					if suite.budget.Calls-suite.CallsAtStart >= cacheFollowupAllowance {
						t.Fatal("followup allowance exhausted")
					}
					id := fmt.Sprintf("followup/%s/%d/%d", route, level, repeat)
					result := run.once(t, target, id, func() liveCase {
						model := "grp"
						if route == "direct" {
							model = suite.Model
						}
						isStream := repeat == 1 || (route == "gateway" && target != "google-generate-content")
						request := liveRequest(t, model, isStream)
						prefix := run.prefix(target+"/"+route+fmt.Sprint(level)) + strings.Repeat("stable alpha beta gamma delta reference information.\n", (level-1)*cachePrefixTokens/8)
						request.Content[0].Children[0].Payload = protocol.StringValue(prefix)
						if target == "anthropic-messages" {
							request.Content[0].Children[0].Cache = []protocol.CacheIntent{{Kind: "breakpoint", Location: "block", Value: mustProtocolValue(t, `{"type":"ephemeral"}`), TTL: protocol.StringValue("5m")}}
						}
						var result liveCase
						if route == "direct" {
							result, _ = suite.direct(t, id, compiled, request, isStream)
						} else {
							result, _ = gateway.request(t, id, copy, request, isStream)
						}
						return result
					})
					if result.Status != "passed" {
						break
					}
				}
			}
		}
	}
}
