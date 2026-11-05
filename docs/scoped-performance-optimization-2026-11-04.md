# Scoped allocation optimization, 2026-11-04

Baseline Q0 is `104e4858`, production `8a019137`, frozen as
`104e4858-source-68f96a100690`. Its archive and four executables are under
`.tmp-dev/allocation-optimization-2026-11-04/scoped-q0`.
Official/rebuilt 1.7.0 is `9b72829a`; A is `b2844846`, not 1.7.0.
Implementation and Windows/local-provider acceptance are complete. Three small
runtime changes pass the parent and final-Q0 gates. Other candidates are rejected.
Whole-request allocations improve slightly; HTTP CPU/throughput improvement is
not established. The historical CPU gap remains and still blocks preset switching.
Measurements started November 4 and finished November 5, 2026 (Asia/Shanghai).

Final evidence: [interactive figure index](../.tmp-dev/allocation-optimization-2026-11-04/scoped-report/index.html),
[all absolute medians](../.tmp-dev/allocation-optimization-2026-11-04/scoped-final-absolute-tables.md),
[ratios and 95% intervals](../.tmp-dev/allocation-optimization-2026-11-04/scoped-final-comparisons.json),
[source and execution identities](../.tmp-dev/allocation-optimization-2026-11-04/scoped-report/identity-manifest.json).

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
the original allocation count and improve time. Corrected HTTP acceptance uses
a separate batch; its rejection is recorded below, independently of the first
candidate's measurements.

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

## Frozen correctness and scheduling evidence

The accepted production changes are `23bc1d5c` (input scope), `e2fadec9`
(projection facts) and `cdff65b7` (array consumers). The final backend archive is
`654b9161-source-399f330441ce`; later source-inventory changes do not alter its
production code. DSL, Spec, Mapping, public APIs and preset loading remain unchanged.

The final bounded verification batch runs all Go packages once, divided into
server, protocol, builtin and remaining packages. All pass, as do vet,
staticcheck v0.8.2, related protocol/builtin/server race, Core and semantic-meter
generation, audit self-tests, scheduler tests and resume/rendering self-tests.
The server suite includes `TestDefaultPackagesGatewayMatrix` and Native,
reference and delivery regressions. Two inventory-related checks initially fail
because the source list lacks five new files. Commit `fe505d6f` regenerates that
list; both checks pass in `scoped-final-inventory-fixed`. The original failed
aggregate is retained, not reported as an uninterrupted successful run.

The main batch takes 358.606 seconds wall time; its individual command durations
sum to 521.389 seconds. This is scheduling evidence, not a separately measured
serial-to-parallel speedup. The largest dependency is server test (176.864 s)
followed by server race (180.486 s), which deliberately do not overlap. Ordinary
tasks use one slot, race/build tasks two, and the total stays within four slots.
Waiting and execution durations, commands, identities and separate logs remain
in each child report. The corrected inventory batch takes another 0.807 s.

Formal timing, exact counters, sampled profiles and forced-GC retention run in
separate exclusive windows after the check queue drains. Frozen binaries and the
same upstream/client fixtures are reused. The final matrix uses all seven
standalone conditions and all six matching counter conditions; the official
release asset is excluded from exact allocation counts.

The primary matrix completes 280 standalone runs in 2,569.235 s and 240 exact
counter runs in 2,352.653 s, without failed requests or identity checks. Neither
default nor package final-vs-Q0 comparisons has a stable regression. Standalone
CPU/throughput differences are inconclusive; the whole-request gains are small
allocation reductions, not evidence of a broad latency speedup.

The following Native/reference benchmark fails before measurement because the
micro runner starts builtin tests from the backend root instead of their package
directory. The runner now uses each package's working directory; an isolated
runner self-test covers all three packages. Original failed evidence stays in
`scoped-accepted-native-reference`; corrected measurements use a fresh label.

A diagnostic overlay build then fails with Windows `VirtualAlloc errno=1455`
(commit-memory exhaustion). Investigation finds 537 orphaned pprof viewers from
earlier rendering sessions: terminating `go tool` had left its viewer children
alive. After the primary matrices finish, only viewers referencing this workspace,
using the renderer's loopback flags, and lacking a live parent are stopped; their
PIDs and command lines are archived in `scoped-orphan-viewers.json`. Rendering now
terminates its own Windows process tree, with a regression test. The diagnostic
build also caps Go package build concurrency at two and succeeds in a new output
directory. No production binary or runtime GC setting changes.

The primary timing/counter window therefore predates viewer cleanup; subsequent
matched diagnostic batches follow it. All conditions within the primary window
share that background environment, but it limits generalization to an otherwise
idle machine. Post-cleanup profiles explain call paths, not a new percentage to
splice into the primary timing table. The failed build and initial benchmark
failure are retained explicitly rather than counted as passed checks.

## Final matched-workload results

The following values are independent medians from five rotating rounds per
condition, on Windows amd64, Go 1.27.2, GOMAXPROCS=2, GOGC=100, GOMEMLIMIT=2GiB.
Rebuilds disable CGO and PGO; the official asset retains its original build
settings. Client, gateway and deterministic upstream are separate processes.
Each batch consumes complete responses and verifies output and upstream calls.
CPU is gateway process time per completed request, not end-to-end latency.

| Scenario | Official 1.7.0 CPU ms | Rebuilt 1.7.0 CPU ms | A CPU ms | Final default CPU ms | Final package CPU ms |
|---|---:|---:|---:|---:|---:|
| Short | 0.690 | 0.675 | 0.694 | 0.679 | 1.343 |
| 256 KiB history | 8.423 | 8.057 | 8.301 | 7.324 | 12.634 |
| 32 tools | 2.518 | 2.243 | 2.365 | 2.396 | 4.501 |
| Four concurrent short | 0.530 | 0.530 | 0.523 | 0.496 | 1.095 |
| 256-frame SSE | 15.503 | 14.893 | 15.381 | 14.160 | 50.903 |
| 2 MiB media | 22.583 | 21.362 | 22.339 | 21.240 | 34.424 |
| Chat to Anthropic JSON | 0.671 | 0.732 | 0.671 | 0.671 | 1.068 |
| Chat to Anthropic SSE | 21.606 | 21.484 | 21.240 | 20.142 | 50.781 |

Every final-package CPU ratio against official/rebuilt 1.7.0 and A has an
interval above one. Against the official release, short is 1.945x
[1.837, 2.214], SSE 3.283x [3.219, 3.600], and media 1.524x [1.400, 1.867].
Against A the corresponding ratios are 1.934x [1.833, 2.121], 3.310x
[3.089, 4.390], and 1.541x [1.415, 1.868]. A remains dev.101, not 1.7.0.
The default chain and package chain are intentionally separate columns: the
package architecture has not become the production default in this round.

Exact counters below use matching test collectors, not sampled pprof estimates.
Each cell is **allocated B/request / objects/request**. The official release
does not expose these exact counters and has no fabricated entry.

| Scenario | Rebuilt 1.7.0 | A | Final package |
|---|---:|---:|---:|
| Short | 157,884 / 2,614.8 | 160,907 / 2,614.6 | 261,472 / 3,542.0 |
| History | 9,909,800 / 33,438.9 | 9,913,852 / 33,439.8 | 8,932,090 / 45,329.8 |
| Tools | 583,659 / 9,866.1 | 586,068 / 9,866.1 | 1,490,856 / 16,688.5 |
| Concurrent | 158,892 / 2,604.5 | 162,311 / 2,604.4 | 261,859 / 3,536.2 |
| SSE | 6,941,916 / 96,437.8 | 6,938,712 / 96,445.2 | 15,172,205 / 205,134.9 |
| Media | 18,464,586 / 4,903.8 | 18,451,652 / 4,901.1 | 15,905,092 / 6,886.0 |
| Cross JSON | 167,291 / 2,387.7 | 170,382 / 2,388.8 | 219,935 / 2,824.0 |
| Cross SSE | 9,531,016 / 118,869.0 | 9,600,561 / 119,131.0 | 14,991,924 / 177,159.5 |

History and media allocate fewer bytes than the historical implementations, but
more objects and CPU. The other six scenes allocate more bytes too. For example,
SSE uses 2.186x the rebuilt 1.7.0 bytes [2.185, 2.194] and 2.127x objects
[2.127, 2.127]. This is a remaining gap, not closed by the current changes.

Final package versus this round's Q0, with all original observations retained:

| Scenario | Allocated B/request, Q0 to final | Change [95% interval] | Assessment |
|---|---:|---:|---|
| Short | 263,552 to 261,472 | -0.789% [-0.927%, +0.081%] | Inconclusive |
| History | 8,936,936 to 8,932,090 | -0.054% [-0.090%, -0.011%] | Improvement |
| Tools | 1,494,705 to 1,490,856 | -0.258% [-0.442%, -0.250%] | Improvement |
| Concurrent | 261,855 to 261,859 | +0.002% [-0.985%, +0.114%] | Inconclusive |
| SSE | 15,222,102 to 15,172,205 | -0.328% [-0.357%, -0.301%] | Improvement |
| Media | 15,903,603 to 15,905,092 | +0.009% [-0.133%, +0.172%] | Inconclusive |
| Cross JSON | 220,007 to 219,935 | -0.033% [-5.909%, +2.669%] | Inconclusive |
| Cross SSE | 15,028,571 to 14,991,924 | -0.244% [-0.265%, -0.199%] | Improvement |

SSE objects decrease 205,913.7 to 205,134.9/request (-0.378%); cross-SSE
177,691.1 to 177,159.5 (-0.299%). Each SSE response has 256 content chunks
and 260 observed wire events, including lifecycle events. Per content chunk,
SSE allocations are 59,461.3 to 59,266.4 B; per observed wire event they are
58,546.5 to 58,354.6 B. These normalizations include fixed request costs; they
are not an isolated incremental frame benchmark.

All final-vs-Q0 standalone CPU, throughput and latency differences remain
inconclusive. Both chains have no stable regression in the measured metrics;
counter timings are not substituted for standalone acceptance. The full linked
tables include all seven/six conditions, p50/p95, first frame, throughput, GC,
peak working set and one-second boundary heap. Bootstrap intervals estimate
ratios of independent medians, not a ratio averaged over matched pairs.

Ten corrected Native/reference micro rounds compare the same frozen binaries:
reference rewrite is 2.833 to 2.819 ms (-0.486%, ratio interval [0.99009,
0.99844]), 5,017,867.5 to 5,007,397 B, and 21,692 to 21,499 objects. Native
continuation bytes fall 248,700.5 to 248,146 (-0.223%), objects 3,149 to 3,137;
time remains inconclusive. No stable regression is found. These post-cleanup
microbenchmarks are separate from the primary HTTP window.

## Flamegraphs, call counts and remaining costs

All 150 interactive figures are indexed: 60 final HTTP, 72 independent
candidate/reference/Native micro, 6 held/cancelled, and 12 rejected-merge figures.
CPU, alloc_space, alloc_objects and inuse_space have separate views and diffs.
HTTP CPU/allocation profiles are normalized to 1,000 completed requests; retained
heap is process-wide. Micro profiles include setup and have fixed iteration
counts recorded in their manifest. A normalized CPU samples/duration percentage
is not CPU utilization. Sampling fluctuations must not replace exact counters.

Four isolated browser checks run concurrently and pass, covering all figure
files and render/hover/pivot for each sample/difference kind in each group. This
is not a claim that every call tree was inspected manually. No pprof viewers
remain after rendering, confirming the process-tree cleanup on actual tools.

The fresh attribution overlay counts each workload separately and is absent from
standalone/counter binaries. A missing package fixture in its baseline alias
causes a preparation failure before requests; identical package copies are
verified by hash, then both stages run in `scoped-final-attribution-fixed`.

| Per SSE request | Q0 | Final | Interpretation |
|---|---:|---:|---|
| Raw field tables | 7,828 | 7,828 | Construction count did not improve |
| Scoped reads | 58,630 | 58,630 | Same logical reads |
| Repeated storage reads | 52,904 | 52,904 | Already served by active scope caching |
| Root / child hits | 34,933 / 17,971 | 34,933 / 17,971 | Same existing reuse |
| Field consumers requiring folding | 783 | 3 | 780 redundant projection folds avoided |
| Ready field consumers | 1,071 | 1,851 | Canonical facts survive projection |
| Merge operations / operands | 795 / 1,592 | 795 / 1,592 | Rejected optimization is absent |
| Detached fragments / content bytes | 5,811 / 804,085 | 5,811 / 804,085 | Ownership copies preserved |

The same-input micro fixture saves one 64 B reader, but the representative HTTP
fixtures have unchanged scope/table counts. No whole-HTTP scope saving is claimed.
Independent projection flames remove the downstream `plannedObjectFields →
JSONObject → sortedKeys` subtree; array flames remove adapter/member slices.
Their isolated gains are much larger than their contribution to full requests.

Final sampled stack categories, **estimates per completed request**, remain:

| Scene / category | B/request | Objects/request | Representative call path |
|---|---:|---:|---|
| SSE field tables | 2,712,185 | 15,696.7 | buildReadValue → readOwnedMember → readFields → readObjectFieldsWithin |
| SSE operand evaluation | 1,843,362 | 32,177.0 | buildOperandValues → child expression plans |
| SSE owned fragments | 1,615,416 | 14,498.6 | valueReader.fragment → strings.Clone |
| SSE stream state | 1,221,378 | 13,013.6 | transaction/index/range holders |
| Short Core projections | 56,465 | 565.8 | generated readCore functions → finishCoreOutput |
| Media transmission copy | 2,127,254 | about 1 | forwardGateway → Value.Bytes |

For SSE field tables, the estimate is 10,594.5 B per content chunk or 10,431.5 B
per observed event. Sampling also attributes about 185,812 B/request to the
profiler itself; it is listed separately. Categories use a disjoint precedence
classifier, while cumulative flame subtrees overlap and must not be added.
The apparently smaller sampled table allocation versus Q0 is not evidence of a
table optimization: its exact construction counts are unchanged.

Media also retains about 4.66 MB/request in bounded request-body reading, 2.11 MB
in ParseValue, and three separate encoding/materialization stacks of about
2.06–2.13 MB each. The diagnostic wire is 2,097,458 bytes and finalization keeps
the same storage in both versions: there is no extra finalization copy to remove
in this fixture. Logs are disabled and have zero body bytes here; this does not
establish the cost or ownership of enabled logging. The universal transport
shortcut remains rejected because the valid borrowed-wire counterexample holds.

Post-GC short-process inuse sampling totals 57.93 MiB: compiled expressions
20.00 MiB and output plans 12.50 MiB are the largest allocation sites. This is
runtime/configuration residency, separate from per-request allocation. It does
not prove those graphs are redundant or that all compiler-allocated objects can
be removed. Remaining Core/operand/field costs need a new, narrower proven
consumer opportunity; this round does not reintroduce rejected compact tables,
capacity generators, enum dispatch or broad static selectors.

Rejected merge diagnostic sampling is consistent with reduced cumulative bytes
(about 15.01 to 14.86 MB/request excluding profiler categories), while its earlier
unforced boundary-heap regression remains. The additional profile does not prove
a leak, identify GC timing as the sole cause, or reverse its rejection.

Five held-stream batches keep four 2 MiB requests active with isolated master
keys. Q0 held heaps are 56.830–56.857 MiB; final 56.831–56.857 MiB. Cancelled
heaps are 48.579–48.591 and 48.583–48.594 MiB. Preparation reservations are zero
during delivery; cancellation clears active requests and queued payload bytes.
No accumulating state or extra retained large body is observed. These correlated
within-process batches support lifecycle checks, not an independent confidence
interval for a universal memory bound.

## Reproduction and conclusion

The primary source, binary, configuration, active revisions, runner, client and
upstream hashes are linked in the identity manifest. Final standalone SHA-256 is
`aa9f7a6b09fdcef58e0f5a0c74e2c5da8ce9ec6534941c358a17991181ea7db5`;
backend source SHA-256 starts `399f330441ce`. The baseline archive starts
`68f96a100690`. Official release SHA-256 is
`06b878052d2cd9a16f1b7a0be2359257116f6d318945b1fd912645db48644970`.
Every stage's complete hashes and commands remain in its builds/source manifest.

Use fresh evidence labels; never overwrite a completed or failed batch. Preserve
the frozen archives and run measurement commands sequentially, after check/build
queues drain. From the repository root:

```powershell
python -B scripts/allocation-optimization.py matrix --stages scoped-q0,scoped-final --label reproduce-scoped-http
python -B scripts/allocation-optimization.py matrix --stages scoped-q0,scoped-final --label reproduce-scoped-counters --phase counters
python -B scripts/allocation-optimization.py micro --stages scoped-q0,scoped-final --label reproduce-scoped-native --pattern '^Benchmark(ReferenceLayoutRewrite|NativeThinkingContinuation)$' --iterations 1000
python -B scripts/allocation-optimization.py http --stages scoped-q0,scoped-final --label reproduce-scoped-profiles/standalone-cpu --scenes short,tools,stream,image,cross-stream --mode cpu --rounds 1
python -B scripts/allocation-optimization.py http --stages scoped-q0,scoped-final --label reproduce-scoped-profiles/standalone-memory --scenes short,tools,stream,image,cross-stream --mode memory --rounds 1
python -B scripts/allocation-optimization.py render --stages scoped-q0,scoped-final --label reproduce-scoped-profiles
node scripts/allocation-flame-smoke.mjs .tmp-dev/allocation-optimization-2026-11-04/reproduce-scoped-profiles/profiles
```

The useful changes are deliberately small. This round finds further admissible
optimization, but not a general solution to the remaining historical performance
gap. No production switch is justified. Under the agreed scope, all candidates
have either been accepted with evidence or rejected; no speculative runtime
abstraction is added to force another gain.

Full Go/related race and script checks are Windows results. They do not establish
cross-platform runtime, real-provider latency, full Agent/WebUI performance or
every possible logging/retention configuration. No current user database/log,
Swift, public configuration, supplier-specific Go path, resource limit or
production preset was changed. Nothing was pushed or published.

Implementation commits are `23bc1d5c`, `e2fadec9`, `cdff65b7`; runner/diagnostic,
rejection and regression evidence is split across `dfa784b4`, `12c7d312`,
`178b8a6e`, `2101e5a6`, `82c45a99`, `0be36b68`, `f03332b5`, `8d1852eb`,
`654b9161`, `fe505d6f` and `08947cd8`. This final report is a separate local commit.
