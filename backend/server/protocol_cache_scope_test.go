package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveSignedGeminiResponseUsesPersistedScope(t *testing.T) {
	body := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"OK","thoughtSignature":"synthetic-signature"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":4645,"candidatesTokenCount":2,"thoughtsTokenCount":26,"totalTokenCount":4673}}`)
	if path := os.Getenv("ELYSIA_CACHE_REPLAY_RESPONSE"); path != "" {
		var err error
		body, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(provider.Close)
	directory := t.TempDir()
	suite := &liveSuite{Model: "gemini-3-flash-preview", Origin: provider.URL, key: "synthetic-secret", StartedAt: time.Now(), client: provider.Client(), budget: &liveBudget{path: filepath.Join(directory, "budget.json")}, path: filepath.Join(directory, "live.json")}
	compiled := compileFixtureDefinition(t, presetDefinition(t, "google-generate-content"))
	gateway := newLiveGateway(t, suite, compiled)
	result, _ := gateway.request(t, "signed", compiled, liveRequest(t, "grp", false), false)
	if result.Status != "passed" {
		t.Fatal("signed response inspection failed", result.Reason)
	}
	if result.StoredUsage.Input.Count != 4645 || result.DownstreamUsage.Output.Count != 28 {
		t.Fatal("observed usage changed")
	}
}
