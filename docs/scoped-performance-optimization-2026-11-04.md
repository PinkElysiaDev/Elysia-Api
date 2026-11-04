# Scoped allocation optimization, 2026-11-04

Baseline Q0 is `104e4858`, production `8a019137`, frozen as
`104e4858-source-68f96a100690`. Its archive and four executables are under
`.tmp-dev/allocation-optimization-2026-11-04/scoped-q0`.
Official/rebuilt 1.7.0 is `9b72829a`; A is `b2844846`, not 1.7.0.
This report records work in progress; it is not final performance acceptance.

## Isolated parallel verification

`VerificationRun.executeTasks` reuses the existing process execution, timeout
and process-tree cleanup. Each task has its own report/log directory. A single
parent writes the aggregate report after completion. Tasks use at most four
slots; heavy builds/race use two. Matching groups exclude each other, preceding
dependencies block failed descendants, and exclusive tasks form measurement
barriers. Timing/profiling tasks never overlap another managed task.

`node scripts/verification/batch.mjs manifest.json` accepts `root`, `directory`,
optional `slots` (default four) and `tasks`. Each task has `name`, `command`,
`args`, optional execution `options`, `slots`, `group`, `after` and `exclusive`.
Paths and environment belong to each child; Python workers do not share mutable
`EVIDENCE`. Manifests and child reports retain commands and source identities.
Evidence directories are fresh per run; previous measurements are not overwritten.

The runner self-tests pass, covering failures, missing commands, timeout cleanup,
weighted overlap, group exclusion, exclusive ordering and failed dependencies.
Synthetic failure cases intentionally exit unsuccessfully. Production runtime
and performance settings are unchanged. No speedup is inferred from these tests.

## Diagnostic fixtures

The scoped consumer fixture isolates merge read/project/constructed producers,
projection consumers, raw/constructed length and join (0/1/8/32/256 elements),
same-input scopes and filter item reads. Existing field reads now cover
1/4/8/9/32/64 keys. One-iteration execution verifies the fixtures compile and
produce output; those timings are not performance evidence.

The attribution overlay adds per-active-reader distinct/repeated storage reads,
operand operation/arity, ready/sort/fold/raw field categories and Value.Bytes
copies. Identity bookkeeping uses numeric addresses only in the diagnostic
build, never retains original text, and resets with the owning reader. These
counts describe live-scope storage, not content equivalence or cache permission.
The Q0 overlay builds successfully. Diagnostic instrumentation is absent from
all standalone and exact-counter acceptance binaries.

## Rejected: explicit field borrowing

The proposed duplicate-copy explanation is disproved. `&valueReader{}` already
borrows every fragment because its sharing threshold is zero. Setting
`isBorrowed: true` changes no ownership or allocation behavior. Ten alternating
20,000-iteration rounds (`scoped-fields-micro`) show identical bytes/objects on
all field/read shapes and no definite field-read timing gain. Three unrelated
scalar shapes show timing regressions (4.4–7.1%); no benefit warrants further
HTTP measurement. The production change is removed, not relabelled as a win.
The independent small-result ownership regression remains.

Protocol/builtin suites and targeted protocol race pass. The first parallel
batch failed because a test still called the renamed private method; the fixed
batch passes, and both batches remain archived. The builtin result is reused
because its production source was unchanged by that test-only correction.

Q0 diagnostic SSE counts are 58,630 scoped reads, 52,904 repeated storage reads,
34,933 root hits and 17,971 child hits per request. Existing caching therefore
already serves those repeats; table construction counts are not all redundancy.
Merge runs 795 times/request (1,592 total operands); field consumers observe
783 fold, 1,318 raw, 1,071 ready and 2 sort cases. These are call counts, not
allocation bytes. Raw diagnostics are in `scoped-q0-attribution`.

## Active input scopes

The combined filter/input candidate (`scoped-scopes`) is rejected: the filter
fixture becomes 8.53% slower with 33.33% more bytes and 35.71% more objects.
Two small span lookups are cheaper than creating its proposed item table.
The filter production path is restored; its eager-evaluation oracle remains.

The isolated `with_input` candidate (`scoped-input`, source `10874d1b2f46`)
reuses an enclosing root or already cached descendant table. Only newly created
readers are released. Ten alternating 30,000-iteration rounds save 64 B and one
object per same-input invocation (2.51% bytes, 7.14% objects); time is inconclusive.
All measured scalar/filter controls have no stable regression. Protocol tests
and targeted race pass (`scoped-input-checks`).

Five standalone HTTP pairs each for short/tools/stream/cross-stream pass without
stable CPU, throughput, latency or memory-boundary regression. CPU ratios are
1.0000/1.0252/0.9976/0.9855; all are inconclusive. No HTTP speedup is claimed.
One admin-verify connection timed out before measurement, with both child
processes exiting zero. Its failed evidence remains in `scoped-input-http`.
`scoped-input-http-resumed/runs.json` references the 16 already completed tasks,
the remaining fresh runs and the failed preparation separately. No completed
timing is overwritten or selectively replaced. The preparation timeout's cause
is unresolved; loopback proxy bypass was enabled, and no proxy setting changed.
The comparison consumes all five observations per condition, normalized to the
same completed request counts. Retention and aggregate allocations are checked
again at final acceptance; this stage's allocation saving is microbenchmark-only.

## Interrupted batches

`allocation-resume.py` creates a fresh output directory and a manifest linking
completed runs instead of copying or overwriting evidence. Reuse requires matching
stage/scenario/phase/execution, binary/driver/measurement-runner hashes and runtime
environment. A failure after measurement requires investigation rather than an
automatic replacement. Original failures are listed separately. The comparator
requires a completed manifest and retains the normal minimum sample counts.
Identity self-tests reject changes to each identity component and GC settings;
the standalone and counter identities remain distinct. The resume mechanism only
schedules existing measurements; it does not add gateway instrumentation.

Offline flame rendering now uses four workers with independent pprof processes,
ports and files. The parent publishes the manifest only after all workers finish.
Two self-tests verify concurrent output isolation, deterministic manifest ordering
and propagation of rendering failures. This parallelism runs after profiling,
never alongside a timed or sampled gateway.

## Transport ownership counterexample

The unconditional Value/string transport switch is rejected before performance
measurement. A compiled package with a valid emit effect can select
`/parameters/container/wire`: its ~256 KiB outgoing JSON shares storage with a
~768 KiB parameter. `EncodeRequestValue` and the immutable Value contract permit
this large view. Holding a strings.Reader until upload completes would retain
~512 KiB of unrelated input that the existing `Value.Bytes()` copy detaches.
`TestEncodedRequestValueMayShareLargeParameterStorage` reproduces this through
the public package encode boundary and verifies byte-copy independence.

The generic gateway and counting entrypoints cannot infer standalone backing
storage from Value alone. No unused transport API is added, no provider-specific
qualification is introduced, and no new ownership metadata or broader compiler
proof is smuggled into this candidate. A later statically qualified transport
experiment is possible, but its scope and benefits remain unproven. This rejects
the blanket switch, not every potential future transport optimization.
