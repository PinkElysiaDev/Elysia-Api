package protocol

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"
)

func compileSharedCacheFixture(t *testing.T, document ProtocolPackage) *Compiled {
	t.Helper()
	compiler, _ := NewCompiler(DefaultLimits(), nil)
	compiled, issues := compiler.Compile(fixtureValue(t, document).Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func newTestCompilationCache() *compilationCache {
	return &compilationCache{entries: map[compilationKey]weak.Pointer[Compiled]{}, flights: map[compilationKey]*compilationFlight{}, presets: map[string]presetCompilation{}}
}

func TestCompilationCacheIdentity(t *testing.T) {
	compiler, _ := NewCompiler(DefaultLimits(), nil)
	raw := fixtureValue(t, verifiedTextPackage(t, "cache-identity")).Bytes()
	first, issues := compiler.Compile(raw)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	other, _ := NewCompiler(DefaultLimits(), nil)
	repeated, issues := other.CompilePackage(raw)
	if IssuesError(issues) != nil || first != repeated {
		t.Fatal("equal keys did not share a process-wide graph", issues)
	}
	limits := DefaultLimits()
	limits.Nodes--
	limited, _ := NewCompiler(limits, nil)
	featured, _ := NewCompiler(DefaultLimits(), []string{featureWebSocketTransport})
	for name, build := range map[string]func() (*Compiled, []ConversionIssue){
		"limits":   func() (*Compiled, []ConversionIssue) { return limited.Compile(raw) },
		"features": func() (*Compiled, []ConversionIssue) { return featured.Compile(raw) },
		"spelling": func() (*Compiled, []ConversionIssue) { return compiler.Compile(append([]byte(" "), raw...)) },
		"definition": func() (*Compiled, []ConversionIssue) {
			return compiler.Compile(fixtureValue(t, verifiedTextPackage(t, "cache-other")).Bytes())
		},
	} {
		compiled, issues := build()
		if IssuesError(issues) != nil || compiled == nil || compiled == first {
			t.Fatal(name, "aliased another key", issues)
		}
	}
	cache := newTestCompilationCache()
	key := compiler.compilationKey(raw)
	build := func() (*Compiled, []ConversionIssue) { return &Compiled{}, nil }
	current, _ := cache.compile(key, build)
	key.version += "-next"
	next, _ := cache.compile(key, build)
	if current == next {
		t.Fatal("compiler versions shared a graph")
	}
	runtime.KeepAlive(first)
}

func TestCompilationCachePinsOnlyEmbeddedPresetBytes(t *testing.T) {
	compiler, err := NewCompiler(DefaultLimits(), []string{featureWebSocketTransport, featureAsyncTasks})
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := embeddedPresetPackage("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	variant := fixtureValue(t, verifiedTextPackage(t, PresetAnthropicID))
	for _, raw := range [][]byte{variant.Bytes(), append([]byte(" "), embedded.Bytes()...)} {
		cache := newTestCompilationCache()
		variantKey := compiler.compilationKey(raw)
		variantGraph, issues := cache.compile(variantKey, func() (*Compiled, []ConversionIssue) { return compiler.Compile(raw) })
		if err := IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		cache.admitPreset(PresetAnthropicID, variantKey, variantGraph)
		if len(cache.presets) != 0 {
			t.Fatal("a preset-ID variant was pinned")
		}
		key := compiler.compilationKey(embedded.Bytes())
		builds := 0
		build := func() (*Compiled, []ConversionIssue) {
			builds++
			return compiler.Compile(embedded.Bytes())
		}
		graph, issues := cache.compile(key, build)
		if IssuesError(issues) != nil || graph == variantGraph || builds != 1 {
			t.Fatal("embedded bytes reused a variant graph", issues)
		}
		cache.admitPreset(PresetAnthropicID, key, graph)
		if len(cache.presets) != 1 || cache.presets[PresetAnthropicID].compiled != graph {
			t.Fatal("a variant admitted first prevented embedded pinning")
		}
		// Prove the strong preset lookup, independently from the weak entry.
		delete(cache.entries, key)
		for range 3 {
			warm, issues := cache.compile(key, build)
			cache.admitPreset(PresetAnthropicID, key, warm)
			if IssuesError(issues) != nil || warm != graph || builds != 1 {
				t.Fatal("warm embedded admission did not reuse its pinned graph", issues)
			}
		}
		for _, changed := range []compilationKey{
			{version: key.version + "-next", limits: key.limits, features: key.features, source: key.source},
			{version: key.version, limits: Limits{}, features: key.features, source: key.source},
			{version: key.version, limits: key.limits, source: key.source},
		} {
			other, _ := cache.compile(changed, func() (*Compiled, []ConversionIssue) { return &Compiled{}, nil })
			if other == graph {
				t.Fatal("preset lookup bypassed the complete admission key")
			}
		}
		cache.presets[PresetGeminiID] = cache.presets[PresetAnthropicID]
		delete(cache.presets, PresetAnthropicID)
		other, _ := cache.compile(key, func() (*Compiled, []ConversionIssue) { return &Compiled{}, nil })
		if other == graph {
			t.Fatal("preset lookup failed to check the ID and embedded source identity")
		}
	}
}

func TestCompilationCacheUnknownPresetNeverPins(t *testing.T) {
	compiler, _ := NewCompiler(DefaultLimits(), nil)
	cache := newTestCompilationCache()
	raw := fixtureValue(t, verifiedTextPackage(t, "unknown-preset")).Bytes()
	compiled, issues := compiler.Compile(raw)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	cache.admitPreset("unknown-preset", compiler.compilationKey(raw), compiled)
	if len(cache.presets) != 0 {
		t.Fatal("unknown preset ID pinned a graph")
	}
}

func TestCompilationSessionVariantCannotPreventEmbeddedPinning(t *testing.T) {
	service, _, variant := newCompilationSessionFixture(t)
	session := service.NewCompilationSession()
	defer session.Close()
	first, issues := session.CompilePreset(PresetAnthropicID, variant)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	embedded, err := embeddedPresetPackage("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	graph, issues := session.CompilePreset(PresetAnthropicID, embedded)
	if IssuesError(issues) != nil || graph == first {
		t.Fatal("embedded preset inherited a variant", issues)
	}
	processCompilations.mu.Lock()
	pinned := processCompilations.presets[PresetAnthropicID]
	processCompilations.mu.Unlock()
	if pinned.compiled != graph || pinned.key != service.compiler.compilationKey(embedded.Bytes()) {
		t.Fatal("session's first variant blocked process pinning of embedded bytes")
	}
	if session.presets[PresetAnthropicID].compiled != first {
		t.Fatal("process pinning changed the session's exact first-source contract")
	}
}

func TestCompilationCacheDoesNotCacheFailures(t *testing.T) {
	cache := newTestCompilationCache()
	builds := 0
	build := func() (*Compiled, []ConversionIssue) {
		builds++
		return nil, definitionIssues(Identity{}, "/test", fmt.Errorf("invalid definition"))
	}
	for range 2 {
		compiled, issues := cache.compile(compilationKey{}, build)
		if compiled != nil || IssuesError(issues) == nil {
			t.Fatal("failed compilation lost its diagnostic", issues)
		}
		issues[0].Reason = "caller-owned"
	}
	if builds != 2 || len(cache.entries) != 0 || len(cache.flights) != 0 {
		t.Fatal("failed compilation was retained")
	}
}

func TestCompilationCacheCoalescesConcurrentMisses(t *testing.T) {
	cache := newTestCompilationCache()
	var builds atomic.Int32
	start, entered, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var workers sync.WaitGroup
	results := make([]*Compiled, 32)
	for index := range results {
		workers.Go(func() {
			<-start
			results[index], _ = cache.compile(compilationKey{}, func() (*Compiled, []ConversionIssue) {
				builds.Add(1)
				close(entered)
				<-release
				return &Compiled{}, nil
			})
		})
	}
	close(start)
	<-entered
	close(release)
	workers.Wait()
	if builds.Load() != 1 {
		t.Fatal("concurrent misses compiled more than once", builds.Load())
	}
	for _, result := range results {
		if result != results[0] {
			t.Fatal("waiters received distinct graphs")
		}
	}
}

// Keep the strong local in a separate frame so stack liveness cannot make the
// collection assertion depend on a compiler optimization.
//
//go:noinline
func collectableCompilation(cache *compilationCache) weak.Pointer[Compiled] {
	compiled, _ := cache.compile(compilationKey{}, func() (*Compiled, []ConversionIssue) { return &Compiled{}, nil })
	return weak.Make(compiled)
}

func TestCompilationCacheDoesNotRetainUnusedGraphs(t *testing.T) {
	cache := newTestCompilationCache()
	pointer := collectableCompilation(cache)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if pointer.Value() == nil {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("weak cache retained a released graph")
}

func TestCompilationCacheReloadUsesGraphButRereadsEvidence(t *testing.T) {
	service, repository, _ := newCompilationSessionFixture(t)
	if err := service.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, _ := service.Pin(PresetAnthropicID)
	key := service.compiler.compilationKey(repository.revision.Definition.Bytes())
	// A test-only cache probe makes a miss fail instead of recompiling. The
	// production path has no compile counters or logging.
	processCompilations.mu.Lock()
	pointer := processCompilations.entries[key]
	processCompilations.mu.Unlock()
	reads := repository.reportReads
	for range 3 {
		if err := service.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
		current, _ := service.Pin(PresetAnthropicID)
		if current != first || pointer.Value() != current {
			t.Fatal("unchanged reload compiled another graph")
		}
	}
	if repository.reportReads != reads+3 {
		t.Fatal("cache bypassed stored activation evidence")
	}
	repository.report.Passed = false
	if err := service.Reload(t.Context()); err == nil {
		t.Fatal("cached graph authorized failed stored evidence")
	}
}

func TestVerificationMemoClonesEvidenceAndStampsEachCall(t *testing.T) {
	compiled := compileSharedCacheFixture(t, verifiedTextPackage(t, "memo-evidence"))
	first := Verify(t.Context(), compiled)
	if !first.Passed || compiled.verification.report == nil {
		t.Fatal(first.Issues)
	}
	baseline := cloneVerificationReport(first)
	first.Checks[0].SampleID = "mutated"
	for index := range first.Checks {
		for capability := range first.Checks[index].Capabilities {
			first.Checks[index].Capabilities[capability] = "mutated"
		}
	}
	for index := range first.Covered {
		first.Covered[index] = "mutated"
	}
	first.Covered = append(first.Covered, "mutated")
	first.Issues = append(first.Issues, ConversionIssue{Reason: "mutated"})
	second := Verify(t.Context(), compiled)
	if second.VerifiedAt.Before(baseline.VerifiedAt) || second.VerifiedAt.IsZero() {
		t.Fatal("cached report lost the per-call timestamp")
	}
	second.VerifiedAt = baseline.VerifiedAt
	if !reflect.DeepEqual(second, baseline) {
		t.Fatal("caller mutation reached shared evidence", second, baseline)
	}
	uncached := verifyUncached(t.Context(), compiled, baseline.VerifiedAt)
	if !reflect.DeepEqual(uncached, baseline) {
		t.Fatal("memoized evidence changed report bytes")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if report := Verify(canceled, compiled); report.Passed {
		t.Fatal("cached evidence hid cancellation")
	}
	limited, err := WithRequestBodyLimit(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if report := Verify(limited, compiled); report.Passed {
		t.Fatal("cached evidence hid a different request budget")
	}
}

func TestVerificationMemoContextInputs(t *testing.T) {
	for _, input := range []string{"request-budget", "document-metrics"} {
		t.Run(input, func(t *testing.T) {
			compiled := compileSharedCacheFixture(t, verifiedTextPackage(t, "memo-context-"+input))
			ctx := t.Context()
			if input == "request-budget" {
				var err error
				ctx, err = WithRequestBodyLimit(ctx, 1)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				ctx = withDocumentMetrics(ctx, 64<<10)
			}
			if verificationMemoEligible(ctx) {
				t.Fatal("a non-default execution input was memo eligible")
			}
			cold := Verify(ctx, compiled)
			if compiled.verification.report != nil || cold.Passed != (input == "document-metrics") {
				t.Fatal("alternate context populated the memo or changed its verdict", cold.Issues)
			}
			if normal := Verify(t.Context(), compiled); !normal.Passed || compiled.verification.report == nil {
				t.Fatal("alternate context poisoned default verification", normal.Issues)
			}
			memo := compiled.verification.report
			warm := Verify(ctx, compiled)
			uncached := verifyUncached(ctx, compiled, warm.VerifiedAt)
			cold.VerifiedAt = warm.VerifiedAt
			if !reflect.DeepEqual(warm, uncached) || !reflect.DeepEqual(warm, cold) || compiled.verification.report != memo {
				t.Fatal("default memo changed alternate-context evidence")
			}
		})
	}
}

func TestVerificationMemoExpiredDeadlineCannotUseWarmEvidence(t *testing.T) {
	compiled := compileSharedCacheFixture(t, verifiedTextPackage(t, "memo-warm-deadline"))
	if report := Verify(t.Context(), compiled); !report.Passed {
		t.Fatal(report.Issues)
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if report := Verify(ctx, compiled); report.Passed {
		t.Fatal("warm evidence hid deadline expiry")
	}
}

type verificationCancelContext struct {
	context.Context
	cancel context.CancelFunc
	reads  int
	at     int
}

func (ctx *verificationCancelContext) Err() error {
	ctx.reads++
	if ctx.reads == ctx.at {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestVerificationMemoDoesNotCachePartialExecution(t *testing.T) {
	compiled := compileSharedCacheFixture(t, verifiedTextPackage(t, "memo-partial"))
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &verificationCancelContext{Context: base, cancel: cancel, at: 5}
	if report := Verify(ctx, compiled); report.Passed || compiled.verification.report != nil || ctx.reads < 5 {
		t.Fatal("partially canceled execution became cached evidence", report.Issues)
	}
	if report := Verify(t.Context(), compiled); !report.Passed {
		t.Fatal("partial execution poisoned subsequent verification", report.Issues)
	}
}

func TestVerificationMemoRechecksCancellationAfterClone(t *testing.T) {
	compiled := compileSharedCacheFixture(t, verifiedTextPackage(t, "memo-cancel-clone"))
	if report := Verify(t.Context(), compiled); !report.Passed {
		t.Fatal(report.Issues)
	}
	memo := compiled.verification.report
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &verificationCancelContext{Context: base, cancel: cancel, at: 3}
	if report := Verify(ctx, compiled); report.Passed || compiled.verification.report != memo {
		t.Fatal("cloning a warm report hid cancellation or changed cached evidence")
	}
}

func TestVerificationMemoDoesNotCacheFailuresOrCancellation(t *testing.T) {
	document := verifiedTextPackage(t, "memo-failure")
	document.Mapping.Samples[0].Expected = StringValue("wrong")
	compiled := compileSharedCacheFixture(t, document)
	for range 2 {
		if report := Verify(t.Context(), compiled); report.Passed || compiled.verification.report != nil {
			t.Fatal("failed verification became cached evidence")
		}
	}
	compiled = compileSharedCacheFixture(t, verifiedTextPackage(t, "memo-cancellation"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if report := Verify(ctx, compiled); report.Passed || compiled.verification.report != nil {
		t.Fatal("canceled verification became cached evidence")
	}
	ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if report := Verify(ctx, compiled); report.Passed || compiled.verification.report != nil {
		t.Fatal("deadline-exceeded verification became cached evidence")
	}
	if report := Verify(t.Context(), compiled); !report.Passed || compiled.verification.report == nil {
		t.Fatal("cancellation poisoned subsequent verification", report.Issues)
	}
}
