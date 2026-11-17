# Agent Note: Restore audited test-integrity gaps

Status: implemented

## Problem

An independent audit identified four test-integrity gaps. Finding #1: the process compilation cache makes pointers identical before and after preparation, allowing the preparation-to-publication handoff test to pass when publication returns success without publishing. Finding #2: cheap Agent fixtures bypass `saveSource`, leaving Anthropic and Gemini without positive source-authoring-to-Agent-call regressions. Finding #3: exact embedded-byte seeding no longer exercises benign recovery of current presets whose omitted fields have been re-encoded as explicit defaults. Finding #4: the cache-contamination sentinel checks only pinned presets, while the Responses capacity test changes limits directly on a `CompileSources` shared graph.

## Decision

- #1 Retain every existing preparation-state and pointer assertion and add `service.View() != previous`. `RegistryView` pins an immutable snapshot; healthy publication without persistent changes must still replace that snapshot, without requiring cached graph pointers to change. This directly rejects a successful no-op on the unchanged-store path.
- #2 Add `TestAgentCallerAuthoredAnthropicAndGeminiSources`, with just one independent server/store and one local streaming upstream per platform. Initial creation and the metadata edit both use production `saveSource`. Assertions cover persisted source records and verified bindings, preservation of explicit capabilities and source policy, rebuilding inherited evidence for a model child binding, and successful-save notification to the refresh coordinator, followed by one real Agent call with text and upstream-call-count assertions. Simulating an in-progress refresh registers only mutex-protected state, starts no background task, and removes that state during cleanup. Both synthetic aliases explicitly declare disable-capable budget capabilities; `seedAgentSource` and the cheap matrices remain unchanged.
- #3 Add `TestPresetRecoveryAcceptsReencodedCurrentDefaultsWithoutWrites`. Fully decode/re-encode one current Anthropic preset and explicitly assert that it is not structurally JSON-equivalent to the embedded source but compiles to the same identity/hash. Replace its current revision and draft with those bytes, then assert successful real initialization, executable presets, unchanged configuration/evidence baselines and row counts, and unchanged revision/draft bytes and timestamps. Non-equivalent JSON and current-draft-hash prerequisites force both revision and draft compilation fallbacks; preserving the stored results proves there was no spurious repair or archive.
- #4 After tests, `TestMain` reads every still-live weak entry through existing private cache structures, releases the lock, independently recompiles with that entry's compiler limits, and compares the complete structural fingerprint. Pinned presets must also appear in the enumeration and cannot silently disappear. Optional features gate admission, not graph shape; the reference compiler enables both optional features and still deliberately bypasses caching via `compilePackage`. Only the previously excluded, synchronized, intentionally mutable verification memo is omitted. Negative tests admit real custom graphs with non-default compiler limits into a local cache and prove that both header-limit and deeper-plan mutations are rejected. The Responses capacity test detaches the graph header through the existing helper before changing the retention boundary and adds an assertion that the shared original's limits did not change; both capacity cases and every existing check remain.

## Alternatives considered

**Require a different preset graph pointer on every publication.** Rejected because it conflicts with legitimate process-cache reuse. Snapshot identity already directly observes real publication without adding an unrelated custom-revision fixture.

**Restore production source verification throughout the Agent matrix.** Rejected because repeated combination verification increases everyday runtime. Two explicitly asserted authoring-path cases restore the missing coverage while the remaining matrices retain cheap fixtures.

**Restore typed re-encoding for every routine preset seed.** Rejected because every initialization would repeat benign fallback compilation. One targeted regression with non-equivalence prerequisites and no-write consequences covers the reported branches.

**Add production counters, admission hooks, or retain every historical graph.** Rejected because this task forbids production changes. Existing cache entries suffice to inspect every graph the cache can currently hand out, without changing weak lifetimes or adding permanent retention.

## Consequences

No production code changes, and no existing assertion, sample, table case, or limit is removed or relaxed. Code deletions only extract the former preset-only `TestMain` traversal and inline error handling into a testable all-cache checker; independent recompilation and structural comparison remain, with an additional error for a missing pinned preset. The existing bilingual compilation-cache note is also updated to describe the current sentinel accurately.

Still uncovered: the sentinel is not a real-time production detector and cannot observe temporary mutations restored before its scan. Collected weak graphs cannot be inspected but also cannot be handed out again. Other test binaries do not run this `TestMain`. The two positive provider source cases do not independently inject transaction rollback/CAS conflicts or every refresh race per provider; benign re-encoding selects one current preset rather than enumerating every default-field combination. Race checks, real billable upstreams, and other backend packages were not run in this task.

The complete `./server ./protocol` attempt failed when server reached its four-minute test timeout; command wall time was 259.040 seconds and peak process-group RSS was 891.609 MiB, while protocol passed in that attempt. The new Agent test was subsequently moved into normal `t.Parallel` scheduling and the focused set passed, but the complete server package was not rerun. The complete repository's five-minute non-race target is unverified, not claimed as achieved, and no timeout was enlarged to hide that. Final server verification selects only `^(TestAgent|TestProtocolRefresh|TestPresetRecovery|TestSourceSave|TestSourceBinding)`; server tests outside this set have no completed final whole-package verification. Within the set, the existing `TestAgentSnapshotPerformance` remains opt-in and skipped because `ELYSIA_AGENT_SNAPSHOT_OUTPUT` was unset.

## Measurements

Single observations on an Apple device running macOS, not performance comparisons. Every Go command uses an absolute `-C` pointing to this auditfix worktree's backend, with `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`. Shared test flags are `-count=1 -shuffle=on -parallel=2 -timeout=4m`. A temporary watchdog sums command process-group RSS approximately every 50 ms, terminates the entire group at 295 seconds or 2 GiB, and cleans up remaining processes. Its script and logs are deleted before commit. RSS is a sampled group peak, not a single-process high-water mark; Go's memory limit is itself soft.

| Command selection | Result | Wall seconds | Peak process-group RSS MiB |
| --- | --- | ---: | ---: |
| `test ./server -run '^TestProtocolRefreshCompilationPublishesPreparedPointers$'` | pass | 18.145 | 888.672 |
| `test ./server -run '^TestPresetRecoveryAcceptsReencodedCurrentDefaultsWithoutWrites$'` | pass | 6.322 | 559.766 |
| `test ./server -run '^TestAgentCallerAuthoredAnthropicAndGeminiSources$'`, final scheduling | pass | 37.855 | 896.672 |
| `test ./protocol -run '^TestCompilationCacheSentinelRejectsMutatedGraphs$'` | pass | 13.090 | 1001.297 |
| `test ./protocol -run '^TestResponsesPackageSourceCapacity$'` | pass | 1.581 | 280.109 |
| `test ./server -run '^(TestAgent\|TestProtocolRefresh\|TestPresetRecovery\|TestSourceSave\|TestSourceBinding)' -json` (unescaped pipes in the actual regex) | pass, 64 top-level / 135 including subtests passed, 1 existing opt-in skip | 91.102 | 1119.469 |
| `test ./protocol` | pass, including final cache sentinel | 12.116 | 338.906 |
| `vet ./server ./protocol`, final code | pass | 2.713 | 228.125 |
| `test ./server ./protocol`, before the scheduling adjustment above | fail, server test timeout, protocol pass | 259.040 | 891.609 |

The first development run of the new source test failed (20.442 seconds, 604.328 MiB): the borrowed generic synthetic Gemini level capabilities could not encode disabling thinking. It passed after this test explicitly declared budget capabilities supported by both mappings; no production behavior changed and no assertion was removed.
