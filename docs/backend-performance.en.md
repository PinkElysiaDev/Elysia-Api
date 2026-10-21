# Backend performance benchmarks

[简体中文](backend-performance.md) · [Development guide](development.en.md)

This harness collects Go backend baselines and comparisons, measuring allocation counts/volume, memory peaks and idle recovery, and attributing costs. This guide retains reusable collection methods; measured conclusions have one current report entry point.

## Latest report and retention

- [Latest profiling report (#91)](backend-performance-algorithm-91-final.md): size-scaling benchmarks, HTTP ABBA, CPU/allocation attribution and explicit regressions.
- [Complete evidence and verification](artifacts/performance-algorithm-91/): raw measurements, 40 profiles, cancellation diagnostics, rejected trials and conflicting results from this round.
- The working tree retains only this round's final report and complete evidence. Earlier version comparisons, #77/#81/#86 reports and archives, and #91 item reports remain accessible in the [pre-cleanup Git snapshot](https://github.com/PinkElysiaDev/Elysia-Api/tree/6a16292/docs). Reusable benchmarks, profiling tools and regression tests are retained.

**Version boundary:** the latest report measured integrated source `4fc5011` against `bdb02c6`; neither includes #90's lifecycle changes. The synchronized `deploy@6a16292` includes #90 and #92–#94, but has not been measured as a combined version. Collect a fresh baseline before optimizing it; do not relabel historical results as measurements of current `deploy`.

## Running

Run from the repository root with Node.js, the project's Go toolchain, and loopback networking. Providers, credentials and databases are local fixtures; no external provider account is needed.

```sh
# Full collection: microbenchmarks, HTTP matrix, separate CPU/allocation
# profiles, and cancellation diagnostics. Use a new, empty output directory.
# Builds, loads and profiles run serially.
node scripts/benchmark-performance.mjs --output=.tmp-dev/performance-baseline

# Fixture smoke check; this sample cannot establish stable tail latency.
node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,stream256-cross,candidates4 \
  --requests=2 --repeats=1 --idle-ms=100 \
  --output=.tmp-dev/performance-smoke

# Separate timing run with more observations and a longer recovery window.
node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,burst4,stream256,stream1024 \
  --requests=128 --repeats=5 --idle-ms=10000 \
  --output=.tmp-dev/performance-repeat

# Attribution only: separate memory and CPU runs per selected case.
node scripts/benchmark-performance.mjs --mode=profile \
  --profiles=chat-short,stream256,image2 --seconds=30 \
  --output=.tmp-dev/performance-profiles
```

Defaults fix `GOMAXPROCS=4`, `GOGC=100`, and `GOMEMLIMIT=2GiB`; environment variables override them. Builds use `sonic` and disable PGO. Compare on the same machine with matching settings and separate output directories. Avoid concurrent builds or other load tests during collection.

`--mode=micro` runs existing protocol/builtin benchmarks five times at 300ms each: short-request semantic pipelines, request workloads, stream decoding, typed resource checks and runtime schema validation.

## HTTP scenarios

| Case | Workload |
| --- | --- |
| `chat-short`, `anthropic-short`, `responses-short`, `gemini-short` | Same-protocol short requests for all four built-ins |
| `chat-anthropic`, `chat-responses` | Cross-protocol short requests |
| `prefix32k`, `history256k`, `tools32` | Long prefix, long history, 32 tools |
| `candidates4` | Four candidates; still asserts exactly one upstream generation per request |
| `reload-chat` | Reload the protocol runtime and invalidate routing; record reload cost separately from the first subsequent request |
| `burst4`, `burst4-body` | Four-worker closed-loop bursts with body logging disabled/enabled |
| `stream16`, `stream256`, `stream1024` | 16/256/1024 text deltas of 256 bytes, plus lifecycle/usage frames |
| `stream256-cross`, `stream256-burst4` | Cross-protocol streaming and four concurrent streams |
| `image2`, `image2-media` | Tool-result history containing 2 MiB of base64 text, without body logging/with media externalization |

Defaults are three repeats of 32 requests per case; image cases use four requests and reload uses one per repeat. Those two case-specific counts override `--requests`. Every response is checked for exact text and usage. The matrix provider checks history text/tool preservation; upstream generation counts must match request counts.

Logging `off` disables body capture only; usage accounting and SQLite persistence stay enabled. `body`/`media` use the existing fixture's 64 MiB body limit; `media` additionally enables externalization. These are fixed fixture settings, not deployment defaults.

Full mode also runs separate cancellation diagnostics: five batches of four concurrent 2 MiB requests, cancelling after receiving a real data frame and checking generation counts and memory after forced GC.

## Measurement boundaries

- Client, gateway and provider run in separate processes. Allocation, CPU, GC and memory figures come only from the gateway child.
- `first-request` measures the first business request to an initialized fixture service, including unwarmed request paths. It excludes process startup, database creation and protocol-fixture verification.
- Four warm-up requests precede formal measurement. Timing and natural recovery runs never force GC or restart the usage writer.
- HTTP latency spans request dispatch through complete response consumption. Exact validation runs in the client. Throughput is closed-loop completion rate including client validation, not maximum gateway capacity.
- `FirstByte` measures the first response body byte. `FirstFrame` measures the first complete SSE frame, or the complete JSON body. Neither necessarily measures the first model text token.
- Allocation and CPU counters include completion of asynchronous usage persistence; HTTP latency stops at response consumption, before any remaining logging wait.
- `settle` waits for requests and usage commits while keeping the writer alive. The older `drain/reclaim` operations are used only in separate forced-recovery diagnostics.
- After each settled batch, recovery samples are taken at 0%, 10%, 50% and 100% of the idle window. The default window is one second; extend it with `--idle-ms`. Recovery points retain cumulative allocation/GC/CPU counters to distinguish natural GC from waiting.
- A 20ms sampler records HeapAlloc, HeapInuse, HeapIdle, HeapReleased, RSS, goroutines and active requests. Sampled peaks may miss short spikes. Timelines are capped at 9,000 points with explicit dropped-sample counts.
- HeapAlloc includes uncollected garbage, not just precisely live objects. Linux RSS comes from `/proc/self/statm`; Windows uses working set. Other platforms or read failures report unavailable data, not measured zero RSS.
- Metrics HTTP handlers, the sampler and runtime background work reside in the gateway process and contribute some overhead. Timeline serialization occurs after the formal counters have been captured.

Percentiles use nearest rank. Summaries report medians of per-repeat metrics; raw per-request observations are retained. The default sample supports cost investigation, not a stable production P99 claim. Comparing stream lengths reveals scaling; dividing whole-request allocations by delta count does not isolate decoder allocations.

## Evidence and attribution

The output contains:

- `report.json`: commit/worktree state, Go/CPU/environment, build options, binary SHA256, commands, statuses and summaries.
- `source.patch`, `runner.mjs`: working-tree differences and collector source. Prefer a clean commit; other untracked sources must be versioned separately.
- `micro.log`: five raw microbenchmark repetitions, suitable for `benchstat`.
- `measure-*/measurements.json`: per-repeat/request results, recovery points and timelines.
- `memory-*/*.pprof`: before/after allocation profiles, a timed heap snapshot and a drained heap profile.
- `cpu-*/cpu.pprof`: profiles from separate CPU runs.
- `alloc_space-top.txt`, `alloc_objects-top.txt`, `inuse_space-top.txt`, `cpu-top.txt`: sampled attribution.
- `cancellation-*.json`, `cancellation.log`: separate cancellation diagnostics.
- `manifest.json`: evidence checksums; `summary.md`: compact results.

Profiling modes force GC before measurement; memory mode forces GC again at completion to update heap profiles. Their timings are not formal performance results. Allocation profiles subtract before from after to reduce startup contamination, but still include profiler overhead. Default Go heap profiles are sampled estimates, not an exact object ledger. `heap-active` is captured approximately 250ms after load begins; short loads may already have ended, so it is not a peak-heap claim.

```sh
go tool pprof -top -sample_index=alloc_objects \
  -base .tmp-dev/performance-baseline/memory-stream256/alloc-before.pprof \
  .tmp-dev/performance-baseline/server.test \
  .tmp-dev/performance-baseline/memory-stream256/alloc-after.pprof

go tool pprof -http=127.0.0.1:0 \
  .tmp-dev/performance-baseline/server.test \
  .tmp-dev/performance-baseline/cpu-stream256/cpu.pprof
```

Rerun matching scenarios after each optimization. Compare allocations/request, bytes/request, CPU/request, peaks and recovery curves together: fewer allocations should not cause sustained oversized-buffer retention. Scope zero-allocation budgets to specific helpers or steady-state stages; establish HTTP budgets after sufficient repetitions. Keep raw evidence in CI artifacts or local output directories and publish summaries and collection settings in the PR/issue.

## Local reuse and allocation budgets

Local optimizations keep resources with their request, stream or writer owner:

- SSE completeness tracking retains four trailing bytes; fragmented reads and EOF checks do not construct tail strings.
- Each upstream stream reuses one idle timer and notification channel. Every Read stops the timer and waits for an already-fired callback before allowing reuse. Consumer processing time does not count as upstream idle; Close cancels the request to interrupt a blocked Read.
- The usage writer reuses a 64-entry batch and timer, clearing batch references after persistence, including failures. The original 50ms collection window and payload accounting remain in effect.
- Final wire validation consumes typed events directly while preserving the semantic budget, JSON normalization and mutable-container isolation. Final byte checks and lifecycle replay still run; JSON-only decoders retain the generic path.
- Semantic deep cloning allocates mutable containers and destination structs; container field assignment copies scalar values without separate temporary holders. Immutable Value bytes can still be shared.

The following helpers measure fragmented SSE tail checks, a single read on an established stream, and final validation of 256 deltas plus start/end frames. The first two target `0 B/op, 0 allocs/op` in steady state, excluding stream construction, networking and arbitrary Reader internals.

```sh
cd backend
GOMAXPROCS=4 go test -tags=sonic -pgo=off \
  ./internal/eventstream ./relay ./protocol/builtin -run '^$' \
  -bench '^(BenchmarkSSECompleteReader|BenchmarkStreamBodyRead|BenchmarkFinalWireValidation)$' \
  -benchmem -benchtime=300ms -count=5
```

`TestFinalWireValidationTypedParity` compares typed and JSON final validation over identical built-in protocol samples, including rejection positions and terminal results. Focused regressions also cover timer reuse, consumer pauses, concurrent Close, candidate request isolation, and writer shutdown/failure release.

## Versioned plans and typed streaming

Gateway requests pin a decoded policy/binding snapshot after reading SQLite's committed conversion generation. Binding, activation, model, source and group writes advance that generation through existing database triggers. A new generation replaces the server's snapshot; in-flight requests retain their pinned snapshot. Read errors fail the request rather than serving a stale snapshot.

Each snapshot lazily compiles reusable conversion plans keyed by source/target revision hashes, source/model IDs, model identifier, operation and transport. At most 256 plans are retained per snapshot; reaching the cap clears the lookup without invalidating already-pinned executions. Compilation is synchronized. Protocol reloads select a different revision key even when the conversion generation is unchanged. Compiled rules are indexed by phase and typed eligibility is decided at compile time.

The shared plan contains only read-only policy/program data. Each candidate receives a separate execution wrapper. Provider evidence is loaded with the current account/model scope, upstream revision and time on each attempt; it is never stored in the shared plan. Diagnostics, delivery state, scope and continuation state remain request-owned. Operation selection iterates scalar route metadata and deep-copies only the selected transport operation.

Built-in event conversion operates on owned, normalized semantic containers. Authored JSON-dependent rules retain the JSON execution path. Typed event encoding preserves semantic preflight, mapping limits, context validation, cancellation and wire-output checks; mappings with schemas, post-transforms or initial output retain the generic path. No typed eligibility decision removes final wire validation.

`EncodeConvertedFrame` compares the completed conversion batch with the decode-time native digest and immediately encodes it. This avoids separately serializing the original batch and repeating the equality check within the call. It never exports or caches an equality proof for publicly mutable events; ordinary `EncodeFrame` continues to detect external edits on every call. Changed or buffered gateway batches are explicitly re-encoded.

Regression coverage includes typed/JSON event conversion and wire encoding across the four built-in text/tool stream families, normalization and resource/cancellation boundaries, native mutation checks, and concurrent execution-wrapper isolation plus generation/revision changes. Compare phase-three evidence against the clean phase-two commit, using the same HTTP matrix and environment above.

## Owned storage and bounded workspaces

The final phase reduces payload work and keeps reuse bounded by its owner:

- JSON syntax validation uses the standard-library scanner without decoding an object tree. Values are still validated and copied on entry, numbers keep their spelling, and protocol resource limits remain separate mandatory checks. The former strict Sonic decoder remains a differential fuzz oracle.
- Generic Value and strict semantic-contract decoding use a single string-backed decoder with number preservation, string copying and validation. Decoded strings cannot retain a large input through small views; strict contracts still reject unknown fields and trailing documents.
- JSON string spans use bulk quote search with backslash-parity checks. Semantic metering counts literal HTML/line-separator expansion without decoding strings; invalid UTF-8 still follows the codec's replacement behavior. Metering continues to charge every occurrence, including shared immutable payloads.
- Deep cloning writes directly into destination structs and slice elements. Only the mutable destination graph is allocated; immutable Values and the private immutable request baseline remain shareable. Reasoning validation uses a shallow local request header because its normalizer replaces fields and owns parameter edits. Candidate recovery clones input only when recovery is active; conversion still produces an independent result.
- Closed JSON containers use stack storage for up to 16 keys/parts and one exactly sized immutable result. Larger containers allocate temporary arrays for that call. Simple keys are written directly; escaped keys retain the standard codec. The common 4/16-field helper targets one allocation for the returned string, not zero-allocation HTTP encoding.
- Small codec results use the immutable string API. Results larger than 64 KiB are detached to avoid retaining excess capacity from a grown encoder buffer.
- Each gateway stream reuses its accepted-event slice up to capacity 16, clears payload references on every return (including failure), and discards larger backing arrays. Reuse is synchronous and stream-local; returned frame bytes do not borrow that storage.

No application-level cross-request buffer pool is added: common container temporaries are on the stack, event storage stays with one stream, and immutable returned JSON must own its bytes. Pooling those outputs would require another copy or a different lifetime contract. The codec and standard-library scanner retain their own internal reuse policies; cold/pool-miss costs remain part of measured runs.

```sh
cd backend
GOMAXPROCS=4 GOGC=100 GOMEMLIMIT=2GiB go test -tags=sonic -pgo=off \
  ./protocol ./internal/jsonutil -run '^$' \
  -bench '^(BenchmarkValueDecodeLarge|BenchmarkValueContainerEncoding|BenchmarkSemanticClone|BenchmarkStrictJSONValidation|BenchmarkTypedResourceCheck)$' \
  -benchmem -benchtime=300ms -count=5
```

`TestGatewayStreamWorkspaceReleasesFailedFrame` exercises actual replay failure after accepting a payload-bearing event and checks reference release and oversized-buffer discard. Clone isolation walks the closed semantic graph, including maps, nested slices and pointers. Fuzz checks cover strict syntax, escaped quote spans and exact semantic metering. Final HTTP comparisons should use phase three as the immediate baseline and phase one for the cumulative four-phase result; report remaining timing/peak regressions as well as allocation gains.
