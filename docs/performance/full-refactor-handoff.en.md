# Handoff: full conversion refactor

Status: design, not implemented. Written 2026-11-16. Nothing in this document has been built.

## Problem

The gateway converts protocol A → Core2 → protocol B by executing a compiled output
plan over a semantic document. Round 2 of performance work (branch `perf/round-2`)
reached **1.06x geomean throughput over dev.173** on the same machine, with the same
harness and the same runtime settings. Peak heap fell 15–37% and allocations fell up
to 38%, but latency barely moved. The 4x–13x goal was not reached, and allocation
tuning is close to exhausted: `stream256` still allocates 177k items per request and
improved only 4.7%.

This document specifies the refactor required to reach that goal. It is the largest
change in the project's history and it should not be started without the golden corpus
described below existing first.

## Why the obvious target is the wrong one

A profile attributed ~34% cumulative time to `outputPlan.build`. That figure is
cumulative, not self. Measured under `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`:

| function | flat | cumulative |
| --- | ---: | ---: |
| `compiledOutputPlan.build` | 0.13% | 38.5% |
| `compiledOutputPlan.buildNode` | 2.74% | 38.4% |
| `compiledOutputPlan.buildOperation` | 1.90% | 38.1% |
| `evaluation.advance` | 1.93% | 3.87% |

The dispatch itself is ~4.6% self-time and the string switch inside it is ~1.5ns of
that. Go 1.27 compiles the ~100-case string switch into a length-bucketed indirect jump
table plus a first-byte binary search, not a comparison chain. Per-node cost is 5.8ns
(`literal`) to 8.3ns (`read`) including prologue, `advance`, and error plumbing. Call
volume is the part that scales — ~1,400 calls per short request, ~40,000 for a 64-message
history — but even at 40,000 calls the entire node boundary is ~232µs of a 5.8ms request.

Replacing the interpreter dispatch with closures or integer opcodes was measured as
recoverable **1–2%**, not more. It is rejected on that arithmetic. An independent
investigation confirmed `buildOperation` never mutates the plan or expression at runtime
and that plans are shared across concurrent requests, so the design is already safe for
future work — it is simply not where the time goes.

**The time goes into re-walking the document.** A single non-streaming request performs
12–15 whole-document traversals plus 2–3 encodes. That is the target.

## What the document costs

Struct sizes measured with `unsafe.Sizeof` on darwin/arm64:

| type | bytes |
| --- | ---: |
| `Node` | 296 |
| `Request` | 264 |
| `Event` | 408 |
| `Response` | 216 |
| `Tool` | 120 |
| `Provenance` | 160 |
| `Native` | 192 |
| `Value` | 16 |

A 64-message conversation spends ~39.7 KB on struct footprint before payload bytes.
`Node` dominates: role, ID, call ID, name, status, payload `Value`, `*ToolInput`, three
slice headers, a map, two pointers, and three unexported fields. `Value` is deliberately
a 16-byte handle to immutable raw JSON, which is a good design and should be kept.

## The shared root cause

Three separate optimizations are blocked by one missing mechanism: **there is no
mutation epoch on `Request` or `Event`**. Writes are scattered across
`conversion_request_fields.go`, `core_control_*.go`, `package_model_controls.go`, and
`conversion_apply.go`, with no single funnel. Consequently nothing can prove "this
document has not changed since the last check" except a full structural comparison, and
so every consumer does one:

- `equalCoreSnapshot` is reached **109 times per request** as a root invocation.
  `requestContextUnchanged` is called 9 times (1 decode, 7 inside `conversion.Request` —
  one per applied rule, 1 encode). `requestCheckUnchanged` twice.
- `digestSemanticEvents` marshals the entire event batch **twice per stream frame**,
  because the decode-side digest covers a different slice than the encode-side digest
  and nothing can prove the two are equal without recomputing.

Measured cost: `equalCoreSnapshot` 4.40% cumulative, `digestSemanticEvents` unmeasured
per-frame but two full marshals.

A monotonic generation counter on the document, bumped at every write site, makes both
comparisons O(1) in the common case. This is why it belongs in the new representation
rather than being bolted on later.

## Step 1 — replace the document layer

Constraint: shipped `spec.json`/`mapping.json` packages and custom user packages are
untouched. Schema 4 / DSL 2 stay exactly as they are. This is an internal Go API change
only.

Design goals, in priority order:

1. **Shrink `Node`.** It is the dominant allocation and the unit of recursion. Target
   under 128 bytes by moving rarely-set fields to a side table indexed from the node,
   and by replacing the three slice headers with a single contiguous child range in a
   shared arena.
2. **Contiguous storage.** Nodes in a per-document arena with parent/child ranges
   instead of nested slices, so a walk is a linear scan and children are cache-adjacent.
   This is the change that makes 12 walks affordable even before any are removed.
3. **Keep `Value` unchanged.** The immutable-raw-JSON handle is correct, it preserves
   absent-versus-null-versus-zero, and ~343 test field reads plus the `spanIdentity`
   caches depend on it. Do not touch it.
4. **Add a mutation generation counter.** One `uint32` per document, incremented by a
   single `touch()` called from every mutating helper. Every existing write site must
   be converted, and a test must assert that no exported field assignment exists
   outside a `touch()` site.
5. **Keep the load-bearing unexported receipts.** `verdict`, `continuationInput`,
   `rangeOrigin`, `rangeAssociation`, `auxiliary`, `controlReceipt`,
   `requestWorkspace` are security and correctness state, not incidental. Several
   tests assert *pointer identity* on them (`hotpath_verdict_test.go:145,161,192`,
   `package_cache_native_test.go:26-27,187`). They move into the arena with the rest;
   they do not disappear.

Regeneration is automatic: `internal/coreplan` and `internal/semanticmeter` reflect over
these structs at build time and write five generated files
(`plan_core_generated.go`, `semantic_clone_generated.go`,
`semantic_normalize_generated.go`, `typed_meter_generated.go`, plus
`definition_encode_generated.go` which is unrelated to the document). Run
`cd backend && go generate ./protocol`. Their oracle tests compare generated output
against hand-written reflective implementations and will fail loudly if a
representation change breaks a walker — that is the intended safety net.
`typed_limits_test.go:238` already enforces meter freshness in CI.

Scope cost, stated plainly: `protocol.Request`/`Node`/`Value` have **~380 non-test call
sites outside `protocol/`** — 338 in `server`, 36 in `agent`. `agent` constructs
`[]protocol.Node` literals directly (`agent/history.go:31,42,67,143`,
`agent/context.go:131`). Approximately 240 `reflect.` uses and 343 direct field reads
exist in tests. This is internal code we own, but it is not a small diff.

## Step 4 — cut redundant passes

Ordered by measured value and by whether they are actually sound. Evidence comes from
instrumented runs of `BenchmarkRequestMemoryPipeline` with temporary probes; every probe
was removed afterward.

### Not removable — do not attempt

**Second encode inside `preserveMappedNative` (~17% of same-protocol encode).**
`canReuseOutput` (`request_workspace.go:45`) is designed to skip the baseline re-encode
by proving the current request equals the decoded baseline. It fails for all four
builtins, and empirically not on the plan flags (those are false for every
`encode_request` plan) but on `equalRequestSnapshot`: conversion legitimately sets
`ClientOutput.CollectUsage` and `ResponsesStorage`, and for gemini also `Parameters`.
The shipped openai-chat program reads `/clientOutput/collectUsage`,
`/clientOutput/rawStreamOptions`, and `/clientOutput/responsesStorage` inside
conditionals, so the differing fields are semantically live. Outputs happen to match on
the benchmark fixture because it is non-streaming — that is data-dependent luck, not an
invariant. Removing the second encode requires a compile-time read-set analysis over
`compiledOutputPlan` proving the plan cannot observe any differing field, including
root-enumerating ops (`keys`, `type`, `entries`, `project`, `walk`, `visit`, empty-pointer
`read`). For openai-chat that analysis still fails. **The gate is doing real work.**

Note the measurement gap this exposes: `scripts/benchmark-performance.mjs` clears
`Native`, so this 17% is invisible to the end-to-end harness while being real for
same-protocol traffic. Fixing the harness is separate work and should be done before
trusting any end-to-end number for same-protocol cases.

**Wire re-validation at the final boundary.**
`ValidateRequestOutput` validates under `upstream-request`, not `generation-request`.
Different validator, so not redundant despite operating on identical bytes.
`plan_encode.go:109-112`'s `mapping.output.validate` is nil for package directions — a
no-op, not a pass. The only candidate is the `finishPackageExecution` validation, and
removing it changes error precedence (schema failure versus `InvalidMutation`) on a
function shared with the non-preserve path. Not provably safe; leave it.

### Removable only after the mutation epoch lands

**Verdict compares (109 root walks per request).** With a generation counter, each
`requestContextUnchanged`/`requestCheckUnchanged` becomes a counter comparison when the
document has not been touched since the witness was taken, and falls back to the full
`equalCoreSnapshot` walk only when it has. This preserves the existing security posture
exactly — the comment at `request_verdict.go:10-12` ("a redundant walk is preferable to
accepting a stale security-relevant pass") stays true, because a touched document still
gets the full walk.

**Per-frame digest (2 marshals per frame).** Once `convertEvent` can report "no exported
field mutated" via the same counter, and `len(acceptedEvents) == len(frame.Events)` in
order, the decode-side digest can substitute for the encode-side recompute.

### Removable now, small

**`canReuseOutput`'s warn-effect check** can be relaxed when `options.Diagnostics == nil`,
since warn multiplicity is the only observable it guards. Small win, low risk, needs a
test that the diagnostics path is genuinely unused in that configuration.

## Step 3 — generate the executor

**Not to be started until step 1 and the mutation epoch have landed and been measured.**
Generating straight-line Go over the current pointer-heavy document would emit code that
still chases pointers across a dozen walks. The gains compound only in this order.

What it means concretely: instead of the runtime interpreting a `compiledOutputPlan`
tree, generate one Go function per compiled Mapping from the `encode_request` /
`encode_response` / `encode_event` programs, cached alongside the existing compiled plan
and keyed on the same revision identity. The generated function must reproduce, exactly:
every error message and its path, first-error ordering, all resource-limit checks
(`Nodes`, `BufferBytes`, `walkNodes`), the `advance()` step limit, absent-versus-null
semantics, and `Value` raw-byte preservation.

The correctness burden is the whole risk. Every one of those behaviours is currently
produced by generic interpreter code in one place; generated code produces them per
program, which means a generator bug becomes a silent semantic difference rather than a
compile error. This is precisely why the golden corpus must exist first.

## Golden corpus — build this before anything else

There are currently **no `.golden` files** in the repository. The closest existing
material is `protocol/testdata/` (conversation fixtures), `protocol/builtin/testdata/`
(stream layouts, agent discovery contracts, archived fixtures), and the compiler fixture
tests which exercise full decode→normalize→encode. None of it is a byte-for-byte oracle.

Required: a corpus captured from the current tree, covering every builtin package in
every direction and every case class that matters — short and long documents, tools,
images, streaming frames, cross-protocol pairs, native-preserved and native-free, and
the error paths (each expected `ConversionIssue` code and path, not just success).
Capture must record full wire bytes, not parsed structures. Any later change to the
corpus is a behaviour change and must be reviewed as one.

## Migration sequence

Each commit green, no step depending on a later one:

1. Golden corpus captured from the current tree, passing against the current tree.
2. Mutation epoch on the existing `Request`/`Event`, with a test proving no exported
   field write exists outside a `touch()` site. Fix the end-to-end harness to stop
   clearing `Native`.
3. Verdict-compare and digest carry-forward optimizations, measured.
4. Document representation change, with the generators regenerated in lockstep.
5. Measure. Re-decide whether step 3 (generated executor) is still worth its risk.

Steps 1–3 are worth doing regardless of whether step 4 or 5 ever happen.

## Risk list

- **Generated-code semantic drift** (step 5): the dominant risk, mitigated only by the
  golden corpus and by `typed_limits_test.go`-style freshness enforcement.
- **The ~380 external call sites**: mechanical but large; a missed site that reads a
  field assuming the old layout may compile and be wrong. Mitigated by keeping field
  names stable where possible.
- **Oracle tests encode the old representation**: `semantic_normalize_parity_test.go`,
  `semantic_clone_oracle_test.go`, `typed_limits_test.go` compare against hand-written
  reflective oracles. They are the safety net for step 4 and a rewrite burden during it.
- **Pointer-identity tests**: `hotpath_verdict_test.go`, `package_cache_native_test.go`,
  `package_history_mapping_test.go` assert identity of receipt pointers. Arena
  allocation changes what those pointers mean.
- **The 4x–13x figure is not promised by this plan.** Steps 1–4 have no measured
  projection; only step 5's payoff could plausibly reach that range, and it has not been
  modelled.

## Not measured

- Any projection of throughput after steps 1, 3, 4, or 5. The only measured numbers in
  this document are the round-2 A/B result, the profile shares, the call counts, the
  struct sizes, and the invocation counts for the verdict compares.
- `digestSemanticEvents` cost per frame.
- Whether the corpus capture covers enough of the real traffic distribution.
- Production behaviour on non-Apple hardware, and on Windows where the handoff baseline
  was originally taken.

## Producing commands

Profile and call counts were taken with:

```sh
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend test ./protocol \
  -run '<benchmark>' -bench=. -count=1 -cpuprofile=.tmp-dev/cpu.out
go tool pprof -top -cum <test-binary> .tmp-dev/cpu.out
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend build \
  -gcflags=-m=1 ./protocol
```

The round-2 A/B used `scripts/benchmark-performance.mjs --execution=production
--mode=measure --repeats=5` on two worktrees at `bfe7638f` and the round-2 head,
interleaved, under `GOMAXPROCS=2 GOGC=100 GOMEMLIMIT=2GiB`.
