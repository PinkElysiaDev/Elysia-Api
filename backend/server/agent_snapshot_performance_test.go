package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/protocol"
)

const (
	agentSnapshotWarmup = 8
	agentSnapshotCalls  = 128
)

func TestAgentSnapshotPinsInFlightBindingAndSeesRevocation(t *testing.T) {
	s := newAgentIntegrationServer(t)
	seedGatewayPackages(t, s)
	started, release := make(chan struct{}), make(chan struct{})
	upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"pinned","object":"chat.completion","created":1,"model":"fake-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`)
	})
	seedCallerModel(t, s, upstream.URL, "openai")
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	binding := bindings[0]
	binding.Binding.Transports = []protocol.Transport{protocol.HTTPJSON}
	if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	caller := newAgentStreamCaller(s)
	done := make(chan error, 1)
	go func() {
		result, err := caller.Call(t.Context(), callerRequest(), agent.StreamCallbacks{})
		if err == nil && result.Text != "hello" {
			err = fmt.Errorf("in-flight Agent output changed")
		}
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		close(release)
		t.Fatal("Agent did not reach provider", err)
	}
	binding.Unbound = true
	err = s.store.SaveProtocolBinding(t.Context(), binding)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal("revocation changed an already pinned call", err)
	}
	_, err = caller.Call(t.Context(), callerRequest(), agent.StreamCallbacks{})
	var readiness agentReadinessError
	if !errors.As(err, &readiness) || readiness.ReasonCode() != "unbound" || upstream.requestCount() != 1 {
		t.Fatal("new Agent call reused revoked binding", err, upstream.requestCount())
	}
}

// This opt-in fixture measures complete local Agent calls, including its local
// HTTP provider and synchronous usage persistence. It is not gateway-only CPU.
func TestAgentSnapshotPerformance(t *testing.T) {
	directory := os.Getenv("ELYSIA_AGENT_SNAPSHOT_OUTPUT")
	if directory == "" {
		t.Skip("opt-in Agent performance fixture")
	}
	s := newAgentIntegrationServer(t)
	seedGatewayPackages(t, s)
	upstream := newCapturingUpstream(t, func(w http.ResponseWriter, body string, call int) {
		if call%2 == 0 && !containsAgentHistory(body) {
			t.Error("second Agent call lost assistant history")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"agent-response","object":"chat.completion","created":1,"model":"fake-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	})
	seedCallerModel(t, s, upstream.URL, "openai")
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		binding.Binding.Transports = []protocol.Transport{protocol.HTTPJSON}
		if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
			t.Fatal(err)
		}
	}
	caller := newAgentStreamCaller(s)
	var previous *agent.CallResult
	call := func(index int) time.Duration {
		request := callerRequest()
		if index%2 != 0 {
			request.Content = append(request.Content, previous.Content...)
			request.Content = append(request.Content, protocol.Node{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("continue")}}})
		}
		started := time.Now()
		result, err := caller.Call(t.Context(), request, agent.StreamCallbacks{})
		elapsed := time.Since(started)
		if err != nil || result.Text != "hello" || result.Usage == nil || result.Usage.Input == nil || result.Usage.Input.Count != 3 {
			t.Fatal("Agent call changed output or usage", result, err)
		}
		previous = result
		return elapsed
	}
	for index := range agentSnapshotWarmup {
		call(index)
	}
	var before, after, recovered runtime.MemStats
	runtime.ReadMemStats(&before)
	beforeCPU, err := loadCPUTime()
	if err != nil {
		t.Fatal(err)
	}
	latencies := make([]int64, agentSnapshotCalls)
	started := time.Now()
	for index := range agentSnapshotCalls {
		latencies[index] = int64(call(index))
	}
	elapsed := time.Since(started)
	afterCPU, err := loadCPUTime()
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	time.Sleep(time.Second)
	runtime.ReadMemStats(&recovered)
	if upstream.requestCount() != agentSnapshotWarmup+agentSnapshotCalls {
		t.Fatal("unexpected upstream calls", upstream.requestCount())
	}
	logs := latestUsageRecords(t, s)
	if len(logs) == 0 {
		t.Fatal("Agent calls did not persist usage")
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	report := map[string]any{
		"calls": agentSnapshotCalls, "upstreamCalls": upstream.requestCount(),
		"ns":      float64(elapsed.Nanoseconds()) / agentSnapshotCalls,
		"bytes":   float64(after.TotalAlloc-before.TotalAlloc) / agentSnapshotCalls,
		"objects": float64(after.Mallocs-before.Mallocs) / agentSnapshotCalls,
		"cpuNS":   float64(afterCPU-beforeCPU) / agentSnapshotCalls,
		"p50NS":   latencies[len(latencies)/2], "p95NS": latencies[len(latencies)*95/100],
		"recoveryHeap": recovered.HeapAlloc, "gcPauseNS": after.PauseTotalNs - before.PauseTotalNs,
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "agent.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(raw))
}

func containsAgentHistory(body string) bool {
	var request struct {
		Messages []struct{ Role, Content string }
	}
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		return false
	}
	for _, message := range request.Messages {
		if message.Role == "assistant" && message.Content == "hello" {
			return true
		}
	}
	return false
}
