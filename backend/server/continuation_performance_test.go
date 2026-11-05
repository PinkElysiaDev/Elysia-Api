package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

type continuationPerformanceFixture struct{ Body, Session string }

func loadContinuationPerformanceFixture(t *testing.T) *continuationPerformanceFixture {
	t.Helper()
	path := os.Getenv("ELYSIA_FLAME_CONTINUATION_FIXTURE")
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture continuationPerformanceFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return &fixture
}

func (fixture *continuationPerformanceFixture) validate(status int, raw []byte) error {
	var result struct {
		Content    []struct{ Type, Text string }
		StopReason string `json:"stop_reason"`
		Usage      struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		}
	}
	if status != http.StatusOK {
		return fmt.Errorf("continuation response %d: %.400s", status, raw)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	var text string
	for _, block := range result.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	if text != "hello" || result.StopReason != "end_turn" || result.Usage.Input != 3 || result.Usage.Output != 2 || !strings.Contains(string(raw), protocol.ContinuationPrefix) {
		return fmt.Errorf("continuation response lost text, usage, stop or carrier: %.400s", raw)
	}
	return nil
}

// This deterministic provider is a separate process, shared by every compared
// gateway. It checks actual recovered upstream signatures, not only HTTP 200.
func TestContinuationPerformanceProvider(t *testing.T) {
	ready := os.Getenv("ELYSIA_CONTINUATION_READY")
	if ready == "" {
		t.Skip("isolated continuation provider")
	}
	var calls, recovered atomic.Int64
	history := strings.Repeat("x", 4096)
	media := ""
	if os.Getenv("ELYSIA_CONTINUATION_MEDIA") == "1" {
		media = strings.Repeat("A", 2<<20)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			_ = json.NewEncoder(w).Encode(map[string]any{"Calls": calls.Load(), "Recovered": recovered.Load()})
			return
		}
		calls.Add(1)
		var input struct {
			Contents []struct {
				Role  string
				Parts []struct {
					Text, ThoughtSignature string
					InlineData             *struct{ MimeType, Data string }
				}
			}
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil || json.Unmarshal(raw, &input) != nil || strings.Contains(string(raw), protocol.ContinuationPrefix) {
			http.Error(w, "invalid upstream history or leaked carrier", http.StatusBadRequest)
			return
		}
		if len(input.Contents) != 63 && len(input.Contents) != 65 {
			http.Error(w, "history length changed", http.StatusBadRequest)
			return
		}
		for index, message := range input.Contents[:63] {
			role := "user"
			if index%2 != 0 {
				role = "model"
			}
			parts := 1
			if index == 0 && media != "" {
				parts = 2
			}
			if message.Role != role || len(message.Parts) != parts || message.Parts[0].Text != history {
				http.Error(w, "history text or role changed", http.StatusBadRequest)
				return
			}
			if parts == 2 && (message.Parts[1].InlineData == nil || message.Parts[1].InlineData.MimeType != "image/png" || message.Parts[1].InlineData.Data != media) {
				http.Error(w, "history media changed", http.StatusBadRequest)
				return
			}
		}
		if len(input.Contents) == 65 {
			assistant, user := input.Contents[63], input.Contents[64]
			if assistant.Role != "model" || len(assistant.Parts) != 1 || assistant.Parts[0].Text != "hello" || assistant.Parts[0].ThoughtSignature != "signed-text" || user.Role != "user" || len(user.Parts) != 1 || user.Parts[0].Text != "continue" {
				http.Error(w, "authenticated signature was not restored", http.StatusBadRequest)
				return
			}
			recovered.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello","thoughtSignature":"signed-text"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`)
	}))
	defer provider.Close()
	address := conversionHTTPAddress{URL: provider.URL, Metrics: provider.URL + "/metrics", PID: os.Getpid(), Execution: &conversionExecutionProof{Execution: "continuation-fixture", Commit: AppCommit, Compiler: protocol.CompilerVersion}}
	raw, err := json.Marshal(address)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ready, raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
