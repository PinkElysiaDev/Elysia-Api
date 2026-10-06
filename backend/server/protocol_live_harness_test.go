package server

import (
	"bytes"
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

func TestLiveInspectorSuppliesResourceProvenance(t *testing.T) {
	compiled := compileFixtureDefinition(t, presetDefinition(t, "responses-api"))
	wire := []byte(`{"id":"r","status":"completed","output":[{"type":"reasoning","id":"reason","summary":[],"encrypted_content":"synthetic-encrypted-payload"}],"usage":{"input_tokens":100,"output_tokens":2,"input_tokens_details":{"cached_tokens":70}}}`)
	result := liveCase{Wire: liveWireEvidence{body: wire, RawUsage: readLiveUsage(wire, liveOperation(compiled, false), false)}}
	response, err := inspectLiveWire(compiled, &result)
	if err != nil {
		t.Fatal(err)
	}
	if response.Usage.CacheRead.Count != 70 || response.Content[0].Resources[0].Scope != liveInspectionScope() {
		t.Fatal("missing scoped resource or cached usage")
	}
}

func TestLiveInspectorRejectsUnassociatedGeminiToolFragments(t *testing.T) {
	compiled := compileFixtureDefinition(t, presetDefinition(t, "gemini-api"))
	wire := "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"name\":\"verify_echo\",\"args\":{}}}]},\"finishReason\":null,\"index\":0}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"name\":\"\",\"args\":{\"arguments\":\"7}\"}}}]},\"finishReason\":null,\"index\":0}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[]},\"finishReason\":\"STOP\",\"index\":0}]}\n\n"
	if _, _, err := inspectLiveStream(compiled, []byte(wire), liveInspectionScope()); err == nil {
		t.Fatal("unassociated vendor fragments silently repaired")
	}
}

func TestLiveStreamInspectorPreservesUnknownFramesAndUsageTails(t *testing.T) {
	compiled := compileFixtureDefinition(t, presetDefinition(t, "chat-completions-api"))
	wire := "data: {\"id\":\"r\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"}}],\"vendor\":{\"n\":9007199254740993}}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2,\"prompt_tokens_details\":{\"cached_tokens\":80}}}\n\ndata: [DONE]\n\n"
	response, evidence, err := inspectLiveStream(compiled, []byte(wire), liveInspectionScope())
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.NativeReplay || len(evidence.Extensions) == 0 || response.Usage.CacheRead.Count != 80 || evidence.Events[protocol.ResponseFinished] != 1 {
		t.Fatalf("incorrect stream evidence: %+v", evidence)
	}
	if _, _, err := inspectLiveStream(compiled, []byte(strings.Split(wire, "data: {\"choices\":[{\"index\":0,\"delta\":{},")[0]), liveInspectionScope()); err == nil {
		t.Fatal("truncated stream passed")
	}
}

func TestLiveObserverChecksFourEvidenceLayersWithoutStoringCredentials(t *testing.T) {
	for _, fixture := range cacheWireFixtures() {
		t.Run(fixture.id, func(t *testing.T) {
			compiled := compileFixtureDefinition(t, presetDefinition(t, fixture.id))
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				isStream := bytes.Contains(body, []byte(`"stream":true`)) || strings.Contains(r.URL.Path, "streamGenerateContent")
				if isStream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, fixture.stream)
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, fixture.response)
				}
			}))
			t.Cleanup(provider.Close)
			directory := t.TempDir()
			suite := &liveSuite{Model: "synthetic", Origin: provider.URL, Compiler: protocol.CompilerVersion, StartedAt: time.Now(), key: "synthetic-secret-never-persisted", budget: &liveBudget{path: filepath.Join(directory, "budget.json")}, path: filepath.Join(directory, "live.json"), client: provider.Client()}
			gateway := newLiveGateway(t, suite, compiled)
			if gateway.scope.Provider == "" || gateway.scope.Account == "" || gateway.scope.Model != suite.Model {
				t.Fatal("observer lost persisted model account provenance", gateway.scope)
			}
			for _, isStream := range []bool{false, true} {
				result, _ := gateway.request(t, "harness", compiled, liveRequest(t, "grp", isStream), isStream)
				if result.Status != "passed" {
					t.Fatalf("stream=%t: %s", isStream, result.Reason)
				}
				if result.Wire.RequestHash == "" || result.Wire.ResponseHash == "" || result.DownstreamHash == "" || result.StoredRequestID == "" || result.StoredUsage.CacheRead.Count != 70 {
					t.Fatal("missing linked evidence")
				}
				suite.record(t, result)
			}
			raw, err := os.ReadFile(suite.path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(suite.key)) {
				t.Fatal("credential leaked into report")
			}
			var saved liveSuite
			if err := json.Unmarshal(raw, &saved); err != nil || len(saved.Cases) != 2 {
				t.Fatal("invalid evidence", err)
			}
		})
	}
}
