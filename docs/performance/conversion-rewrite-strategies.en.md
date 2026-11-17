# Protocol conversion: three rewrite strategies

Status: proposed, ready to implement. Written 2026-11-17. Measured baseline: `fabb0bf8` (this branch plus a golden-corpus test commit, which is kept
out of this PR).

This document replaces the incremental plan in `phase-1-and-4-runbook.en.md` and
`phase-3-generated-executor.en.md` as the direction for performance work. Those two
documents are kept for their measurements and their "do not attempt" findings.

Acceptance bar for any strategy: **≥ 4× throughput, or ≥ 2× lower peak memory**, against
upstream `deploy` (dev.173 production code), on the eight handoff scenarios, with conversion output
byte-identical to the current interpreter.

---

## 1. Where we are

### 1.1 What was tried, and why it is abandoned

Round 2 (`2f1afba5` .. `d83433c5`, not merged) replaced the semantic document with an
arena-backed, epoch-stamped, copy-on-write `Node` and then added epoch proofs to skip
verdict comparisons (4.1) and frame digests (4.2). Measured end to end against dev.173 it
ran at **0.86×–1.11×**, below its own starting point `fabb0bf8`, and added roughly
380–490 allocations per request. 4.1 and 4.2 had no measurable pipeline effect. The work is
dropped; this branch starts again from the round-1 work.

The lesson is structural: every layer of ownership proof, epoch, side table and
copy-on-write added work to a pipeline whose cost is already dominated by generic
interpretation and repeated passes. Making a generic tree cheaper to copy and compare does
not remove the copies and compares.

### 1.2 `fabb0bf8` against upstream `deploy`

Upstream `deploy` (`bfe7638f`) differs from dev.173 (`e033c423`) in one backend test file
only, so dev.173 is built as the upstream reference. `fabb0bf8` is `deploy` plus 15
commits: the 14 on this branch (round-1 performance work, bounded test tiers, compile
cache) and a golden-corpus test commit that adds only tests and fixtures, not shipped code.

Method: `scripts/benchmark-performance.mjs --execution=production --mode=measure
--repeats=5`, `GOMAXPROCS=2 GOGC=100 GOMEMLIMIT=2GiB`, `-pgo=off`, Go 1.27.3, macOS,
Apple device. Three rounds alternating dev.173 → `fabb0bf8`; medians across rounds.
Absolute values are not comparable with the handoff's numbers, which were recorded on a
different setup; ratios are.

| Case | req/s (ratio) | CPU ms/req | P50 ms | P99 ms | allocs/req | peak Go heap MiB |
|---|---|---|---|---|---|---|
| `chat-short` | 560.3 → 600.6 (1.07×) | 1.66 → 1.53 | 1.36 → 1.34 | 4.84 → 4.59 | 3539 → 2946 | 421 → 218 |
| `history256k` | 76.0 → 80.4 (1.06×) | 12.79 → 11.49 | 11.23 → 10.54 | 33.47 → 28.02 | 57586 → 35602 | 258 → 216 |
| `tools32` | 243.4 → 271.2 (1.11×) | 3.58 → 3.25 | 2.98 → 2.86 | 10.44 → 8.41 | 15356 → 11853 | 261 → 217 |
| `burst4` | 1135.0 → 1383.6 (1.22×) | 1.85 → 1.49 | 2.67 → 2.51 | 7.00 → 4.22 | 3532 → 2948 | 275 → 191 |
| `stream256` | 22.2 → 22.6 (1.02×) | 42.41 → 40.60 | 34.41 → 34.05 | 69.72 → 60.68 | 185451 → 176818 | 249 → 220 |
| `image2` | 29.9 → 30.2 (1.01×) | 35.87 → 33.72 | 25.62 → 24.95 | 50.89 → 51.27 | 8603 → 7311 | 276 → 229 |
| `chat-anthropic` | 722.2 → 791.9 (1.10×) | 1.24 → 1.11 | 1.10 → 0.99 | 3.33 → 2.66 | 2945 → 2459 | 195 → 220 |
| `stream256-cross` | 20.7 → 21.0 (1.01×) | 45.56 → 43.68 | 37.27 → 37.12 | 71.44 → 64.03 | 163029 → 156714 | 230 → 217 |

Gateway peak resident memory, sampled with `ps` every 50 ms (one round; the harness
records 0 on macOS because its RSS reader supports only Linux and Windows):

| Case | dev.173 MiB | `fabb0bf8` MiB | Ratio |
|---|---:|---:|---:|
| `chat-short` | 516 | 346 | 0.67 |
| `burst4` | 559 | 328 | 0.59 |
| `history256k` | 578 | 367 | 0.63 |
| `tools32` | 548 | 328 | 0.60 |
| `stream256` | 543 | 378 | 0.70 |
| `image2` | 637 | 431 | 0.68 |

Full non-race suite at `fabb0bf8`: **236.7 s** (server package 230.4 s), `go vet` clean.
The bounded tiers and shards that bring it there are recorded in
`.agents/notes/implemented/testing/2026-11-16-bounded-test-tiers.*.md` and
`.agents/notes/implemented/performance/2026-11-16-memory-limit-and-test-shards.*.md`.

**Verdict:** `fabb0bf8` is a real but small improvement: 1.01×–1.22× throughput, 30–41%
lower peak RSS. It is nowhere near 4×. For scale, the 1.7.0 release ran the same
scenarios at 1.09×–1.91× of dev.173 throughput with 65–94 MiB peak RSS (from
`v1.7.0-dev173.json`), so dev.173's design lost ground that incremental tuning has not
recovered.

### 1.3 Where the time goes at `fabb0bf8`

CPU profiles (`--mode=profile`, 15 s) split three ways:

| Case | Scheduler wake-ups (`runtime.semawakeup`) | Request handling (`serveProtocolRequest`) | Socket writes (`syscall`) |
|---|---:|---:|---:|
| `chat-short` | 65.5% | 5.5% | 13.3% |
| `chat-anthropic` | 58.6% | 8.0% | 13.5% |
| `tools32` | 49.8% | 28.0% | 5.9% |
| `history256k` | 13.3% | 51.6% | 3.9% |
| `image2` | 7.1% | 54.8% | 2.7% |
| `stream256-cross` (syscall also `stream256`) | 1.3% | 84.6% | 79.6% / 83.5% |

Three separate problems:

1. **Small requests: goroutine hand-offs, not conversion.** About 60% of CPU is
   `wakep → startm → semawakeup → pthread_cond_signal`, driven by `ready` (36%) and
   `newproc` (28%). The profile does not attribute the wake-ups to call sites; the per-request
   goroutines and timers to audit are the Hertz connection, the `net/http` client
   `persistConn` read/write loops, deadline timers and the usage writer. Conversion is
   about 5% of CPU here. No conversion rewrite alone can reach 4× on these cases.
2. **Large requests: the interpreter and repeated passes.** In `history256k`, the output
   plan interpreter (`compiledOutputPlan.build`) is 33% of samples; `EncodeRequestValue`
   18%, `ValidateRequestOutput` 10%, `preserveMappedNative` 10%, `CompiledConversion.Request`
   8%. In `image2`, scanning the same bytes repeatedly dominates: `IndexByteString` 17%,
   sonic `skip_one` 24%, `CountString` 11%, `utf8.ValidString` 5%. Allocation is 31× the
   request body for `history256k` (7.9 MiB per 256 KiB request) and 8.3× for `image2`.
3. **Streams: one flush per frame.** 80–84% of stream CPU is the socket write in
   `responseStream.Flush` (`server/gateway_stream.go:169`), unchanged since dev.173.

Memory: the live heap after requests drain is about 44–53 MiB, of which 44.5 MiB is the
compiled protocol packages (`CompilePreset` → expression trees, output plans, parsed
`Value`s, JSON schemas). The four shipped `mapping.json` files are 5.85 MB of source
(1.7.0's four definitions were 0.49 MB). Peak RSS climbs from ~75 MiB to ~330–360 MiB
during the first two seconds of load and never returns, so peak RSS is set by allocation
volume per request, not by live data.

---

## 2. Strategy A — Register bytecode VM replacing the plan tree (recommended)

**Opinion:** this is the strategy to do. The output plan and expression trees are a
general-purpose tree interpreter wrapped in plan nodes, operand arrays, meters and
re-validation. Replace them with a flat, typed bytecode executed by one tight loop. Keep
the DSL and the Spec/Mapping format; change only what the compiler
emits and what executes it.

### Design

- **Compile** each Mapping program (`decode_*`, `encode_*`) once per activation into a
  `[]Instr` with a constant pool and a fixed register file. Instructions are fixed-width
  structs (`op uint8, a, b, c uint16, k uint32`), not interfaces or closures.
- **Registers hold borrowed `Value` spans** of the input bytes, not copies. A `read`
  becomes a pointer lookup cached by path offset; a `literal` becomes a constant-pool
  load; `object`/`array` emit straight into one output buffer.
- **Fuse the passes into the program.** Resource metering (`Nodes`, `BufferBytes`),
  the step limit and output validation are counters and checks the compiler inserts at
  emission points, not separate walks. `ValidateRequestOutput` is replaced by emitting
  only schema-valid shapes and checking the remaining runtime-dependent constraints
  inline.
- **Decode directly into the target encoder where possible.** For same-protocol and
  native-preserving paths, the program copies unchanged byte spans from input to output
  (a `copy span` instruction), which removes the second encode in `preserveMappedNative`
  as a by-product: there is no reconstructed baseline to compare against.
- **Read sets are known at compile time.** Each program records exactly which Core paths
  it reads. The runbook's §4.4 "read-set analysis" becomes a property of the bytecode
  instead of a separate proof.
- **Errors keep exact paths and order.** Every instruction that can fail carries the
  source path index; first-error ordering follows instruction order, which follows the
  current evaluation order.
- **Keep the tree interpreter as an oracle** behind a build tag until the corpus and a
  differential sweep pass, then delete it.

### Expected effect

Not measured. The basis for choosing it: on `history256k` the output-plan interpreter alone is
33% of samples, with the encode, re-validation, native-preservation and conversion entry
points at 18%, 10%, 10% and 8% cumulative (these overlap with the interpreter share); on
`image2` the byte rescans are about 57% of flat samples. A single forward pass over the input with span copies removes most of
that. On small requests it does not help much (see §1.3), which is why it is paired with
Strategy C.

Memory: the 44.5 MiB of compiled packages become compact instruction arrays and constant
pools. Per-request allocation should drop to roughly the output buffer plus a register
file, which is what lowers peak RSS.

### Steps

1. Prototype the VM for one program: openai-chat `encode_request`. Measure
   `BenchmarkRequestMemoryPipeline/openai-chat` and `history256k` against the interpreter.
   **Stop gate:** below 3× on `history256k` CPU, stop and report before continuing.
2. Cover all ops used by the four shipped packages; run the corpus differentially against
   the interpreter.
3. Move metering, step limits and output validation into the instruction stream.
4. Add span-copy instructions and delete `preserveMappedNative`'s second encode.
5. Switch default execution; keep the interpreter behind a tag for one release.

### Risks

A compiler defect is a silent semantic difference. A differential run against the current
interpreter over every mapping sample, all cross-protocol pairs, streaming, error and
limit-boundary cases is the only protection, so the oracle stays
until both pass. Custom user packages must compile to the same bytecode with no Go
toolchain at runtime, which this design satisfies (no Go code generation).

---

## 3. Strategy B — Byte-level transcoder, no semantic document

**Opinion:** the most radical option and the only one that plausibly reaches the ceiling
on large payloads; also the riskiest. Do it only for paths where Strategy A's prototype
shows the semantic document itself, not the interpreter, is the remaining cost.

### Design

- Drop the intermediate Core2 document on the hot path. Each Mapping compiles to a
  **streaming transcoder**: a state machine that tokenizes the source JSON once and writes
  the target JSON directly, copying unchanged spans (message text, base64 image data,
  tool argument blobs) without decoding or re-encoding them.
- Core2 stays as the **specification** and the corpus oracle: the transcoder for A→B must
  produce the same bytes as A → Core2 → B. The semantic document is still built on cold
  paths (verification, Agent, management) where speed does not matter.
- Streaming frames use the same transcoder per frame, with per-stream state for
  associations and tool-call indexes.

### Expected effect

Not measured. The case for it: `image2` allocates 8.3× its body and spends most of its
CPU rescanning bytes; `history256k` allocates 31×. A span-copying transcoder touches each
input byte about once and allocates about one output buffer. That is the shape that
reaches ≥ 2× lower memory and ≥ 4× throughput on large bodies.

### Risks

Highest. Absent/null/zero, ordering, tool associations and source-owned extensions are
currently guaranteed by the shared document. A transcoder must reproduce every one of
them per protocol pair. Cross-protocol pairs are N×(N−1); with four builtins that is 12
transcoders, though they are generated from the Mappings rather than hand-written.

---

## 4. Strategy C — Gateway concurrency and stream write path

**Opinion:** required regardless of A or B, because on small requests the cost is not in
conversion. Without C, no conversion rewrite reaches 4× on `chat-short`, `burst4` or
`chat-anthropic`.

### Design

- **Collapse goroutine hand-offs per request.** Profile shows ~60% of small-request CPU in
  scheduler wake-ups. Audit each per-request goroutine and timer: the outbound
  `net/http` client's `persistConn` read/write loops, `context.WithDeadline` timers
  created per request (`relay.dynamicTimeoutClient.Do` → `setRequestCancel`), usage
  batch writer wake-ups, and Hertz connection handling. Replace per-request timers with
  one deadline wheel, run the upstream exchange on the request goroutine where the
  transport allows it, and batch usage persistence by count and time instead of waking per
  request.
- **Coalesce stream flushes.** Flush once per batch of frames already available from
  upstream and always before blocking on the next upstream read, never on a timer, so no
  frame is delayed. `CommitDelivery` moves with the flush so it never reports unflushed
  bytes. This targets the 80–84% syscall share on streams.
- **Usage persistence off the hot path.** SQLite `SaveUsageBatch` is 2.4% of
  `chat-short`; batch it so it runs once per N requests.

### Expected effect

Not measured. The scheduler share alone (49–66% of small-request CPU) bounds what C can
recover on those cases; stream gains depend on how many frames arrive per upstream read.

### Risks

Low to moderate. Stream batching changes write timing (not content); late-failure and
idle-timeout contracts must be retested. Removing goroutines must not break cancellation
or browser-disconnect semantics.

---

## 5. Recommendation and order

1. **C first** (smallest, independent, unlocks small-request gains), measured alone.
2. **A** with the openai-chat `encode_request` prototype and its 3× stop gate.
3. **B** only for the large-body paths, and only if A's prototype shows the semantic
   document is the remaining cost.

Gates for each step: output byte-identical to the interpreter in the differential run; full non-race suite green and under
five minutes; per-scenario numbers against dev.173 reported individually; "not measured"
for anything not measured. Never weaken assertions, enlarge limits, or delete samples.

## 6. Test runtime

The full non-race suite already meets the five-minute bound at `fabb0bf8` (236.7 s; server
230.4 s), from the bounded tiers and server shards recorded in the two Agent Notes listed
in §1.2. A further reduction belongs with Strategy A: the server package spends most of
its time compiling and verifying the shipped packages in fixtures, and a compact bytecode
compile cache shared across fixtures directly shortens that.
