package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
)

const liveRequestLimit = 256
const liveOutputLimit = 128
const liveBodyLimit = 8 << 20
const liveRequestTimeout = 90 * time.Second
const liveUsageTimeout = 5 * time.Second
const liveUsagePoll = 10 * time.Millisecond
const liveErrorLimit = 1024

type liveBudget struct {
	mu                   sync.Mutex
	path                 string
	Calls                int       `json:"calls"`
	Stopped              string    `json:"stopped,omitempty"`
	Limit                int       `json:"limit,omitempty"`
	CleanupReserve       int       `json:"cleanupReserve,omitempty"`
	Deadline             time.Time `json:"deadline,omitempty"`
	FollowupCallsAtStart *int      `json:"followupCallsAtStart,omitempty"`
}

func (budget *liveBudget) reserve() error {
	return budget.reserveOperation(false)
}

func (budget *liveBudget) reserveOperation(isCleanup bool) error {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.Stopped != "" && !isCleanup {
		return fmt.Errorf("live run stopped: %s", budget.Stopped)
	}
	limit := budget.Limit
	if limit == 0 {
		limit = liveRequestLimit
	}
	if !isCleanup {
		limit -= budget.CleanupReserve
		if !budget.Deadline.IsZero() && !time.Now().Before(budget.Deadline) {
			return fmt.Errorf("live run deadline reached")
		}
	}
	if budget.Calls >= limit {
		return fmt.Errorf("live request limit reached")
	}
	budget.Calls++
	return budget.save()
}

func (budget *liveBudget) save() error {
	raw, err := json.MarshalIndent(budget, "", "  ")
	if err != nil {
		return err
	}
	temporary := budget.path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, budget.path)
}

func (budget *liveBudget) stop(reason string) error {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	budget.Stopped = reason
	return budget.save()
}

type liveWireEvidence struct {
	StartedAt        time.Time           `json:"startedAt,omitempty"`
	Endpoint         string              `json:"endpoint"`
	Status           int                 `json:"status"`
	RequestHash      string              `json:"requestHash"`
	RequestBytes     int                 `json:"requestBytes"`
	ResponseHash     string              `json:"responseHash"`
	ResponseBytes    int                 `json:"responseBytes"`
	RequestID        string              `json:"requestId,omitempty"`
	ElapsedMillis    int64               `json:"elapsedMillis"`
	RawUsage         []map[string]any    `json:"rawUsage,omitempty"`
	Error            string              `json:"error,omitempty"`
	Policies         map[string]any      `json:"cachePolicies,omitempty"`
	StreamEvidence   *liveStreamEvidence `json:"streamEvidence,omitempty"`
	RequestEvidence  string              `json:"requestEvidence,omitempty"`
	ResponseEvidence string              `json:"responseEvidence,omitempty"`
	body             []byte
	request          []byte
}

type liveCase struct {
	Model              string               `json:"model,omitempty"`
	Assessment         *liveCacheAssessment `json:"assessment,omitempty"`
	StoredTotals       map[string]any       `json:"storedTotals,omitempty"`
	ID                 string               `json:"id"`
	Ingress            string               `json:"ingress,omitempty"`
	Target             string               `json:"target"`
	Revision           string               `json:"revision"`
	Stream             bool                 `json:"stream"`
	Status             string               `json:"status"`
	Reason             string               `json:"reason,omitempty"`
	FailureClass       string               `json:"failureClass,omitempty"`
	Wire               liveWireEvidence     `json:"wire"`
	DownstreamStatus   int                  `json:"downstreamStatus,omitempty"`
	UpstreamUsage      *protocol.Usage      `json:"upstreamUsage,omitempty"`
	ReferenceUsage     *protocol.Usage      `json:"referenceUsage,omitempty"`
	DownstreamHash     string               `json:"downstreamHash,omitempty"`
	DownstreamBytes    int                  `json:"downstreamBytes,omitempty"`
	DownstreamEvidence string               `json:"downstreamEvidence,omitempty"`
	downstream         []byte
	DownstreamUsage    *protocol.Usage `json:"downstreamUsage,omitempty"`
	StoredUsage        *protocol.Usage `json:"storedUsage,omitempty"`
	StoredRequestID    string          `json:"storedRequestId,omitempty"`
	ToolCalls          []string        `json:"toolCalls,omitempty"`
}

type liveSuite struct {
	Targets           map[string]liveTargetProfile `json:"targets,omitempty"`
	SchemaVersion     int                          `json:"schemaVersion"`
	Model             string                       `json:"model"`
	Origin            string                       `json:"origin"`
	Compiler          string                       `json:"compiler"`
	StartedAt         time.Time                    `json:"startedAt"`
	Cases             []liveCase                   `json:"cases"`
	Limit             int                          `json:"requestLimit"`
	CallsAtStart      int                          `json:"callsAtStart"`
	CallsAtCheckpoint int                          `json:"callsAtCheckpoint"`
	Stopped           string                       `json:"stopped,omitempty"`
	Suite             string                       `json:"suite"`
	PreflightEvidence string                       `json:"preflightEvidence,omitempty"`
	PreflightHash     string                       `json:"preflightHash,omitempty"`
	key               string
	budget            *liveBudget
	path              string
	client            *http.Client
}

func reuseLivePreflight(t *testing.T, suite *liveSuite, compiled *protocol.Compiled, available map[bool]bool) bool {
	path := os.Getenv("ELYSIA_LIVE_PREFLIGHT_REPORT")
	if path == "" {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var prior liveSuite
	if err := json.Unmarshal(raw, &prior); err != nil {
		t.Fatal(err)
	}
	if prior.Model != suite.Model || prior.Origin != suite.Origin || prior.Compiler != suite.Compiler {
		t.Fatal("preflight evidence has a different model, origin or compiler")
	}
	for _, result := range prior.Cases {
		if strings.HasPrefix(result.ID, "preflight/") && result.Target == compiled.Identity().DefinitionID && result.Revision == compiled.Hash() && result.Status == "passed" {
			available[result.Stream] = true
		}
	}
	if !available[false] || !available[true] {
		t.Fatal("preflight evidence missing this target revision")
	}
	suite.PreflightEvidence, suite.PreflightHash = path, liveHash(raw)
	return true
}

func openLiveSuite(t *testing.T) *liveSuite {
	t.Helper()
	if os.Getenv("ELYSIA_LIVE_TESTS") != "1" {
		t.Skip("real provider tests require explicit opt-in")
	}
	key, directory, budgetPath := os.Getenv("ELYSIA_VERIFY_API_KEY"), os.Getenv("ELYSIA_VERIFY_DIR"), os.Getenv("ELYSIA_LIVE_BUDGET")
	profiles, err := readLiveTargetProfiles(os.Getenv("ELYSIA_LIVE_TARGETS"))
	if err != nil {
		t.Fatal(err)
	}
	if (key == "" && len(profiles) == 0) || directory == "" || budgetPath == "" {
		t.Fatal("live run requires in-memory credential and evidence/budget paths")
	}
	lock, err := os.OpenFile(budgetPath+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("another live verification owns this budget", err)
	}
	lock.Close()
	t.Cleanup(func() { os.Remove(budgetPath + ".lock") })
	budget := &liveBudget{path: budgetPath}
	if raw, err := os.ReadFile(budgetPath); err == nil {
		if err := json.Unmarshal(raw, budget); err != nil {
			t.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	suite := &liveSuite{SchemaVersion: 1, Model: "gpt-6.1-sol", Origin: "https://moyuu.cc", Compiler: protocol.CompilerVersion, StartedAt: time.Now().UTC(), Limit: liveRequestLimit, key: key, budget: budget, path: filepath.Join(directory, "live.json"), client: &http.Client{Transport: relay.NewSecureTransport(), Timeout: liveRequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	suite.Targets = profiles
	suite.CallsAtStart, suite.Suite = budget.Calls, os.Getenv("ELYSIA_LIVE_SUITE")
	if testing.Short() {
		suite.Suite = "preflight"
	}
	t.Cleanup(func() { suite.client.CloseIdleConnections() })
	return suite
}

func liveHash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func (suite *liveSuite) record(t *testing.T, result liveCase) {
	t.Helper()
	result.Model = suite.Model
	if result.Assessment == nil {
		result.Assessment = assessLiveCache(result)
	}
	result.Reason = strings.ReplaceAll(result.Reason, suite.key, "<redacted>")
	result.Wire.Error = strings.ReplaceAll(result.Wire.Error, suite.key, "<redacted>")
	if result.Status != "passed" {
		result.FailureClass = classifyLiveFailure(result)
	}
	suite.budget.mu.Lock()
	suite.CallsAtCheckpoint, suite.Stopped = suite.budget.Calls, suite.budget.Stopped
	suite.budget.mu.Unlock()
	for _, artifact := range []struct {
		suffix string
		body   []byte
		path   *string
	}{{"request", result.Wire.request, &result.Wire.RequestEvidence}, {"upstream", result.Wire.body, &result.Wire.ResponseEvidence}, {"downstream", result.downstream, &result.DownstreamEvidence}} {
		if len(artifact.body) == 0 {
			continue
		}
		if bytes.Contains(artifact.body, []byte(suite.key)) {
			t.Fatal("credential in synthetic wire evidence rejected")
		}
		name := liveHash([]byte(result.Target+"/"+result.ID)) + "." + artifact.suffix + ".txt"
		if err := os.WriteFile(filepath.Join(filepath.Dir(suite.path), name), artifact.body, 0600); err != nil {
			t.Fatal(err)
		}
		*artifact.path = name
	}
	suite.Cases = append(suite.Cases, result)
	raw, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(suite.key)) {
		t.Fatal("credential in evidence rejected")
	}
	if err := os.WriteFile(suite.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %s (%s)", result.ID, result.Status, result.Reason)
}

func liveBase(origin string, compiled *protocol.Compiled) string {
	if path := compiled.Operations()["generate"].Path; path == "/chat/completions" || path == "/responses" {
		return origin + "/v1"
	}
	return origin
}

func liveOperation(compiled *protocol.Compiled, isStream bool) protocol.Operation {
	name := "generate"
	if isStream {
		name = "stream"
	}
	return compiled.Operations()[name]
}

// exchange is the only paid-request boundary. It counts uncertain submissions
// before sending and never follows redirects or retries generation requests.
func (suite *liveSuite) exchange(ctx context.Context, compiled *protocol.Compiled, body []byte, isStream bool, writer http.ResponseWriter) liveWireEvidence {
	operation := liveOperation(compiled, isStream)
	return suite.exchangeOperation(ctx, liveBase(suite.Origin, compiled), operation, body, isStream, writer, false)
}

func (suite *liveSuite) exchangeOperation(ctx context.Context, base string, operation protocol.Operation, body []byte, isStream bool, writer http.ResponseWriter, isCleanup bool) liveWireEvidence {
	wire := liveWireEvidence{RequestHash: liveHash(body), RequestBytes: len(body), request: append([]byte(nil), body...), Policies: map[string]any{}}
	var requestValue any
	if json.Unmarshal(body, &requestValue) == nil {
		collectLivePolicies(requestValue, "", wire.Policies)
	}
	if err := suite.budget.reserveOperation(isCleanup); err != nil {
		wire.Error = err.Error()
		return wire
	}
	request, err := protocol.BuildHTTPRequest(ctx, base, suite.key, operation, body, map[string]string{"model": suite.Model})
	if err != nil {
		wire.Error = err.Error()
		return wire
	}
	wire.Endpoint = request.URL.Scheme + "://" + request.URL.Host + request.URL.EscapedPath()
	started := time.Now()
	wire.StartedAt = started.UTC()
	response, err := suite.client.Do(request)
	if err != nil {
		wire.Error = strings.ReplaceAll(err.Error(), suite.key, "<redacted>")
		return wire
	}
	defer response.Body.Close()
	wire.Status = response.StatusCode
	wire.RequestID = response.Header.Get("X-Request-Id")
	var captured bytes.Buffer
	var sink io.Writer = &captured
	if writer != nil {
		writer.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		writer.WriteHeader(response.StatusCode)
		sink = io.MultiWriter(&captured, liveFlushWriter{writer})
	}
	_, err = io.Copy(sink, io.LimitReader(response.Body, liveBodyLimit+1))
	wire.ElapsedMillis = time.Since(started).Milliseconds()
	wire.body = captured.Bytes()
	wire.ResponseBytes = len(wire.body)
	wire.ResponseHash = liveHash(wire.body)
	if err != nil {
		wire.Error = err.Error()
	}
	if len(wire.body) > liveBodyLimit {
		wire.Error = "response exceeded verification bound"
	}
	wire.RawUsage = readLiveUsage(wire.body, operation, isStream)
	if response.StatusCode >= 400 {
		var failure struct {
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(wire.body, &failure) == nil && len(failure.Error) > 0 {
			wire.Error = string(failure.Error)
		}
		wire.Error = strings.ReplaceAll(wire.Error, suite.key, "<redacted>")
		if len(wire.Error) > liveErrorLimit {
			wire.Error = wire.Error[:liveErrorLimit]
		}
		lower := strings.ToLower(wire.Error)
		if response.StatusCode == 402 || strings.Contains(lower, "insufficient_quota") || strings.Contains(lower, "insufficient balance") || strings.Contains(lower, "余额不足") {
			if err := suite.budget.stop("provider quota exhausted"); err != nil {
				wire.Error += "; cannot persist quota stop: " + err.Error()
			}
		}
	}
	return wire
}

func classifyLiveFailure(result liveCase) string {
	reason := strings.ToLower(result.Reason + " " + result.Wire.Error)
	switch {
	case strings.Contains(reason, "quota") || strings.Contains(reason, "balance"):
		return "quota"
	case strings.Contains(reason, "request limit") || strings.Contains(reason, "run stopped"):
		return "budget_stop"
	case result.Wire.Status == 401 || result.Wire.Status == 403:
		return "authentication"
	case result.Wire.Status == 404 || result.Wire.Status == 405:
		return "endpoint_unavailable"
	case strings.Contains(reason, "model") && (strings.Contains(reason, "not found") || strings.Contains(reason, "unavailable")):
		return "model_unavailable"
	case strings.Contains(reason, "timeout") || strings.Contains(reason, "deadline") || strings.Contains(reason, "connection"):
		return "network"
	case strings.Contains(reason, "verification_required") || strings.Contains(reason, "verification_mismatch"):
		return "binding_verification"
	case strings.Contains(reason, "unsupported_") || strings.Contains(reason, "no equivalent") || strings.Contains(reason, "no declared"):
		return "incompatible_semantics"
	case strings.Contains(reason, "upstream_contract_violation"):
		return "upstream_contract"
	case result.Status == "not_run":
		return "not_executed"
	default:
		return "assertion_or_contract"
	}
}

type liveFlushWriter struct{ http.ResponseWriter }

func (writer liveFlushWriter) Write(raw []byte) (int, error) {
	count, err := writer.ResponseWriter.Write(raw)
	if flush, ok := writer.ResponseWriter.(http.Flusher); ok {
		flush.Flush()
	}
	return count, err
}

func collectLivePolicies(value any, path string, policies map[string]any) {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			childPath := path + "/" + key
			switch key {
			case "cache_control", "prompt_cache_key", "prompt_cache_retention", "cachedContent":
				policies[childPath] = child
			}
			collectLivePolicies(child, childPath, policies)
		}
	case []any:
		for index, child := range node {
			collectLivePolicies(child, fmt.Sprintf("%s/%d", path, index), policies)
		}
	}
}

func readLiveUsage(raw []byte, operation protocol.Operation, isStream bool) []map[string]any {
	var usage []map[string]any
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if key == "usage" || key == "usageMetadata" {
					if object, ok := child.(map[string]any); ok {
						usage = append(usage, object)
					}
				} else {
					visit(child)
				}
			}
		case []any:
			for _, child := range node {
				visit(child)
			}
		}
	}
	decode := func(raw []byte) {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil {
			visit(value)
		}
	}
	if !isStream {
		decode(raw)
	} else {
		_ = protocol.ReadFrames(context.Background(), bytes.NewReader(raw), operation, liveBodyLimit, func(value protocol.Value, _ protocol.Object) error { decode(value.Bytes()); return nil })
	}
	return usage
}

func liveInspectionScope() protocol.Scope {
	return protocol.Scope{Provider: "https://moyuu.cc", Account: "verification-account", Model: "gpt-6.1-sol"}
}

func decodeLiveResponse(compiled *protocol.Compiled, raw []byte, isStream bool, scope protocol.Scope) (*protocol.Response, error) {
	if !isStream {
		return compiled.DecodeResponse(context.Background(), raw, protocol.EvaluationContext{Scope: scope})
	}
	response, _, err := inspectLiveStream(compiled, raw, scope)
	return response, err
}

func inspectLiveWire(compiled *protocol.Compiled, result *liveCase) (*protocol.Response, error) {
	scope := liveInspectionScope()
	if result.Model != "" {
		scope.Model = result.Model
	}
	familyID := map[string]string{"openai_chat": "openai-chat-completions", "openai_responses": "openai-responses", "claude": "anthropic-messages", "gemini": "google-generate-content"}[compiled.Identity().Family]
	reference, referenceErr := referenceLiveUsage(familyID, result.Wire.RawUsage)
	result.ReferenceUsage = reference
	var response *protocol.Response
	var err error
	if result.Stream {
		response, result.Wire.StreamEvidence, err = inspectLiveStream(compiled, result.Wire.body, scope)
	} else {
		response, err = decodeLiveResponse(compiled, result.Wire.body, false, scope)
	}
	if err != nil {
		return nil, err
	}
	result.UpstreamUsage = response.Usage
	if referenceErr != nil {
		return nil, referenceErr
	}
	if err := compareLiveUsage(result.ReferenceUsage, response.Usage); err != nil {
		return nil, fmt.Errorf("raw upstream usage: %w", err)
	}
	return response, nil
}

func (suite *liveSuite) observer(compiled *protocol.Compiled) (*httptest.Server, <-chan liveWireEvidence) {
	completed := make(chan liveWireEvidence, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := protocol.ReadBoundedBody(request.Body, liveBodyLimit)
		if err != nil {
			http.Error(writer, "invalid verification request", 400)
			return
		}
		var flags struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &flags)
		isStream := flags.Stream || strings.Contains(request.URL.Path, ":streamGenerateContent")
		wire := suite.exchange(request.Context(), compiled, body, isStream, writer)
		if wire.Status == 0 {
			http.Error(writer, "verification request unavailable", 503)
		}
		completed <- wire
	}))
	return proxy, completed
}
