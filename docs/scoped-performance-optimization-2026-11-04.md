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

## Canonical projection facts

Projection already folds decoded duplicate keys and sorts fields. Its output now
retains those facts and the logical encoded length; subsequent field consumers
do not rebuild maps or sort again. No input/output, permissions or provenance
check is removed. Protocol suite and relevant race pass.

Ten alternating 30,000-iteration rounds (`scoped-projection-micro`) improve
project/field time 17.18%, bytes 19.64%, objects 23.08%; project/merge shapes
improve time 5.80–9.19%, bytes 6.43%, objects 13.64%. Control shapes have no
stable regressions. Five HTTP pairs for short/tools/stream/cross-stream initially
show a short-request p95 increase of 0.18%. The one allowed five-pair short
confirmation is merged with the original data, making that difference
inconclusive. No stable standalone regression remains; HTTP speed is not claimed.
Both original and confirmed comparisons remain in `scoped-projection-http`.

The separate five-round three-condition SSE counter batch
(`scoped-stage-counters`) finds scope reuse unchanged within uncertainty, and
projection allocation 15,222,112 → 15,171,416 B/request (ratio 0.99667,
95% interval 0.99647–0.99720), objects 205,912.7 → 205,132.7 (ratio 0.99621,
interval 0.99619–0.99624). These small whole-request gains are not presented as
the larger isolated microbenchmark improvement. Counter-build timings are not
substituted for standalone timing acceptance.

## Rejected merge capacity experiment and correction

The first linear pair-merge candidate (`scoped-merge`) passes semantic/race
checks and five HTTP pairs per short/tools/stream/cross-stream scene. However,
the additional ten-round overlap fixture disproves its allocation gate: reserving
both input lengths increases bytes 12.5%/33.3%/50% for 1/4/8 identical keys.
Rejected collisions allocate an output unnecessarily and slow down 17.8–33.4%.
Those failures are retained in `scoped-overlap-micro`; the first candidate is
not adopted despite its nonoverlap and HTTP gains.

The correction counts the sorted union and resolves reject collisions before
allocating the output. It then fills exactly that capacity; this is sizing work,
not a second input validation. Operand evaluation, error ordering and logical
budgets are unchanged. Ten rounds (`scoped-merge-union-micro`) cover raw,
projected, constructed, sparse/dense Core and 0/1/4/8/32 overlapping fields with
all policies. No shape has a stable time/bytes/object regression. For 1/4/8-key
replacement merges, bytes fall 12.5%/22.2%/25%; small collision rejects retain
the original allocation count and improve time. Corrected HTTP acceptance is
still in progress and is not inferred from the first candidate's measurements.

One attempted full check in the backend-only source archive fails because
published documentation examples are outside that archive. It is an incomplete
test environment, not a passing suite. The corrected workspace protocol suite
and targeted race both pass (`scoped-merge-union-checks`). Its frozen production
source is the input for all corrected measurements.

Corrected merge is also rejected. Its five-pair HTTP batch has no stable timing
regression, but the unforced one-second cross-stream heap boundary increases
39.19%. The single five-pair confirmation is combined with the first batch;
the ten-observation ratio remains 1.3877 with a regression interval. This is an
uncollected boundary heap, not proof of a leak or retained-body growth. It is
nevertheless a stable measured memory regression under the agreed gate, so the
production merge change is restored. No extra repetitions are used to search
for a passing result. The old-map oracle, overlap benchmarks, failed candidate
archives and both comparisons remain available. Further GC/retention attribution
is diagnostic only and cannot turn these measurements into acceptance.

## Immediate array consumers

Length reads constructed array sizes directly; validated raw arrays are counted
with existing span scanning. Source-array join consumes members locally without
the Value/JSONOutput/plannedOutput adapter slices. The result remains owned;
expression-list join still evaluates all operands before string/type failures.
The previous array consumption is retained as a test oracle for bytes, errors,
logical steps and budgets, including Unicode, null, nested objects and cancellation.
Protocol suite and targeted race pass (`scoped-array-checks`).

Ten alternating 20,000-iteration rounds (`scoped-array-micro`) improve raw
256-element length time 61.5%, bytes 97.7%; raw join time 43.3%, bytes 68.7%.
Constructed joins improve too. A 32-element constructed length control initially
regresses 1.82%; one same-size confirmation is merged, giving ratio 1.01537,
95% interval [1.00000, 1.02899], inconclusive. No combined stable regression
remains. All original observations are retained.

Five HTTP pairs for short/tools/stream/cross-stream have no stable time or memory
regression (`scoped-array-http`). Tools throughput improves 0.48%; other timing
changes are inconclusive. Five separate SSE counter pairs find bytes/objects
unchanged within uncertainty (`scoped-array-counters`). No whole-SSE allocation
improvement is inferred from the isolated array benefits.
