# Agent Note: Compile cache identity, context and historical retention

Status: implemented

## Problem

The [weak compilation cache](2026-11-16-compile-cache-and-single-decode.en.md) can process-pin an authored variant merely because its ID names a preset. That prevents the actual embedded graph from occupying its bounded slot. The offline memo's request-budget-only context claim lacks a complete reader inventory. Removing historical strong retention also makes repeated durable job loads lose their graphs after collection, despite the process cache's weak entries. Ingress job handling (`backend/server/gateway_job.go`) and upstream execution (`backend/server/gateway_job_executor.go`) both use `LoadRevision`.

## Decision

### Embedded-byte pinning

The process cache indexes pinned entries by **preset ID**, storing the graph and its complete `compilationKey`: compiler version, engine limits, feature identity and SHA-256 of exact admitted source bytes. Admission additionally requires an embedded package for that ID, a matching compiled definition ID, and source SHA-256 equal to the embedded package SHA-256. Canonical definition hashes are not a substitute: even whitespace-only alternative spelling must remain unpinned. Unknown IDs never pin.

`protocol` embeds the existing Spec/Mapping files to authenticate the bytes independently of callers and without importing `protocol/builtin` back into its parent. A once-initialized immutable inventory joins them exactly as `builtin.Packages` and `CompileSources` do, without normalizing JSON, and retains their values and hashes. No sample files are moved or changed. Pinned reads check the whole compilation key **and** the slot's embedded ID/source identity. Only one embedded graph per preset ID is process-pinned; other engine configurations can still use the ordinary weak cache.

A session still retains its first successful source per recognized ID for its own publication contract. Every successful `CompilePreset` separately attempts authenticated process admission, even when the session first saw a variant. Thus a variant cannot block a later embedded admission in the same session.

### Complete offline context-input inventory

The inventory starts at `Verify` / `verifyUncached` and follows the ordinary fixtures, `executeVerificationSample`, `executeEventFixture`, `executeEventFixtureWithState`, `executeEventFixtureStream`, `verifyRoundTrip`, `verifyEventRoundTrip`, `verifyNativeExtension`, `verifyTaskSamples`, `verifySessionSamples`, `verifyAgentSamples`, `verifyModelSamples`, `verifyTokenCountSamples`, and their execution helpers. Repository-wide searches enumerate every production context value reader, context constructor and cancellation/deadline reader in `backend/protocol`; imported `internal/eventstream` and `internal/jsonutil` are checked too.

| Context input | Readers and propagation | Memo decision |
| --- | --- | --- |
| `requestBudgetKey{}` / effective `RequestBodyLimit(ctx)` | `resource_budget.go`: `RequestLimits` → `wireLimits` → `requestLimits`; `mappingLimits`; `nativeLimits`; `EncodeSemantic`; conversion `phaseLimits` / `ruleLimits`. Request decoding/encoding, operation input/output validation, native replay, token-count child request graphs and Agent request conversion reach these helpers. `WithRequestBodyLimit` also reads this same key to preserve an already pinned limit, but offline verification does not install an override. `PreviewEnvelopeLimit` reads the same value; it is an authoring-envelope helper, not called by `Verify`. | Only effective `DefaultRequestBodyBytes` is eligible. A different budget bypasses the memo; an explicitly pinned default has identical effective semantics. |
| `documentMetricsKey{}` | `document_metrics.go`: `withDocumentMetrics`, `measureDocumentMetrics`, `fitsDocumentMetrics`, `checkDocumentSemantic`, `checkDocumentMapping`. Ordinary/Agent/token-count request decoding and encoding, continuation extraction and native replay propagate the context into these helpers. Large nested calls can create a local owner. `document_normalization.go`'s `normalizeDocumentMapping` also reads this key, but currently has no production callers; it introduces no additional input. | Any caller-supplied value bypasses the memo. This includes a default-budget metrics owner. Locally created owners contain execution-local policy-independent measurement facts and do not escape a call. |
| Cancellation / deadline expiry | `ctx.Err()` in `Verify` and reachable runtime, mapping, expression/evaluation, core-control/validation, output encoding, stream finalization, wire contract, model-policy and operation-plan paths. Stored evaluation/validation contexts forward this same context. There is no offline `Deadline()` or `Done()` reader and no live transport wait; the deadline's effect is `Err() == context.DeadlineExceeded`. | Recheck after acquiring the memo mutex and after cloning a warm report. Store only a complete passing report while `Err() == nil`. Canceled, expired, failed and partial executions never populate the memo. |

The following other discovered keys are **not reachable from offline `Verify`**. They belong to `VerifyCombination`, `VerifyBindingCombination`, profile selection or their dedicated helpers, which are not memoized here:

- `conversionVerificationKey{}`: conversion selection in `verification_combination.go` and `verifyEventCombination` in `verification_event_combination.go`.
- `conversionVerificationDiagnostics{}`: `verificationDiagnostics` and combination-only callers.
- `combinationTransportKey{}`: combination request/transport selection and `verification_profiles.go`.
- `requestScopeVerificationKey{}`: combination/profile request-scope selection.
- `visibleReasoningVerificationKey{}`: combination visible-reasoning report selection.

`EvaluationContext` is a different type, not a `context.Context` value bag. `newVerificationContext` derives scope and Values from immutable fixtures, creates fresh state and fixes synthetic operation ID/time. Event fixtures add local stream validators/replay/budgets. Task helpers use synthetic options; session helpers share only fixture-local state. Agent helpers build options from the selected request fixture, preferences/model and compiled conversion defaults. Model and token-count operation plans use empty/default options or their compiled child graphs. `WithOutputStream`, `WithConversion`, `WithOperation`, `WithValueReader`, definition/target builders and runtime binders do not read additional Go context values. Scope, preferences, parameters, model, output controls, native policies, engine/authored limits and conversion choices are compiled/fixture inputs, not ambient context. No provider, network, environment, clock or external callback supplies offline evidence; `VerifiedAt` is the per-call clock stamp and synthetic operation time remains fixed. Reflection `MapIter.Value` and weak-pointer `Value()` are not context readers.

The memo remains **one report per graph**, not a context-keyed map. Bypassing for any non-default request budget or supplied metrics owner is simpler than keying mutable metrics state and leaves a finite, directly auditable condition. It retains the deep clone of checks, nested capabilities, coverage, issues and target before sharing any evidence with a caller, and a fresh `VerifiedAt` for each call.

### Bounded graph retention, never authorization

Historical `LoadRevision` retains only successfully verified process-cache graphs under `service.mu`, keyed by `id + "/" + hash`. At all times the sum of retained canonical definition bytes is at most `compiler.limits.BufferBytes`, and the entry count is at most `compiler.limits.StateItems`. The production defaults remain 8 MiB of definition bytes and 4096 entries. These are accounting limits, **not** estimates of graph heap size. A graph whose canonical definition alone exceeds the byte bound is not retained. FIFO insertion order supplies oldest-first eviction; hits do not refresh the order. Entry, order and byte counters are removed together, and vacated order slots are cleared.

Retention does not add a second compile implementation or a cache-hit authorization shortcut. Each historical call still invokes `loadVerifiedRevision`: reread the durable revision, call the existing process-cache compiler, check definition ID/hash, reread the current-engine durable report, and run `CanActivate`. Keeping the graph strongly live guarantees that the process weak-cache lookup can reuse that exact graph. Only after the full load succeeds is it retained. The pre-existing active-snapshot shortcut in `LoadRevision` remains unchanged; activation/reload still use their existing authoritative publication path.

Invalidation points are explicit:

- Successful `DeleteRetained` commit drops the matching entry immediately; a failed commit leaves it intact. The existing management history-deletion handler already wraps `Store.DeleteProtocolHistory` with this method.
- Any failed historical load drops the matching retained entry, including a missing durable revision, missing/stale/failed report, compiler/sample mismatch or definition mismatch. Out-of-band deletion is therefore rejected and evicted on the next load.
- Capacity pressure evicts the oldest entry. A successful load with a different admitted graph for the same revision key replaces the old retained graph without double counting.

Previously pinned in-flight graphs remain usable. Neither graph retention nor the offline memo creates durable evidence or permission.

## Alternatives considered

**Trust a preset ID or canonical hash.** Rejected: authored variants and alternative spellings can share those identities. Embedded exact-byte hashes and complete-key reads are required.

**Move the embedded loader or add provider-specific admission branches.** Rejected: directly embedding the existing source tree inside `protocol` authenticates generic package identities without a package cycle, exported loader API or provider branches.

**Key the memo by every context combination.** Rejected: a metrics owner is mutable execution-local state, not an immutable semantic cache key. Conservative bypass keeps one bounded report and avoids sharing caller-owned state.

**Return retained revisions before consulting durable evidence.** Rejected: it can serve deleted or unverified history. Strong retention is only a lifetime owner for the existing process cache; durable authorization remains on every historical load.

**LRU or a new retention limit.** Rejected: the existing engine `BufferBytes` / `StateItems` limits and oldest-first policy suffice. No production limits are enlarged.

## Consequences

Embedded presets cannot be displaced by an ID collision. Caller-supplied metrics conservatively forgo memo speedups. Historical job polling keeps graph identity through GC without trusting cached authorization, although durable reads, exact-source hashing and the service mutex remain on that path. Strong history retention increases graph lifetime within the pre-existing definition-byte/entry bounds; the bounds do not claim a hard graph-RSS limit.

Tests cover variant-first and whitespace-variant admission, same-session embedded admission, warm pinned reads independent of weak entries, unknown IDs and complete-key/ID isolation. Context tests compare alternate-context cold/warm reports with uncached execution, cover cancellation, expiry and partial runs, and ensure cancellation after cloning cannot be hidden. Retention tests force GC between loads, verify authoritative rereads, exercise both capacity dimensions across twelve revisions, prove FIFO hits do not refresh age, and check deletion, failed commits, invalid evidence and recovery.

An external `protocol_test` regression compares the SHA-256 of every actual `builtin.Packages()` output with the private embedded hash inventory through a copying test-only accessor. It checks ID membership in both directions, rejects empty/duplicate shipped IDs, and requires every space-prefixed package to miss every embedded hash. Thus drift in either package assembler or either embedded ID set fails CI without adding a production import cycle or exported API. The pinned lookup is unchanged: complete-key comparison already short-circuits the embedded-inventory lookup and compiled identity check for nonmatching keys; the bounded four-slot scan does not justify additional indexing.

No existing semantic assertion or fixture is relaxed or removed. The preset immutability sentinel still compares the complete graph fingerprint with a fresh uncached compile. Its reference now uses the **exact admitted embedded bytes**, rather than canonical `compiled.definition`, which compacts retained literal spelling. Its map-key sort now uses complete key fingerprints for non-string keys instead of `reflect.Value.String()` placeholders, which made `map[Value]` order nondeterministic. All keys and values remain fingerprinted; a regression test checks stable ordering and detects changed keys/values. A new source/key identity guard strengthens the sentinel. The entry-bound fixture uses the existing four-sample fixture's minimum `StateItems` rather than deleting samples to fit an invalid smaller test setup.

## Measurements

Environment: Apple device, macOS. Before measurements use commit `a9fc1b1d`; after measurements include the additional regression tests and the real embedded preset sentinel. These are single bounded runs, not an isolated-system throughput comparison.

Every Go command runs with `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`. Tests additionally use `-parallel=2 -count=1`. Ignored `.tmp-dev/testperf/fable-c/watch.py` starts a separate process group, samples summed group RSS every 100 ms and kills the whole group at 2,000,000,000 bytes. It also wraps `/usr/bin/time -l`. No whole-server test run, whole-module test run or server race run is performed.

Commands from the repository root (each prefixed by `python3 -I .tmp-dev/testperf/fable-c/watch.py <label>`):

```sh
go -C backend test ./protocol -parallel=2 -count=1
go -C backend test ./protocol/... -parallel=2 -count=1
go -C backend test ./internal/architecture -parallel=2 -count=1
go -C backend test ./server -run 'TestProtocol|TestProductionStartup|TestProductionReload|TestPreset|TestGatewayJob' -parallel=2 -count=1
go -C backend vet ./protocol/... ./server ./storage
```

| Run | Before wall s | After wall s | Before peak group RSS MiB | After peak group RSS MiB | Before maximum-process RSS MiB | After maximum-process RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Full `./protocol` | 13.688 | 8.411 | 980.8 | 957.9 | 937.8 | 560.8 |
| Focused server regex above | 31.165 | 34.696 | 963.1 | 985.4 | 908.3 | 926.5 |

Go-reported package execution time is 7.968 → 7.927 s for `./protocol` and 20.947 → 26.029 s for the focused server run. Wrapper wall time includes compilation, tool startup and watchdog teardown; differing build-cache state and system activity prevent attributing these single-run changes to throughput. The server observation is slower, not a claimed speedup. All runs finish below the watchdog threshold.

Additional passing checks: `./protocol/...` takes 49.675 s at 583.1 MiB peak group RSS (`./protocol` 5.348 s, `./protocol/builtin` 38.018 s); architecture takes 0.968 s at 84.2 MiB; vet takes 4.520 s at 322.4 MiB. The focused correction/fingerprint regressions pass in 7.615 s at 1000.3 MiB group RSS using `-run 'TestCompilationCache|TestCompilationSessionVariant|TestVerificationMemo|TestLoadRevision|TestCompiledFingerprint'`. No watchdog kills occur. `git diff --check` is clean.

The cross-package byte-identity follow-up passes for all four shipped packages using `go -C backend test ./protocol -run '^TestCompilationCacheEmbeddedHashesMatchBuiltinPackages$' -parallel=2 -count=1 -v`: 14.174 s wrapper wall, 0.632 s package execution, 991.0 MiB peak group RSS. Focused `-run 'TestCompilationCache|TestCompilationSessionVariant|TestCompiledFingerprint'` passes in 3.731 s wrapper wall at 248.6 MiB; architecture passes in 0.977 s at 111.5 MiB. These runs use the same environment/watchdog and trigger no kills. This follow-up changes only tests and this bilingual note.

The complete backend suite, full server package, race testing and live upstream/provider verification are deliberately unverified under this task's run bounds. No performance claim is based on a watchdog-killed run.
