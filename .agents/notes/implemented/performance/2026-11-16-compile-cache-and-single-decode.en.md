# Agent Note: Weak compilation cache and single decode

Status: implemented

## Problem

Every independent registry, validation, and management reload can rebuild the same four shipped protocol graphs. Server fixtures additionally decode and re-encode those packages into another source spelling. Repeated compilation and offline verification dominate startup allocation, and formerly independent white-box compiler fixtures can corrupt one another if immutable production graphs become shared. Expression presence detection walks every child subtree again, while repeated inline schemas each invoke the JSON Schema compiler.

## Decision

`protocol` owns a process-wide cache used by `Compiler.Compile`, `CompilePackage`, `CompileSources`, `Service.Validate`, compilation sessions, and persisted-revision reloads. Its key is `CompilerVersion`, the complete engine `Limits`, the sorted installed feature identity, and SHA-256 of the **exact definition bytes**, without JSON normalization. Authored mapping limits and all samples are included in those bytes. Distinct spelling, limits, features, or compiler versions cannot alias a graph.

Entries contain `weak.Pointer[Compiled]`. Concurrent misses on one key share a flight; errors are not cached, and each waiter owns its diagnostics. `runtime.AddCleanup` removes dead metadata only if the weak identity still matches, so a late cleanup cannot remove a replacement. Only explicitly admitted exact embedded package bytes are strongly retained: at most four graphs, one per ID, with the complete admission key checked on reads. Alternative spellings/configurations and authored variants are not process-pinned. Production registries already keep those preset graphs live. Compilation sessions keep their first-admission/publication contract, but their redundant linear deduplication walk is removed. Historical loads retain process-cache graphs under the engine byte/entry bounds while still rereading durable definitions and reports. These corrected pinning, context-input, and retention decisions are owned by [Compile cache corrections](2026-11-16-compile-cache-corrections.en.md).

Graph reuse is not authorization. Reload/publication still rereads revisions, checks identity/hash, reads authoritative stored reports, and runs `CanActivate`. Missing, failed, stale, or mismatched DB evidence cannot be replaced by cached offline evidence. Atomic snapshots, in-flight pins, rollback, and repair isolation remain intact.

### Immutability audit

All executable `Compiled` fields are initialized before cache publication: definition/identity/hashes, limits and capabilities, native policy, mapping/output plans, wire contracts and validators, operation/task/model/Agent plans, token-count children, and package plans. Compile-only expression facts are released first. Public package/operation/capability accessors return independent values or copies. JSON Schema validation creates its own local validator. Traversal indexes/lazies, budgets, native replay workspaces, and stream transactions remain request-owned (`valueReader`, evaluation/input views, and evaluation state). No production runtime graph mutation was found.

Offline evidence is the deliberate exception: a separately allocated mutex-protected memo, not an execution lazy. Tests that intentionally mutate private limits or replace mappings/contracts use a `_test.go` header/map detach; the Agent output slice is detached too. The mutation audit found header writes, outer map-entry replacement, and Agent slice-element replacement, not writes through retained runtime-plan pointers. Existing independent-plan and resource-limit assertions remain unchanged. A protocol `TestMain` sentinel freshly recompiles retained presets and compares their limits and structural graph fingerprints after the suite, excluding only the synchronized evidence memo. Cache tests exercise actual shared results rather than detached fixtures.

### Offline evidence inputs

`Verify` is not literally pure: it stamps `VerifiedAt`, observes cancellation/deadlines, and inherits `RequestBodyLimit`. Its **successful complete evidence** is deterministic for these enumerated inputs:

- The compiled source identity, compiler version, engine/authored limits, and installed features.
- Every ordinary, event-sequence, model, token-count, task, session, and Agent fixture, including sample IDs/order, scope, context, input, expected output/diagnostic, and capability declarations. These are all in the complete definition bytes.
- The effective request-body budget and caller-owned document metrics. Only the default budget without a supplied metrics owner is memoized; other inputs bypass the memo rather than growing a per-graph option map. The complete context-reader inventory is in [Compile cache corrections](2026-11-16-compile-cache-corrections.en.md).

Offline execution has no provider/network input and uses explicit synthetic operation identity/time. Combination/profile verification is separate and is not cached. Supplied context metrics bypass memoization conservatively, even though they store only policy-independent facts. Canceled, deadline-exceeded, failed, and partial runs never populate the memo. A waiter rechecks cancellation after acquiring the memo lock. Each call receives a deep clone of checks, nested capabilities, coverage, issues, and target scope, with its own `VerifiedAt`. Equal timestamps yield byte-identical cached/uncached reports. **A cached report means these fixtures were not re-executed for that call in this process**; it is not a new upstream proof or permission to ignore stored evidence.

### Decoding and schema reuse

`CompileSources` validates each independent JSON boundary, meters original bytes/tokens including duplicate keys and package nesting, then performs one typed package decode. `ParseValue` is syntax validation plus an immutable byte copy, not a parsed DOM; raw token metering must remain before typed normalization. Independent boundaries are checked even on a cache hit, preventing invalid source splitting from borrowing an otherwise valid joined-package key. Oversized input is rejected before allocating the joined buffer.

Only typed-decode failures use a small diagnostic-only independent-source fallback. It preserves the established source identity, code, path, and codec message; it cannot compile or turn rejection into acceptance. If it finds no source error, the original attributed rejection remains. A fail-closed fallback test and existing rejection tests protect this boundary. This is smaller than introducing a second source-offset-aware parser.

`Expression.UnmarshalJSON` decodes each field and records presence in the same walk, using decoder positions rather than rescanning child values. The old strict decoding/presence path remains only for malformed, unknown, or mixed-case input so its acceptance and diagnostics stay compatible. A legacy test oracle covers duplicate fields, escaped keys, null/zero/false, mixed case, malformed JSON, and exact error messages.

Inline wire schema plans share a per-compilation content memo across Spec and Mapping expression compilers. Keys are deterministic schema encodings; limits are fixed for that compilation and there are no expression-scope-dependent schema options. Successful plans and their JSON Schema validators are shared; errors are never memoized and callers keep their own authored site attribution.

Server preset fixtures now preserve embedded source bytes and exercise real `CompilePreset` admission before unchanged DB seeding and runtime initialization. They no longer manufacture a second typed/re-encoded preset spelling. This is not a shared or pre-seeded registry and skips no sample, evidence, reload, or authorization check.

## Alternatives considered

**Test-only seeded/shared registry.** Rejected by the user: it masks production reload/compilation cost and risks skipping evidence boundaries. Production caching supplies reuse; fixtures only preserve production source spelling.

**Strong general-purpose LRU.** Rejected: it retains arbitrary expensive custom graphs and introduces capacity/tuning policy. Weak entries plus exactly one admitted graph per shipped preset bound strong retention without such a policy.

**No Verify cache.** Timestamp/context dependence prevents caching whole reports naively. Instead, only complete successful default-budget evidence is memoized, copied, and stamped per call. Failures and alternate budgets still execute normally.

**Normalize definition keys or schemas across compiles.** Rejected for definitions: source spelling, duplicate fields, and raw resource usage are part of admission. Schema reuse is restricted to one compile and its fixed options.

**Source-offset-aware second parser.** Rejected in favor of the smaller diagnostic-only fallback. The fallback is fail-closed and preserves the existing strict decoder's messages.

## Consequences

Unchanged registries and test servers share immutable executable graphs without sharing request state or granting activation. Warm server startup performs no preset compilation or verification work after first admission. Custom graphs and their evidence remain collectible; weak metadata cleanup is asynchronous. At most four exact embedded preset graphs remain live for the process lifetime; verified historical graphs additionally have bounded service-owned retention. Compilation still costs SHA-256 over exact bytes; source-pair calls also validate independent boundaries. Cached Verify calls allocate independent report slices rather than returning mutable shared evidence. The source diagnostic fallback deliberately retains a cold compatibility path.

## Measurements

Environment: Apple device, macOS, Go 1.27.3. Another agent runs concurrently; wall/RSS observations are single bounded runs, not isolated-system claims. Every Go command uses `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`; tests use `-parallel=2 -count=1`. An ignored `.tmp-dev/testperf/fable-b/watch.py` sums process-group RSS and kills the group above 2 GiB. `/usr/bin/time -l` supplies wall time and maximum process RSS; the watchdog independently samples aggregate RSS. No full server package, full Go suite, or unbounded run is used.

Commands, from the repository root, all run under that watchdog and `/usr/bin/time -l`:

```sh
go -C backend test ./protocol -parallel=2 -count=1
go -C backend test ./protocol/builtin -parallel=2 -count=1
go -C backend test ./server -run '^TestChatCompletions' -parallel=2 -count=1
go -C backend test ./protocol/builtin -run '^$' -bench 'BenchmarkPackageCompile|BenchmarkVerifyPreset' -benchmem -parallel=2 -count=5
```

| Command | Before wall s | After wall s | Before max RSS MiB | After max RSS MiB |
| --- | ---: | ---: | ---: | ---: |
| `./protocol` | 26.17 | 12.16 | 946.8 | 957.6 |
| `./protocol/builtin` | 48.11 | 34.34 | 545.8 | 482.3 |
| Focused ChatCompletions | 45.85 | 15.62 | 850.4 | 546.7 |

Five-run medians follow. `BenchmarkPackageCompile` retains its existing loop: because it does not pin a result between iterations, the weak cache may miss after GC. Thus these are repeated API costs, not isolated cold-compilation costs or guaranteed-hot costs. Verification medians include independent report cloning.

| CompileSources preset | Before ms/op | After ms/op | Before B/op | After B/op | Before allocs/op | After allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Chat | 66.796 | 7.029 | 80,990,534 | 11,785,869 | 260,924 | 14,402 |
| Responses | 148.747 | 13.122 | 200,977,274 | 24,977,023 | 1,191,024 | 86,760 |
| Anthropic | 60.975 | 7.210 | 67,033,714 | 16,619,300 | 273,878 | 27,617 |
| Gemini | 62.781 | 4.584 | 70,237,544 | 10,723,865 | 257,120 | 13,838 |

| Verify preset | Before ms/op | After microseconds/op | Before B/op | After B/op | Before allocs/op | After allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Chat | 100.033 | 1.690 | 68,799,366 | 8,048 | 416,020 | 43 |
| Responses | 112.697 | 2.668 | 74,731,136 | 11,024 | 631,697 | 59 |
| Anthropic | 149.825 | 2.211 | 120,938,165 | 10,784 | 796,912 | 50 |
| Gemini | 108.250 | 2.071 | 92,339,668 | 10,320 | 630,305 | 47 |

A temporary, unshipped Go overlay counts `Compiler.Compile` API entries, actual `compilePackage` builds, `Verify` entries, and actual uncached verification runs. Five sequential `newTestServer` subtests run in one process, with each server cleaned up before the next. The measurement command is:

```sh
go -C backend test -overlay=../.tmp-dev/testperf/fable-b/overlay/overlay.json ./server -run '^TestSequentialStartupCacheMeasurement$' -parallel=2 -count=1 -v
```

| Startup | Initialization wall ms | Compile API calls | Actual compiles | Verify API calls | Actual Verify runs |
| --- | ---: | ---: | ---: | ---: | ---: |
| First | 966.532 | 16 | 4 | 4 | 4 |
| Warm 1 | 202.031 | 16 | 0 | 0 | 0 |
| Warm 2 | 215.095 | 16 | 0 | 0 | 0 |
| Warm 3 | 240.470 | 16 | 0 | 0 | 0 |
| Warm 4 | 218.955 | 16 | 0 | 0 | 0 |

The initial overlay before fixture source preservation measured 12 first-start builds and 7–8 warm builds, identifying the artificial second spelling. The final overlay passes its zero-warm-work assertion. Counts are not production logging or shipped hooks.

Final bounded `go test ./protocol/...`, `go test ./internal/architecture`, focused server `-run 'TestProtocol|TestProductionStartup|TestProductionReload|TestPreset'`, and `go vet ./protocol/... ./server ./storage` pass. The full non-race suite's five-minute target and full-suite peak RSS remain **unverified**: running the whole server/full Go suite is outside this task's memory-safe run authorization. Race testing and isolated cold-compilation profiling are not performed.
