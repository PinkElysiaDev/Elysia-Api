# Agent Note: Bounded backend test tiers

Status: implemented

## Problem

Repeated server fixtures compile and verify the same shipped protocols and source-binding combinations. Default Go package/test concurrency multiplies that work; the complete race check previously used one 60-minute test process with a 65-minute command timeout. Reported unbounded runs consumed 7–12 GB and took 20–40+ minutes. Resource bounds alone do not remove repeated fixture work or identify which part of a monolithic race run failed.

## Decision

- `scripts/verification/backend.mjs` retains the existing JSON/evidence runner. `fast` selects every non-server package; `all` adds every server test. Existing `conversion`, `gemini-stream`, and `output` names and the default `conversion` selection remain unchanged. Go discovers packages instead of maintaining a package allowlist.
- Ordinary defaults are `GOFLAGS=-p=1`, `GOMAXPROCS=2`, `GOMEMLIMIT=1GiB`, and `-parallel=2 -count=1 -timeout=15m`; each task has a 16-minute external timeout. `--jobs=1..4` controls task slots, not Go package parallelism. Ordinary full checks independently schedule non-server packages and eight disjoint server shards in four shared slots by default. Race retains `GOFLAGS=-p=2`, `GOMEMLIMIT=2GiB`, and two default slots. The current integrated policy and measurements are recorded in [memory limits and test shards](../performance/2026-11-16-memory-limit-and-test-shards.en.md).
- Full race checks use separate package tasks and four disjoint server tasks: Gateway/PackageGateway, Agent, Protocol/Preset/RecoveredPresets, and the `-skip` complement. Protocol, builtin, and server race tasks occupy all slots. Other package tasks share bounded slots. Each task retains independent logs and evidence; timeout/interruption terminates the process group/tree, escalating to KILL on POSIX.
- Preset fixture seeding keeps the exact embedded package bytes and reads only `spec.id`. Whole-package decode/re-encode materializes omitted defaults and causes startup to compile stored revisions/drafts again to prove equivalence. Seed-and-publish uses the existing production `CompilationSession`, closed after that publication; seed-before-initialization omits the redundant reload. This is not a test-owned compiled cache or registry.
- Agent execution fixtures reuse existing `fixtureBindingReports` proofs against real SQLite sources, models, bindings, and invalidated routes. Source authoring still uses production verification in `TestSourceSaveIsolatesUnrelatedInvalidBindings`, `TestSourceSaveKeepsIndependentModelProtocolOverride`, and `TestSourceBindingUpdatePersistsInheritedEvidence`; some Agent cases also retain `saveSource`.
- Usage-body policy cases share one server/store per policy, not per endpoint/outcome. All 18 leaf cases and assertions remain. Each case drains/stops the usage writer, clears usage records, and removes media assets before the next case. The next case restarts the writer, preserving the asynchronous path and exact one-record assertion.
- The two sequential Agent thinking matrices share only the immutable runtime and replace source/model/binding state completely for each case. Recovery defects and mutating tool probes keep independent stores/servers. Recovery setup reuses verified fixture reports; repair and the second idempotence initialization remain real. Expected preset hashes reuse the verified pre-corruption view instead of recompiling solely for assertions.
- New regressions cover preset publication after a non-isolated custom-refresh failure and semantic sequence conflicts when buffered source events expand into multiple projected events. No sample, defect case, subtest, assertion, protocol limit, or production timeout is removed or relaxed.

## Alternatives considered

**Test-only seeded registry.** Rejected by the user in favor of a separately implemented production compiled-protocol cache. These changes stand independently of that cache and use only normal persisted evidence and one-publication production sessions.

**gotestsum.** Rejected because it duplicates the existing runner's JSON parsing and per-task evidence/report handling.

**Bounds only.** Rejected because repeated fixture work remains and the race job stays monolithic, with poor timeout attribution.

**Sharing mutating fixtures.** Rejected for recovery corruption and tool-probe conflict cases. Those cases retain fresh state; only demonstrably replaceable sequential fixtures share a server.

## Consequences

Daily commands in `backend/TESTING.md` use bounded tiers, and CI also bounds vet/build parallelism. Server race selection includes existing fuzz seeds and future names through the complement. The Node partition test scans current top-level declarations; an additional bounded `go test -list` check observed 612 names partitioned exactly once: gateway 98, Agent 49, protocol 34, other 431.

Shared-fixture tests pass `-shuffle=on -count=3` in both normal and temporarily reversed case-table order. Route invalidation and storage conversion generations refresh replaced contracts; callers complete synchronously, reservations are released, no sessions/continuations are reused, and queued usage is drained between policy cases. Dedicated source-verification tests, both new edge regressions, runner tests, and `go vet ./server` pass.

GOMEMLIMIT is a soft Go-runtime bound, not a process-tree RSS guarantee. The isolated measurements below predate the integrated production cache; they did not run the whole server package or `go test ./...`. Current full non-race results and remaining race-verification limits are recorded in the linked integrated note. No test-teardown scavenging is added.

## Measurements

Single observations on an Apple device running macOS, based on `39b5a3fd`, **without** the concurrent production cache. Other work was concurrent, so these are not statistical comparisons. Wall time includes compilation; RSS below is `/usr/bin/time -l` maximum RSS in MiB. The watchdog separately sums RSS across the whole process group and kills at 2 GiB.

| Focused `-run` | Before wall | After wall | Before peak RSS | After peak RSS |
| --- | ---: | ---: | ---: | ---: |
| `^TestGatewayUsageBodyPolicy$` | 48.81 s | 13.73 s | 1038.7 MiB | 886.7 MiB |
| `^TestPresetRecoveryDefectsAndIdempotence$` | 43.33 s | 25.33 s | 711.0 MiB | 718.3 MiB |
| `^TestAgent` | 186.74 s | 89.00 s | 1908.6 MiB | 1812.6 MiB |
| `^TestChatCompletions` | 27.27 s | 19.29 s | 672.4 MiB | 546.2 MiB |

Final process-group peaks were respectively 940.8, 747.5, 1832.8, and 581.2 MiB; every final run passed without a watchdog kill. Usage-policy JSON test-execution time fell from 36.57 s to 5.09 s. The focused lifecycle/transport group completed in 85.98 s, with 1448.5 MiB maximum RSS. Three shuffled shared-fixture repetitions took 34.62 s; the reversed-table repetitions took 39.86 s. Final `go vet ./server` passed in 2.64 s under the same memory/concurrency bounds.

An intermediate full Agent run reached the watchdog cap before exact-byte seeding was fixed. A same-subgroup `-memprofile` comparison on `^TestAgent(Thinking|ToolProbe)` showed sampled allocation space decreasing from 24040.73 to 15508.67 MiB and end-of-run in-use space from 44.22 to 41.39 MiB. Live memory was predominantly Sonic decoder pools, not retained compiled graphs; memoized binding reports accounted for approximately 4 MiB. Final exact-byte/session seeding and both Agent source-fixture helpers resolve the full-group regression without scavenging.

Exact measurement shape, from the repository root (the ignored watchdog invokes `/usr/bin/time -l`, sets `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`, samples process-group RSS, and kills at 2 GiB):

```sh
python3 -I .tmp-dev/testperf/fable-a/watch.py shipped-usage go test ./server -run '^TestGatewayUsageBodyPolicy$' -parallel=2 -count=1 -timeout=15m -json
python3 -I .tmp-dev/testperf/fable-a/watch.py shipped-recovery go test ./server -run '^TestPresetRecoveryDefectsAndIdempotence$' -parallel=2 -count=1 -timeout=15m -json
python3 -I .tmp-dev/testperf/fable-a/watch.py shipped-agent go test ./server -run '^TestAgent' -parallel=2 -count=1 -timeout=15m -json
python3 -I .tmp-dev/testperf/fable-a/watch.py shipped-chat go test ./server -run '^TestChatCompletions' -parallel=2 -count=1 -timeout=15m -json
python3 -I .tmp-dev/testperf/fable-a/watch.py shipped-lifecycle go test ./server -run '^Test(Health|SourceRefresh|Retention|UsageWriter|UsageQueue|Shutdown|RequestedShutdown|Preparation|StreamCancellationUsage|GatewayJob|GatewayWebSocket|Hertz|MCP|A2A)' -parallel=2 -count=1 -timeout=15m -json
node --test scripts/verification/backend.test.mjs scripts/verification/runner.test.mjs
```

Post-change focused JSON timings still include fixture work because the production cache is not present. The largest remaining fixed transport wait is the two 5-second production HTTP shutdown deadlines in `TestShutdownForceClosesStuckHTTPConnection`; no injectable test-only timeout exists, so its real behavior and assertions remain unchanged. Remaining Agent, health, recovery, and source-refresh hotspots primarily compile/verify packages or binding matrices, not avoidable sleeps. Global slowest-test ranking belongs to the integrated full-suite evidence, not these focused groups.
