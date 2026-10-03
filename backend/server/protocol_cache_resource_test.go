package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

const cacheResourceTTL = 10 * time.Minute
const cacheResourceGrace = 90 * time.Second

var cacheResourceName = regexp.MustCompile(`^cachedContents/[A-Za-z0-9_-]+$`)

type cacheResourceReceipt struct {
	Name          string          `json:"name"`
	Model         string          `json:"model"`
	ExpireTime    time.Time       `json:"expireTime"`
	UsageMetadata json.RawMessage `json:"usageMetadata"`
}

func (run *cacheLiveRun) resourceOperation(t *testing.T, id, method, path string, body []byte, isCleanup bool) liveCase {
	return run.once(t, "gemini-api", id, func() liveCase {
		operation := protocol.Operation{Kind: "generate", Method: method, Path: path, Transport: protocol.HTTPJSON, Auth: run.compiled["gemini-api"].Operations()["generate"].Auth}
		wire := run.suite.exchangeOperation(t.Context(), run.suite.Origin, operation, body, false, nil, isCleanup)
		result := liveCase{ID: id, Target: "gemini-api", Revision: run.compiled["gemini-api"].Hash(), Status: "inconclusive", Wire: wire, Reason: wire.Error}
		if wire.Status >= 200 && wire.Status < 300 {
			result.Status = "passed"
		}
		result.Assessment = assessLiveCache(result)
		result.Assessment.Contract = "gemini_cached_content_resource"
		return result
	})
}

func decodeResourceReceipt(raw []byte, model string) (cacheResourceReceipt, error) {
	var receipt cacheResourceReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return receipt, err
	}
	if !cacheResourceName.MatchString(receipt.Name) || receipt.Model != "models/"+model || receipt.ExpireTime.IsZero() {
		return receipt, fmt.Errorf("resource identity, model or expireTime violates the declared contract")
	}
	return receipt, nil
}

func (run *cacheLiveRun) prepareResources(t *testing.T) {
	for _, route := range []string{"direct", "gateway"} {
		id := "resource/" + route
		if run.hasTask(id) {
			continue
		}
		body, err := json.Marshal(map[string]any{"model": "models/" + run.suite.Targets["gemini-api"].Model, "displayName": fmt.Sprintf("elysia-verification-%d-%s", run.checkpoint.StartedAt.Unix(), route), "ttl": fmt.Sprintf("%ds", int(cacheResourceTTL.Seconds())), "contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": run.prefix(id)}}}}})
		if err != nil {
			t.Fatal(err)
		}
		result := run.resourceOperation(t, id+"/create", http.MethodPost, "/v1beta/cachedContents", body, false)
		if result.Status != "passed" {
			return
		} // Do not retry an uncertain or unsupported creation.
		receipt, err := decodeResourceReceipt(result.Wire.body, run.suite.Model)
		if err != nil {
			if cacheResourceName.MatchString(receipt.Name) {
				// A malformed receipt can still identify our newly created resource.
				// Retain only that identity for cleanup; it cannot authorize reuse.
				run.checkpoint.Tasks = append(run.checkpoint.Tasks, cacheCheckpointTask{ID: id, Target: "gemini-api", Model: run.suite.Model, Revision: run.compiled["gemini-api"].Hash(), Resource: receipt.Name, ExpiresAt: receipt.ExpireTime, State: "invalid_contract"})
				run.save(t)
			}
			result.ID += "/contract"
			result.Status = "failed"
			result.Reason = err.Error()
			run.suite.record(t, result)
			return
		}
		run.checkpoint.Tasks = append(run.checkpoint.Tasks, cacheCheckpointTask{ID: id, Target: "gemini-api", Model: run.suite.Model, Revision: run.compiled["gemini-api"].Hash(), Route: route, State: "waiting", StartedAt: result.Wire.StartedAt, DueAt: receipt.ExpireTime.Add(cacheResourceGrace), Resource: receipt.Name, ExpiresAt: receipt.ExpireTime})
		run.save(t)
		inspection := run.resourceOperation(t, id+"/get", http.MethodGet, "/v1beta/"+receipt.Name, nil, false)
		if inspection.Status != "passed" {
			continue
		}
		for _, stream := range []bool{false, true} {
			result := run.generate(t, "gemini-api", route, fmt.Sprintf("%s/use/%t", id, stream), "", "", stream, receipt.Name)
			if result.Status != "passed" {
				break
			}
			var request map[string]any
			if err := json.Unmarshal(result.Wire.request, &request); err != nil {
				t.Fatal(err)
			}
			if request["cachedContent"] != receipt.Name || request["systemInstruction"] != nil || request["tools"] != nil {
				t.Fatal("resource reference or cached context changed")
			}
		}
	}
}

func (run *cacheLiveRun) expireResource(t *testing.T, task cacheCheckpointTask) {
	inspection := run.resourceOperation(t, task.ID+"/expired-get", http.MethodGet, "/v1beta/"+task.Resource, nil, false)
	use := run.generate(t, "gemini-api", task.Route, task.ID+"/expired-use", "", "", false, task.Resource)
	result := liveCase{ID: task.ID + "/expiry", Target: task.Target, Revision: task.Revision, Status: "inconclusive", Reason: "resource expiry was not established"}
	if (inspection.Wire.Status == 404 || inspection.Wire.Status == 410) && (use.Wire.Status == 400 || use.Wire.Status == 404 || use.Wire.Status == 410) {
		// Authentication, quota and model failures are not expiry evidence.
		lower := strings.ToLower(use.Wire.Error)
		if strings.Contains(lower, "cache") || strings.Contains(lower, "expir") {
			result.Status = "passed"
			result.Reason = "expired resource unavailable and generation reference rejected"
		}
	}
	result.Assessment = assessLiveCache(result)
	result.Assessment.Timing = result.Status
	result.Assessment.Contract = "gemini_cached_content_expiration"
	run.suite.record(t, result)
}

func (run *cacheLiveRun) cleanup(t *testing.T) {
	for index, task := range run.checkpoint.Tasks {
		if task.Resource == "" || task.State == "cleaned" {
			continue
		}
		result := run.resourceOperation(t, task.ID+"/delete", http.MethodDelete, "/v1beta/"+task.Resource, nil, true)
		if result.Wire.Status == 200 || result.Wire.Status == 204 || result.Wire.Status == 404 || result.Wire.Status == 410 {
			run.checkpoint.Tasks[index].State = "cleaned"
			run.save(t)
		}
	}
}

func TestCacheResourceReceiptRejectsForeignModelAndUntrustedPath(t *testing.T) {
	for _, raw := range []string{`{"name":"https://other/resource","model":"models/m","expireTime":"2026-10-03T00:00:00Z"}`, `{"name":"cachedContents/id","model":"models/foreign","expireTime":"2026-10-03T00:00:00Z"}`} {
		if _, err := decodeResourceReceipt([]byte(raw), "m"); err == nil {
			t.Fatal("foreign resource accepted")
		}
	}
}
