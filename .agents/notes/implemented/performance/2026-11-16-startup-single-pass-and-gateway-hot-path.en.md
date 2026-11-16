# Agent Note: Single-pass startup and gateway hot path

Status: implemented

## Problem

Startup repeatedly compiles the four preset packages: lazy service construction can load them before preset recovery and runtime reload each create their own compilation session. Temporary compiler graphs grow the heap far beyond the live registry, leaving substantial resident memory after initialization. Gateway requests also serialize around a conversion-generation SQLite read and rebuild outbound inputs merely to compare them against preflight inputs with `reflect.DeepEqual`.

## Decision

Startup constructs the protocol service without its lazy reload, then runs one full refresh/publication attempt with one `CompilationSession`. Existing baseline/evidence rereads still protect changing recovery plans. Stored preset definitions identical to, or JSON-equivalent to, the embedded definition do not require an auxiliary compilation; other spellings/default representations still follow existing validation and repair rules. Publication rereads authoritative revisions and verification evidence. Initialization failure keeps management available and generation disabled; if the full refresh fails for a reason other than an isolated conversion error, a preset-only refresh/publication runs on that failure path alone so presets stay committed and available for repair. A successful initialization calls `debug.FreeOSMemory()` once; management reload paths do not scavenge.

`Store` loads the persisted conversion generation at open. Trigger-covered writes read the resulting generation inside their transaction, commit, and only then monotonically publish it to an atomic counter. Single-statement source/model/binding writes now use the same transaction helpers. Audited paths cover source creation/settings/keys/enabling, bound-source saves, replacement/merge/manual/discovery catalogs, model availability/edits/deletion, group saves/deletion, managed bindings, conversion activation, protocol refresh/archive, function-tool enabling, and `ExecRaw`. Membership-only group writes and unrelated token/log/continuation writes do not touch trigger-covered tables. The DB generation and optimistic-concurrency checks remain authoritative; no schema changes are required.

Gateway conversion snapshots use atomic pointer/generation reads on the unchanged path and lock only for rebuilding. A candidate's shared preparation function constructs its actual outbound request and evaluation options for both eligibility and forwarding. The first eligible generation candidate retains that encoding and its diagnostic sink. Alternative/released candidates re-encode on dispatch, and cache synthesis deliberately re-encodes changed inputs. The old deep comparison and compiler reuse-capability flag are removed. `finalizeRequestWire` is unchanged.

The architecture allowlist explicitly recognizes the existing dependency-free `internal/expressionencode` generator, whose missing entry caused the initial architecture check to fail.

## Measurements

Environment: Apple device, macOS, Go 1.27.3 darwin/arm64. Single observations, not statistical benchmarks. The temporary test opens a fresh temporary config/SQLite store, forces GC/scavenging before measurement, then times only `initializeProtocolRuntime`. Restart measurement seeds the DB outside that interval. `TotalAlloc` is an interval delta; heap fields are sampled immediately after initialization. A temporary Go overlay increments an atomic counter at `Compiler.Compile`; production code has no measurement logging/hooks. The temporary test and overlays are deleted afterward.

Commands shown from the repository root; execution used the absolute worktree backend path with the same flags:

```sh
go -C backend test -overlay=../.tmp-dev/startup-measurement/overlay.json ./server -run '^TestStartupMeasurement$' -count=1 -v
MEASURE_RESTART=1 go -C backend test -overlay=../.tmp-dev/startup-measurement/overlay.json ./server -run '^TestStartupMeasurement$' -count=1 -v
```

| Case | Wall ms | Compiles | TotalAlloc MiB | HeapSys MiB | HeapReleased MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| Fresh, before | 1759.557 | 8 | 1916.46 | 342.56 | 2.12 |
| Fresh, after | 1413.019 | 4 | 1297.24 | 251.00 | 128.58 |
| Healthy restart, before | 985.234 | 12 | 1650.25 | 390.41 | 46.23 |
| Healthy restart, after | 462.090 | 4 | 825.11 | 246.66 | 128.67 |

Fresh startup reduces observed wall time by 19.7% and allocations by 32.3%; healthy restart reduces them by 53.1% and 50.0%. These measurements precede the final exact-byte comparison shortcut and the final storage commit-helper cleanup; the final tree is not remeasured. `HeapSys - HeapReleased` is not process RSS, so these results do not claim an RSS measurement.

Validation status: **pending: test-suite memory fix**. The following completed before the request to stop all further tests/benchmarks:

- `go -C backend vet ./...`: passed on an intermediate tree, before final storage-helper changes.
- `go -C backend test -overlay=../.tmp-dev/startup-measurement/overlay.json ./server -run 'TestGatewayOutboundEncoding|TestGatewayPreparationHandoff|TestProtocolRefreshCompilation|TestProductionStartup|TestProductionReload|TestProtocolStartup' -count=1`: passed, 21.395 s, before final cleanup.
- `go -C backend test ./storage -run 'TestCachedConversionGeneration|TestConversionGenerationDiscardedWrites|TestConversionSnapshotPublishesGeneration' -count=1`: passed, 0.477 s, with explicit postcommit publication; the later expanded mutation-path test has not run.
- `go -C backend test ./server/... ./storage/... ./internal/... -count=1`: failed/incomplete. The server test process was killed after 588.797 s under memory pressure; storage passed in 1.769 s; architecture reported the existing missing `internal/expressionencode` allowlist entry. That entry is now fixed but unverified.
- The serial retry, `go -C backend test ./server/... ./storage/... ./internal/... -parallel=1 -count=1 -timeout=30m`, was stopped as requested, before producing results. Its wrapper set `GOMEMLIMIT=1GiB`, `GOMAXPROCS=2`, `GOFLAGS=-p=1`, `GOGC=50`, and a 1.75 GiB process-group RSS watchdog. No bounded-suite result or peak-memory claim is available.
- Final `go vet ./...`, the full requested suites, standalone `go test ./internal/architecture -count=1`, race checks, and final startup/gateway measurements remain pending. No further tests or benchmarks run this round; owned test processes are stopped. `git diff --check` passes.

## Alternatives considered

**Share a session across two startup passes.** Rejected by the user: it retains duplicate refresh/publication orchestration and does not address the startup-only lazy reload. One refresh/publication attempt removes the redundant work rather than masking part of it.

**TTL caching plus request fingerprints.** Rejected by the user: a TTL permits stale routing/binding evidence, while fingerprints still traverse/allocate over request inputs and require another invalidation contract. Committed generation publication and owned outbound inputs avoid both mechanisms.

**SQLite preupdate/commit hooks.** Rejected during implementation: SQLite commit hooks run before commit can finally succeed, and statement/savepoint rollbacks can discard preupdate events. Counting those hooks could publish uncommitted or incorrect generations. Explicit postcommit publication preserves the DB trigger semantics instead of reproducing them.

## Consequences

Startup retains one verified registry and returns compiler churn to the OS without adding GC/scavenging work to management reloads or requests. The unchanged gateway conversion-snapshot path performs neither SQL nor global locking, and ordinary first-attempt forwarding needs no deep request/options comparison or duplicate clone/encoding.

The mutation path pays one generation read inside its transaction; previously autocommitted covered-table writes now have an explicit transaction. Future writes to trigger-covered tables must use the same commit helpers. Separate processes/Store instances or raw external SQLite handles do not notify this atomic counter; supported writes must go through the owning Store, and external repairs require reopening or an authoritative snapshot reload. Alternative candidate encodings are intentionally not retained, bounding preparation lifetime at the cost of re-encoding those candidates if dispatched. Final validation is not complete, and the memory-heavy suite must be fixed/bounded before the pending checks resume.
