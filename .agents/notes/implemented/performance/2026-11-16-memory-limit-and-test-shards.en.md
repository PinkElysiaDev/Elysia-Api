# Agent Note: Memory limits and independent backend test shards

Status: implemented

## Problem

The gateway retains substantially more RSS after request bursts than its live heap requires. Startup scavenging alone does not recover later garbage or retained spans. A process-wide budget must have a useful default without silently overriding an operator's explicit limit, and reclamation must not become a per-request GC operation.

The full ordinary backend suite also spends most of its wall time in one server process. Resource limits alone do not remove repeated protocol fixture construction or permit independent heavy groups to finish concurrently. All existing tests, assertions, cases and samples must remain selected.

## Decision

- `main` applies `debug.SetMemoryLimit` before constructing the server. A positive existing-config `memoryLimitBytes` value wins exactly, without clamping. Omitted/zero leaves the runtime limit untouched: an explicit `GOMEMLIMIT` (including `off`) applies, and with neither set no limit is imposed. Negative config values are rejected on load/reload. This setting requires restart. Embedded `server.New` does not change the host's global limit. No production default is imposed. The measurements below were taken while a 256 MiB default was still shipped; that default never bound on the measured workloads (GC cycles and peak RSS identical to the unlimited run), so it reclaimed nothing while pacing the collector on anything larger. Reclamation is the idle worker's job, and its recovery is independent of the soft limit. The bounded-test-memory target comes from the runner's explicit `GOMEMLIMIT`, not from a production default.
- Preserve startup `debug.FreeOSMemory()` in `server/protocol_runtime.go`. A lifecycle-owned worker samples once per second and requires five quiet seconds, a 30-second reclaim cooldown, and either 32 MiB of allocation churn since its initial baseline/last reclaim or at least 32 MiB of unreleased idle spans representing at least half the in-use heap. The allocation trigger also catches post-burst garbage still in in-use spans.
- Request admissions increment an epoch under the existing admission mutex: no per-request timer, clock read, memory-stat read, goroutine or GC. The worker also checks detached Agent turns, gateway sessions, source/catalog refresh, usage retention, health probes and job transport. It rechecks idleness and admission epoch immediately before reclaiming, stops on lifecycle cancellation, and joins before shutdown closes dependencies. Admissions are not blocked during collection.
- Reuse `scripts/verification/backend.mjs` and its existing bounded scheduler/process-group cleanup. Ordinary `all` independently schedules every non-server package and eight disjoint server shards in four shared slots by default. Each process uses `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`, `-parallel=2 -count=1 -timeout=15m`, with the unchanged 16-minute external timeout. `--jobs=1..4` adjusts the process count. The final server complement automatically includes new tests, fuzz targets and examples. Plain `go test ./server` remains unsharded and complete.
- Race retains its separate 2 GiB / `-p=2` budget and serialized heavy-task policy. Focused suites and the default conversion selection remain unchanged. CI invokes the same ordinary full entry point before the existing race tier. Optional `ELYSIA_VERIFICATION_DIR` keeps evidence in ignored worktree scratch; the default location is unchanged.
- Test-only fixture helpers return exact authored preset bytes only when the typed fixture encoding is byte-identical to the decoded default. Modified/custom packages retain their distinct complete encoding. Existing production compilation-cache identity, admission, activation and verification contracts are unchanged.
- The 12 media-tool-result cases share immutable presets and one server/store/provider fixture. Every case replaces group membership/order and conversion selections/evidence, clears counters, forgets the round-robin cursor and invalidates routes. The two include-delivery matrices share one encrypted store per strictness mode, retaining all eight signed/unsigned leaves in total. Unsigned executes first without creating a continuation; signed still asserts exactly one persisted continuation. Strict streaming rejection and upstream call-count assertions are unchanged.

## Alternatives considered

- **Per-request or periodic unconditional `FreeOSMemory`:** rejected because it adds avoidable latency and GC work, including during generation. Only the background idle policy reclaims after startup.
- **Treat the budget as an RSS ceiling or clamp explicit settings:** rejected. Go's limit is soft managed memory, and explicit configuration must be honored exactly. An excessively low budget is permitted but can cause GC throttling.
- **Apply the process limit in `server.New`:** rejected because tests and embedded hosts must retain ownership of their global runtime policy.
- **Only cap the original monolithic test process:** rejected because expensive fixture work remains serialized. A six-shard experiment passed but took 327.695 seconds; splitting the broad complement into independent management/wire/other groups removed that structural bottleneck.
- **Skip cases, weaken assertions, extend timeouts, fabricate passing evidence or add another compiled registry:** rejected. Fixture reuse uses existing verified contracts; no source case/sample/assertion or Go test timeout is removed or relaxed.
- **Unbounded process overlap:** rejected. Four shared slots bound ordinary overlap; `--jobs=1` is available when aggregate memory matters more than wall time.

## Consequences

The default process has a soft budget and post-burst recovery without hot-path collection. The idle check is deliberately not an admission barrier: a request arriving immediately after the final snapshot can overlap a GC pause. Background database rollup/log work is not a global process-wide quiescence guarantee. The budget excludes some non-Go allocations and cannot promise a hard RSS maximum. Low explicit budgets can trade substantial GC work for memory; production measurements below report that cost rather than assuming it away.

The accepted ordinary full run finishes in 239.055 seconds, 60.945 seconds below five minutes. This is one observation, not a cold-cache or statistically guaranteed CI SLA. Previous intermediate arrangements measured 292.122–327.695 seconds. Overlapping processes lowers peak single-process RSS relative to the serialized baseline but increases aggregate RSS. The measured 2829.03 MiB aggregate is 2.76 GiB, within the user's 3 GiB workflow allowance; neither per-process soft limits nor this single observation guarantee a hard 3 GiB aggregate ceiling. The new arrangement alone, using original fixtures, already reaches 254.286 seconds; fixture changes reduce repeated work but are not the sole cause of the wall-time improvement.

Authoritative compiled inventories retain all 2,349 original top-level Test/Fuzz/Example names and add seven tests. The final JSON logs contain exactly one terminal event for every one of the 2,356 final names, with no removed, orphaned, duplicate or unexpected names: 2,341 pass and 15 skip. The same 15 opt-in performance/live diagnostic tests skipped in the original run; no new skip is introduced. Server inventory is 615 → 620, including its existing example. Node tests additionally partition source declarations and synthetic future Test/Fuzz/Example names exactly once for both ordinary and race plans.

`go vet ./...`, `go test ./internal/architecture -count=1`, all seven Node runner tests, the final full ordinary entry point and targeted shared-matrix checks pass. An additional combined shuffled `-race -count=3 -timeout=5m` check times out after 301.047 seconds during the include matrices; no data-race warning is reported, but the invocation is failed, not successful race evidence. Its timeout is not enlarged. Narrowed three-repeat race checks pass for idle-memory tests (1.806 seconds) and the Agent concurrency guard (1.448 seconds). The additional media race invocation is canceled during finalization, and the queued include invocation does not run; neither is counted as passing. The complete race tier, Linux CI execution, cold-cache execution and live-provider verification are not measured. Temporary watchdogs, binaries, configuration, databases and logs are removed after measurement; no external provider is used.

## Measurements

Environment: macOS, Apple device, Go 1.27.3. Baseline is detached `65a1138b208d274651be07d953940da523ec80d1`. Timed tests/builds and production load do not overlap. Ordinary observations use the bounds above. A Python watchdog samples descendant-process RSS through `ps` approximately every 200 ms, sums simultaneous descendants for aggregate peak, and terminates at 1,100 seconds or 7 GiB. RSS values are sampled peaks, not exact kernel high-water marks. Small short-lived process peaks can be missed. Test results are uncached (`-count=1`); build caches are warm.

### Whole-suite wall time and sampled RSS

| Run | Wall seconds | Peak single process MiB | Peak aggregate MiB |
| --- | ---: | ---: | ---: |
| Original non-server packages, serialized | 77.138 | 526.66 | 586.95 |
| Original complete server test binary, unsharded | 410.327 | 1442.53 | 1442.53 |
| Original serialized phase sum | 487.465 | 1442.53 | 1442.53 |
| Original sources/fixtures with final four-slot partition | 254.286 | 975.84 | 2441.42 |
| Final actual developer/CI full runner | 239.055 | 1023.52 | 2829.03 |

The serialized sum combines separately measured consecutive phases; it is not a fresh single-command cold-build measurement. Its retained original server binary excludes compilation/linking; final runner wall includes Go command overhead. Relative to that sum, observed wall falls 51.0% (2.04×) and single-process peak falls 29.0%, while aggregate peak increases 96.1%. Relative to original fixtures under the same partition, wall falls 6.0%, but process/aggregate peaks increase. Between-run variability prevents attributing those percentages solely to individual fixture edits. User-provided earlier 64-second non-server / 503-second server measurements are a separate reference, not mixed into this controlled table.

### Server shard wall and test-process RSS

Original timings use the retained original binary; final timings include each `go test` command. Shared slots can delay task launch; table wall excludes queue time. RSS is the sampled test-process peak, not the sum of concurrent shards.

| Shard | Names after | Before seconds | After seconds | Before RSS MiB | After RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| Gateway / PackageGateway except heavy matrices | 86 | 144.122 | 130.230 | not measured by shard identity | 1023.52 |
| Management / lifecycle | 65 | 110.297 | 107.025 | 841.88 | 860.19 |
| Wire / conversion | 73 | 170.096 | 167.920 | 774.78 | 782.09 |
| Gateway heavy matrices | 12 | 133.960 | 89.004 | 589.19 | 602.69 |
| Agent | 50 | 75.241 | 79.152 | 484.16 | 526.56 |
| CLI / Ops | 30 | 46.823 | 39.176 | 695.27 | 696.81 |
| Protocol / Preset / RecoveredPresets | 35 | 45.167 | 56.361 | 911.02 | 939.86 |
| Automatic complement | 269 | 52.763 | 57.984 | 932.80 | 969.92 |

The original partition's maximum 975.84 MiB process had a truncated exit-time command identity, so no guessed per-shard attribution is supplied. Aggregate peaks are measured only for the complete overlapping process tree above, not independently isolated per shard.

### Non-server package task wall

Both columns use independent package tasks under the same four-slot partition; elapsed includes command/build overhead and excludes queue time. No-test packages are still scheduled and checked. Per-package aggregate RSS is not measured; the whole-run aggregate above includes these tasks. For long-running test processes, sampled before/after RSS is protocol 354.27/334.00 MiB, builtin 445.95/457.91 MiB, relay 263.39/261.88 MiB and storage 80.97/84.59 MiB. Reliable short-lived test-process peaks are not measured.

| Package | Before seconds | After seconds | Original → final names |
| --- | ---: | ---: | ---: |
| root | 2.871 | 4.437 | 0 → 1 |
| agent | 1.552 | 2.023 | 44 → 44 |
| config | 3.500 | 3.897 | 27 → 28 |
| internal/admission | 0.675 | 1.177 | 3 → 3 |
| internal/architecture | 0.546 | 0.686 | 1 → 1 |
| internal/atomicfile | 0.562 | 0.802 | 3 → 3 |
| internal/clishell | 0.240 | 0.216 | 0 → 0 |
| internal/coreplan | 0.769 | 0.733 | 0 → 0 |
| internal/dbpath | 0.184 | 0.184 | 0 → 0 |
| internal/eventstream | 0.854 | 0.940 | 3 → 3 |
| internal/expressionencode | 0.240 | 0.211 | 0 → 0 |
| internal/jsonutil | 0.575 | 0.648 | 3 → 3 |
| internal/outbound | 0.845 | 0.864 | 11 → 11 |
| internal/routing | 1.098 | 1.231 | 16 → 16 |
| internal/semanticmeter | 0.371 | 0.354 | 0 → 0 |
| internal/storagequery | 0.185 | 0.146 | 0 → 0 |
| logging | 0.643 | 0.813 | 4 → 4 |
| protocol | 12.464 | 11.994 | 868 → 868 |
| protocol/builtin | 68.631 | 70.001 | 630 → 630 |
| relay | 24.427 | 25.497 | 15 → 15 |
| storage | 4.155 | 4.155 | 106 → 106 |
| webui | 0.116 | 0.115 | 0 → 0 |

Under the same partition, top-level fixture execution times fall from 92.49 to 34.16 seconds for the media-tool-result matrix, 31.41 to 16.16 seconds for include delivery, and 24.89 to 12.84 seconds for package include delivery. All 12 + 4 + 4 leaf cases and assertions remain.

### Production RSS, latency and GC cost

Production binaries use a deterministic local HTTP upstream through real admin-created sources, bindings, groups and an inference token. Model-catalog synchronization is disabled; loopback is explicitly allowed. Every generated response is validated. No inherited `GOMEMLIMIT` is supplied. The order is original-1 → default-1 → explicit-96MiB-1 → explicit-96MiB-2 → default-2 → original-2, with fresh data/configuration each time. Each process performs three 2,000-request batches at concurrency 1 and three 10,000-request batches at concurrency 4; each batch has 256 additional warmup requests. Thus each variant contributes six batch observations per concurrency, not six independent hosts. RSS is sampled every 100 ms including startup/admin setup, with 8 seconds idle before load and 36 seconds after load to cover the reclaim cooldown. No forced-GC endpoint or test-child scavenger is used.

| Variant | Concurrency | Median req/s | Median p50 ms | Median p99 ms | Median GC cycles/batch | Median process CPU seconds/batch |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Original | 1 | 661.419 | 1.508 | 3.989 | 10.0 | 3.690 |
| Default 256 MiB | 1 | 894.791 | 0.990 | 2.748 | 10.0 | 2.580 |
| Explicit 96 MiB | 1 | 774.907 | 1.067 | 4.204 | 43.0 | 3.585 |
| Original | 4 | 1458.477 | 2.544 | 8.166 | 37.5 | 14.400 |
| Default 256 MiB | 4 | 1761.502 | 1.973 | 6.137 | 37.0 | 11.510 |
| Explicit 96 MiB | 4 | 1106.806 | 2.769 | 11.321 | 196.5 | 17.965 |

GC and process-CPU deltas include warmup and driver startup; throughput/percentiles exclude warmup. CPU is process time from `ps`, not an isolated GC CPU counter. Isolated GC-assist CPU and throttling-limiter activation are not measured. The tight budget nevertheless exposes the end-to-end GC-pressure cost: versus default at concurrency 4, throughput is 37.2% lower, p50 is 40.3% higher, p99 is 84.5% higher, GC cycles are 5.31× and process CPU is 56.1% higher. Versus original, tight throughput is 24.1% lower and p99 is 38.6% higher. A low explicit budget must not be advertised as free memory savings.

Default versus original has observed concurrency-4 deltas of +20.8% throughput, −22.4% p50 and −24.9% p99, with similar GC counts. These are **not a demonstrated speedup or a zero-cost guarantee**: original throughput ranges 987.605–1872.511 req/s and p99 5.615–14.770 ms, while default ranges 1458.449–1835.019 req/s and p99 5.772–7.646 ms. At concurrency 1 the throughput ranges are 448.047–1040.379 and 586.926–1026.579 req/s respectively. Large overlapping ranges and temporal variability prevent isolating a reliable default latency/throughput penalty from these observations. Costs under larger payloads, sustained near-live-set pressure and request arrival exactly during idle GC are not measured.

| Process | Before-load idle RSS MiB | After-load RSS MiB | After 36s idle RSS MiB | Peak RSS MiB | Heap after idle MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| Original-1 | 366.109 | 439.531 | 439.547 | 439.547 | 102.627 |
| Default-1 | 364.406 | 438.031 | 438.063 | 438.063 | 57.023 |
| Explicit-96MiB-1 | 236.938 | 213.297 | 213.297 | 258.438 | 54.716 |
| Explicit-96MiB-2 | 238.891 | 312.625 | 35.797 | 312.625 | 54.751 |
| Default-2 | 311.000 | 387.281 | 219.406 | 388.516 | 56.438 |
| Original-2 | 190.063 | 261.563 | 261.594 | 375.219 | 64.254 |

Default-2 recovers 167.875 MiB of RSS after load, but Default-1 shows no material RSS drop despite a roughly 57 MiB heap. Default peaks of 388.516–438.063 MiB do not uniformly beat original peaks of 375.219–439.547 MiB; all are below the separately supplied approximately 485 MiB dev.173 reference, not a new direct dev.173 comparison. One tight-budget RSS sample is below the Go-reported allocated heap; the two accounting measures are not interchangeable. The shipped policy bounds managed-memory pressure and attempts idle reclamation, but these observations do not establish a stable RSS ceiling or consistent physical-page return on macOS.

### Producing commands and inventory proof

Let `R` denote the authorized worktree `/Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime`. Historical scratch commands below describe the producing harnesses; they are intentionally removed after use rather than shipped as a second benchmark framework.

```sh
# Original binary was compiled before editing Go sources; executed from backend/server.
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 "$R/.tmp-dev/runtime/server-before.test" -test.v -test.count=1 -test.parallel=2 -test.timeout=15m
# Sampler commands, executed consecutively without competing builds/tests.
python3 -I "$R/.tmp-dev/runtime/measure.py" baseline-packages
python3 -I "$R/.tmp-dev/runtime/measure.py" partition-before
python3 -I "$R/.tmp-dev/runtime/measure.py" acceptance
# Candidate production binary and paired load, after all timed tests finish.
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 CGO_ENABLED=0 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend build -buildvcs=false -pgo=off -o "$R/.tmp-dev/runtime/gateway-after" .
python3 -I "$R/.tmp-dev/runtime/memory-load.py"
python3 -I "$R/.tmp-dev/runtime/summarize.py"
# Additional bounded race checks; combined invocation times out, narrowed checks pass.
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test -race ./server -run '^Test(IdleMemory|GatewayIncludeCarrierDeliveryModes|PackageGatewayIncludeRequiresClientCarrier|GatewayMediaToolResultCandidateIsolation)' -shuffle=on -count=3 -parallel=2 -timeout=5m
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test -race ./server -run '^TestIdleMemory' -shuffle=on -count=3 -parallel=2 -timeout=2m
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test -race ./agent -run '^TestRunTurn_ConcurrencyGuard$' -count=3 -timeout=2m
# Original/final inventories, including examples.
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test -overlay="$R/.tmp-dev/runtime/baseline-overlay/overlay.json" -list='^(Test|Fuzz|Example)' ./...
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test -list='^(Test|Fuzz|Example)' ./...
# Actual command launched by acceptance (also used in CI).
ELYSIA_VERIFICATION_DIR="$R/.tmp-dev/runtime/acceptance-runner" node "$R/scripts/verify-protocol.mjs" check --suite=all --jobs=4
node --test "$R/scripts/verification/backend.test.mjs" "$R/scripts/verification/runner.test.mjs"
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend vet ./...
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test ./internal/architecture -count=1
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test ./server -run '^Test(GatewayIncludeCarrierDeliveryModes|PackageGatewayIncludeRequiresClientCarrier|GatewayMediaToolResultCandidateIsolation)$' -count=1 -timeout=5m
```

`baseline-packages` uses a Go overlay restoring every changed tracked Go file from the original commit and removing new Go files; `partition-before` uses that overlay for non-server tasks and the retained original server binary with the final run/skip partition. The corrected complete original partition passes. Earlier setup failures caused by an incorrect fixture working directory or incomplete overlay are excluded, not treated as product failures or passing evidence.

Original and final inventories are produced with bounded `go test -list='^(Test|Fuzz|Example)' ./...`, the former with the same original-source overlay. Parsed package/name sets must satisfy original ⊆ final; each final package/name must occur exactly once among terminal top-level JSON events across final shard/package logs, and no extra names may appear. Platform-excluded source files are not confused with the compiled platform inventory. `summarize.py` checks those conditions and reads per-task reports and process-tree samples; the recorded totals above are actual checked results.
