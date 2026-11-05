# Profiling-driven optimization evidence

This work follows the seven-step optimization plan for `ed73365a / dev.148`.
All workloads use isolated databases and the fixed local provider. No supplier
traffic or production preset switch is part of these changes. Results below
are Windows amd64 evidence, not cross-platform acceptance.

**Final result (2026-11-04):** the planned implementation is frozen at
`951da69d77d0fb63af5b4bf15fae4041decd221b`. All six measured HTTP scenarios
improve CPU and allocation cost against `ed73365a`. The fixed A baseline and
compilation no-regression gates still fail, so production switching remains
blocked. See [final acceptance](#7-final-acceptance-2026-11-04) and the local
[flamegraph and evidence index](../.tmp-dev/optimization-acceptance-report/index.html).

## 1. Execution identity after reload — ac63f0cc

The existing runner now recollects active protocol and policy revisions after
reload, saves `reload-execution.json`, and rejects execution/revision drift.
There is no additional per-request identity check. The real reload regression
confirms that today's package-to-legacy restoration is rejected; an unchanged
package snapshot is accepted. The archived A overlay compiles and provider
self-tests pass. Evidence: `.tmp-dev/optimization-reload-guard/`.

## 2. Pure preflight encoding reuse — a3767f36

Package eligibility is decided after compilation. The first candidate's
single-use encoding binds the compiled snapshot, complete request, limits and
evaluation metadata; diagnostics are replayed once. Mutation, cancellation,
revision/context changes and cache synthesis invalidate reuse. Final wire,
operation and authorization checks remain in place.

Related gateway tests, the 16-direction matrix and targeted race pass. Five
alternating pairs against ed73365a show short/media allocated bytes down
9.18%/21.38%, allocations down 8.09%/24.60%, CPU down 6.80%/29.41%, and P50 down
3.15%/36.18%. Heap/recovery/RSS results remain inconclusive. Evidence:
`.tmp-dev/optimization-preflight-{build,pairs,tests.log,race.log}`.

## 3. Immutable normalization facts

Generated Core readers, private container construction and final materialization
share the existing bounded semantic cache within one synchronous encoding.
Small inputs do not allocate the cache. Mutable conversion paths pass no cache;
storage identity, not content equality, identifies immutable values. Logical
occurrences still count in full. Public APIs and resource limits are unchanged.

The generator and all private reader callers were reviewed. Core/Value/Native
ownership paths were inspected; this is not a claim of whole-repository review.
Protocol and builtin tests pass. Full protocol race passes (34.468 seconds).
The attempted **full builtin race timed out at eight minutes** while still
running tests, with no reported data race; it is not a passing result.

Ten microbenchmark samples of four reads/encodes of the same 2 MiB immutable
body per invocation show about 0.77 ms with the cache versus 1.97 ms without it.
The cache adds two allocations per invocation; output allocation remains about
8.43 MB. This benchmark establishes scan reuse, not HTTP acceptance.

Five alternating HTTP pairs against the preflight snapshot were saved in
`.tmp-dev/optimization-normalization-pairs/`. Media CPU/P50 point estimates
improve 33.33%/26.58%, but their intervals remain inconclusive. SSE CPU/P50 are
inconclusive; peak RSS increases 4.87% in that sample. Short P50 initially
regresses 27.27%. A separate five-pair short recheck does not reproduce it
(P50 −11.74%, CPU −3.23%, both inconclusive); recovery heap increases 5.35%.
Neither the first failed sample nor the recheck is discarded. **Latency and
memory acceptance remain open**, pending the final combined candidate and
independent profiles.

Reproduction inputs, hashes and commands are in
`.tmp-dev/optimization-normalization-build/`, the two pair directories and
`.tmp-dev/optimization-normalization-{benchmark,race,tests}.log`.
The build records the dirty source snapshot; the later bootstrap-comment move
does not affect production code. Profiling and timed comparisons run separately,
with GOMAXPROCS=2, GOGC=100, GOMEMLIMIT=2GiB, CGO_ENABLED=0 and PGO disabled.

## 4. Scoped object field views

Wire shape validation and mapping pointer reads share read-only fields in the
existing input/iteration lifetime. This replaces cached Sonic object trees;
the root plus eight-descendant bound is unchanged. Plain keys borrow input only
in these private views. Public/adapter maps and keys remain owned; retained small
selections still detach at the existing value-reader boundary. Iteration exit
releases the view on success and error. The old parser-error retention tests
now exercise release after mapping failure: the cache no longer owns Sonic
parser errors, and the span scanner only consumes already validated Values.

Protocol/builtin tests and targeted protocol race pass. The targeted builtin
race also covers Native tool/thinking continuation, stream text ranges and
citation layout. Ten field-view microbenchmarks are recorded; view sharing adds
two small workspace allocations compared with a single-use scan, so that
microbenchmark alone is not evidence of allocation savings against the former
cached-AST execution.

Five alternating HTTP pairs against the normalization snapshot show:

| Scenario | Bytes/request | Allocations/request | CPU/request | P50 |
| --- | ---: | ---: | ---: | ---: |
| Short | −13.88% | −15.61% | inconclusive | inconclusive |
| 256-frame SSE | −37.25% | −34.04% | −24.69% | −18.28% |
| 256 KiB history | −1.75% | −5.17% | inconclusive | inconclusive |

SSE sampled peak heap falls 2.14%; other memory and first-frame results remain
inconclusive. Evidence: `.tmp-dev/optimization-field-views-{build,pairs}/` and
`optimization-field-views-{tests,race,benchmark}.log`. The normalization short
CPU profiles each contain about 14 seconds of samples; CPU/request differs by
about 1.5%, which does not explain the first timing run's 27% P50 change. They
remain diagnostic evidence, not a substitute for unprofiled measurements.

## 5. Canonical containers and static request fields

Private nodes record canonical-key/order facts and construction-time encoded
lengths. Already sorted canonical fields no longer go through another map and
sort. Struct fields retain their original wire order; a sorted selection copies
only the field slice. Invalid UTF-8 keys still use the original last-wins
normalization. Lengths are set before publishing a completed node, never lazily
on a shared object; mutable run builders do not cache incomplete lengths.

The generator also binds fixed Request field selectors at compilation. An
initial attempt used the presence of a request pointer as eligibility and
failed `TestPackageInstructionLayoutPreservesNativePlacement`: a local Node
reader could still carry its enclosing request pointer. The final implementation
requires the expression's actual static input type to be Request. The failure
log is retained; corrected protocol/builtin tests and targeted race pass.

Ten canonical-field microbenchmarks avoid six allocations/4,712 bytes per
32-field selection. Five alternating HTTP pairs versus field views reduce short
request bytes/allocations by 3.04%/3.32%; timing remains inconclusive. SSE
bytes/allocations fall 1.00%/0.94%, P50 falls 2.08%, and sampled peak heap falls
2.35%. CPU and RSS remain inconclusive. Evidence:
`.tmp-dev/optimization-container-{build,pairs}/`,
`optimization-container-{tests,fixed-tests,race,benchmark}.log`.

## 6. Release compilation graphs

Compile-only edges, inferred schemas/types and the output-plan backlink now
live in a detachable structure. Publication releases them only after contract,
permission and semantic proofs, including token-count programs. Spec and
conversion compilers release their own completed graphs. Executable plans,
diagnostic locations and Native metadata survive unchanged. Proof unit tests
inspect pre-publication graphs explicitly; production revisions do not keep
the historical expression evaluator alive for tests.

Protocol/builtin tests and targeted race pass. The first test run exposed a
proof fixture inspecting a published graph; its input now comes from the
contract-compilation stage. The original failure log remains available.

A separate memory profile against the container snapshot measured exact
post-GC starting HeapAlloc of 72.293 versus 63.688 MiB (−11.90%). Sampled drained
heap was 78.09 versus 66.57 MiB; sampled values are not exact heap sizes. The
remaining executable graph and authored configuration are still retained.

Ten alternating compilation pairs initially showed 2.4–4.6% more allocations.
Batching 64 short-lived fact records removes nearly all of that increase without
changing limits. The corrected ten pairs still show **1.34–2.53% more allocated
bytes**, and Responses compile time regresses 2.26% with a non-overlapping
interval. Other compile times are inconclusive. This is a retained-memory versus
transient-compilation tradeoff, **not a passing no-regression result**; it remains
an explicit production-switch blocker. The batch build is the final graph
implementation; the first memory profile precedes the batch allocation change.

Evidence: `.tmp-dev/optimization-{container,graph}-memory-profile/`,
`optimization-graph-{compilation,batch-compilation,batch-build}/`, and
`optimization-graph-{tests,fixed-tests,race,batch-tests}.log`.

### Execution roots

Binding allocation now belongs to the existing plan entrypoint. Child nodes
use the initialized bindings directly. Per-node cancellation, step accounting,
diagnostic ordering and Native binding restoration are unchanged; there is no
new interpreter. Protocol/builtin tests and binding/output-plan/Native targeted
race pass. Final combined timing evidence follows below.

## 7. Final acceptance, 2026-11-04

### Frozen implementation and identities

| Commit | Change |
| --- | --- |
| `ac63f0cc` | Reject execution identity drift after reload |
| `a3767f36` | Reuse eligible package preflight encodings |
| `7659a41d` | Reuse immutable normalization facts |
| `cfd3f108` | Share scoped object field views |
| `615cd423` | Avoid rebuilding canonical containers; generate fixed selectors |
| `0bae64fa` | Release compile-only graph facts after proofs |
| `4cb82171` | Initialize bindings at execution roots |
| `84aac46f` | Add reference-rewrite and Native-continuation benchmarks |
| `951da69d` | Simplify generated field-presence conditions |

The final source was clean when frozen. The subsequent documentation commit
does not change the measured implementation. The candidate build manifest is
`.tmp-dev/optimization-acceptance-build/report.json`; it records source archive,
binary, package/configuration hashes, commands and toolchain. Candidate binary
SHA-256 is `9edf747ccba42a5e94173b39154904e89fb405fbac9556549060b329c72a78ea`.
The same fixed local provider is used on both sides of every comparison.

All formal measurements use Go 1.27.2, Windows amd64, Intel Core Ultra 7 270K
Plus, `GOMAXPROCS=2`, `GOGC=100`, `GOMEMLIMIT=2GiB`, `CGO_ENABLED=0`, and PGO off.
The limits and GC settings are not optimization variables. HTTP runs use
isolated databases, disabled request logging and verified exact fixture output.
Provider call counts equal completed workload counts. Execution identity is
checked at startup and snapshot changes, not on each request.

### Unprofiled HTTP comparisons

Each scenario has five alternating baseline/candidate pairs. Per-sample counts
are short 1,024; history 64; tools 128; four-worker burst 512; SSE 32; media 16.
Both sides use identical counts. Media counts were increased from the earlier
four-request exploratory runs; the final comparisons below are separate runs.
CPU and allocated bytes are divided by completed requests. P50 is the median
of each sample's request-latency median; throughput uses completed requests
over the measured wall time. Startup is excluded from the steady-state table;
cold-request and recovery observations remain in the raw files.

Changes against the starting package version `ed73365a`:

| Scenario | CPU/request | Bytes/request | P50 | Throughput |
| --- | ---: | ---: | ---: | ---: |
| Short request | -18.75% | -24.39% | -32.90% | +17.45% |
| 256 KiB history | -23.08% | -14.22% | -17.50% | +25.07% |
| 32 tools | -26.42% | -36.20% | -27.33% | +37.80% |
| Four concurrent short requests | -11.36% | -24.40% | -17.03% | +20.65% |
| 256-frame SSE | -22.08% | -37.93% | -19.77% | +24.54% |
| 2 MiB media | -48.57% | -21.99% | -44.53% | +79.41% |

All metrics in this table have 95% intervals on the improvement side. Across
all recorded metrics in these six final scenarios, none is classified as a
regression. Several peak/recovery heap comparisons are inconclusive; this is
not complete memory acceptance. For example, short peak heap has a +13.52%
point estimate but a 0.744–1.170 ratio interval. SSE recovery heap has a +3.93%
point estimate with a 0.731–1.175 interval. They must not be reported as either
proven improvements or stable regressions.

Against fixed A `b2844846`, substantial stable regressions remain:

| Scenario | CPU ratio C/A | CPU ratio 95% interval | Bytes ratio C/A |
| --- | ---: | ---: | ---: |
| Short request | 2.132 | 1.929–2.343 | 1.919 |
| 256 KiB history | 1.938 | 1.722–2.214 | 1.008 |
| 32 tools | 2.000 | 1.308–2.235 | 2.762 |
| Four concurrent short requests | 2.235 | 2.118–2.533 | 1.913 |
| 256-frame SSE | 4.500 | 3.900–5.083 | 2.718 |
| 2 MiB media | 1.737 | 1.571–2.200 | 0.878 |

The media allocation improvement does not offset its CPU regression. Peak heap,
recovery heap and RSS also regress against A. A's binary was built with the
same toolchain/settings and uses the same fixed provider. These six fixtures
pass the runner's output checks on both versions; failed or incomplete legacy
cases are not included. New upstream token counting is not compared with old
local estimation.

Raw samples, identities, per-metric intervals and failed-run handling are in
`.tmp-dev/optimization-acceptance-vs-ed73365/report.json` and
`.tmp-dev/optimization-acceptance-vs-a/report.json`. The existing comparator
uses 10,000 seeded bootstrap resamples of independent medians, rather than
paired resampling. Five pairs are limited evidence, not a claim about all
deployments. The 5% flag only orders investigation; any stable regression
still blocks switching. A report's `status: passed` means collection completed,
not that its performance gate passed.

### Additional semantic and compilation measurements

Ten alternating microbenchmark pairs against `ed73365a` cover two additional
paths. Reference layout rewriting uses 32 messages, approximately 128 KiB,
emoji and UTF-16 reference relocation. Native continuation includes a native
prefix, an edited/rebuilt thinking delta and subsequent native continuation.
Correctness assertions remain enabled; setup/compilation is outside the timed
loops. The baseline overlay adds the same benchmark and only changes fixture
helper signatures to `testing.TB`; source/overlay hashes are recorded.

| Benchmark | Time | Bytes | Allocations |
| --- | ---: | ---: | ---: |
| Reference layout rewrite | -8.14% | -1.60% | -3.38% |
| Native thinking continuation | -10.16% | -13.86% | -20.30% |

All six intervals indicate improvement. Evidence is in
`.tmp-dev/optimization-semantic-micro/{report,samples,comparison}.json`.
These are protocol benchmarks, not HTTP or complete Agent acceptance.

The compilation tradeoff in section 6 remains unresolved. Ten pairs against
the canonical-container snapshot show allocated bytes up 1.34–2.53%, Responses
compile time up 2.26%, and allocation counts up 0.074–0.081% for three packages.
These are stable regressions even below 5%. Other compilation times remain
inconclusive. The final frozen short/SSE/media diagnostic processes start
after GC with exact heap sizes of 63.686/63.680/64.989 MiB respectively
(`measurements.json`, `Before.Heap`). This supports the retained-heap reduction
but does not establish a pass for transient compilation or natural recovery.

### Flamegraph findings

There are 13 new standalone interactive graphs: short/SSE/media CPU, allocated
bytes and retained bytes (nine), plus reference/Native CPU and allocated bytes
(four). The original 131 graphs remain available. Profiles were collected in
separate processes from the timed comparisons. HTTP allocation profiles subtract
the pre-workload profile; semantic benchmark profiles include one-time fixture
compilation and must not be interpreted as steady-state totals.

| Evidence | Observation and implication |
| --- | --- |
| Media CPU | Normalization cumulative share falls from 39.83% to 13.41%. `EncodeRequestValue` is 26.89%, with preparation now the main encoding chain. Remaining `typedMeter.scanRaw` is 24.04%; GC drain is 18.18%. Immutable measurement reuse is the next candidate, subject to unchanged logical accounting. |
| SSE allocated bytes | `stringslite.Clone` is 20.36%, `Value.readObjectFieldsWithin` 11.57%, `JSONOutput.encodeWithin` 3.83%, and `nativeCaptureState.forItem` 3.23%. Sonic object AST construction is no longer a leading site in this HTTP workload. These shares describe a smaller allocation total, not independent percentage savings. |
| SSE CPU | `compiledOutputPlan.buildNode` is 7.47% flat and 40.16% cumulative; `buildScalar` is 17.53% cumulative. The remaining plan traversal and GC cost require profiling individual operands, not replacement of the interpreter. |
| Short retained heap | Sampled `inuse_space` totals 68.12 MiB: compiler allocation sites account for 26.01 MiB, output plans 11.50 MiB, `decode_` 10 MiB and `Expression.UnmarshalJSON` 4.50 MiB. Allocation-site attribution does not prove that runtime plans or authored configuration can be removed. This sampled drained heap is distinct from exact starting HeapAlloc. |
| Reference rewrite | `buildNode` is 46.79% cumulative; UTF-16 length calculation is 5.14% flat. Clone/Builder/byte construction dominate allocations. Range scans may only reuse facts for the same unchanged text and reference coordinate system. |
| Native continuation | `buildNode` is 59.91% cumulative. Sonic `newLazyObject` still contributes 16.51% of allocated bytes in this separate path, alongside encoding and field maps. The SSE field-view change did not eliminate every AST construction path. |

Cumulative percentages overlap and must not be added. Profiling infrastructure
also appears in samples (for example, `runtime/pprof.allFrames`); it is not an
application optimization target. The graph indices and raw top/cumulative
tables are linked from `.tmp-dev/optimization-acceptance-report/index.html`.

### Correctness and execution gates

| Check | Result and evidence |
| --- | --- |
| `go test ./... -count=1 -timeout=8m` | Pass; `optimization-acceptance-go.log`. Includes the 16-direction JSON/SSE gateway matrix and complex protocol fixtures. |
| `go vet ./...` | Pass; `optimization-acceptance-vet.log`. |
| staticcheck v0.8.2 | Pass via `go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...`; `optimization-final-staticcheck-fixed.log`. Generator-produced double negations were fixed at source and regenerated. |
| Related protocol/builtin race | Pass, 2.482s/9.466s; `optimization-root-batch-race.log`. Earlier stage-specific ownership, Native, reference and stream race results remain linked above. |
| Related server race | Pass, 182.261s; `optimization-acceptance-server-race.log`. Covers prepared encoding, reload drift, full response and stream delivery ranges. |
| Isolated cancellation | Five batches, four workers with 2 MiB bodies; zero active requests, queue depth and queued payload after both settlement and reclamation. `optimization-acceptance-boundaries/cancellation-*.json`. |
| Production execution identity | **Blocked.** Actual defaults select legacy codecs. The production-mode probe rejects the mismatch before accepting measurements; `optimization-acceptance-boundaries/production/run.log`. It is not a successful package production run. |
| Full builtin race | Earlier attempt timed out after eight minutes; not claimed as passed. See section 3. |

Log paths in the table are under `.tmp-dev/`. The staticcheck executable was
not on PATH; the initial command-not-found and pre-fix diagnostics remain in
the evidence. Failed test/profile experiments and inconclusive comparisons
have not been replaced with only their successful reruns.

Review covered changed protocol/server files, generator output and callers,
cache/ownership lifetimes, compilation proofs and release boundaries. Other
references were statically scanned; this is not a whole-repository line-by-line
review. Windows local fixtures do not establish all Agent, management API,
WebUI, real supplier, cross-platform or production performance. Existing
databases/logs and Swift were untouched. No remote push or publication occurred.

### Remaining optimization order

1. Remove the measured compile allocation/time regression. Profile transient
   fact allocation and schema/output-plan construction against the container
   snapshot. Keep proof ordering and graph release; do not restore retained
   compile-only graphs or enlarge limits. Require ten matched compilation pairs
   plus retained-heap evidence before closing this gate.
2. Trace retained authored conversion-option declarations separately from
   runtime plans. Inspect ownership and revision/snapshot lifetime before
   removing any representation. Require reload, old-snapshot and Native proof
   tests, plus exact post-GC and sampled heap comparisons. The current profile
   identifies allocation sites, not proven removable owners.
3. Reduce remaining field-map and small-string copies within existing immutable
   execution scopes. Start with SSE and the independently identified Native
   AST path. Keep detachment at Core/Native/state/diagnostic retention boundaries;
   test repeated keys, precise numbers, escaping, invalid UTF-8 and partial
   delivery. Do not introduce a global cache or pool with longer ownership.
4. Investigate `typedMeter.scanRaw` reuse only where immutable identity, scope
   and budget semantics match. Charge every logical occurrence; invalidate on
   modification. Recheck media, reference rewrite, cancellation and limits
   before claiming scan removal. Keep final wire and permission boundaries.
5. Reprofile changed hotspots separately from measurements. Repeat the affected
   ten-sample microbenchmarks and five-pair HTTP cases; investigate uncertain
   memory results where changes affect them. Keep A and compilation gates
   blocked until stable regressions are removed. Do not switch presets based
   only on the improvements over `ed73365a`.

### Reproduction

Run from the repository root using the archived builds/provider and the same
toolchain. Choose new output directories; collectors refuse to overwrite an
existing run. Runner copies, commands, source/binary hashes and environments
are retained inside each evidence directory. The one-off orchestration below
reuses the tracked runner and comparator; `.tmp-dev` artifacts are local and
must be retained alongside the checkout for exact reproduction.

```powershell
node .tmp-dev/run-profiling-optimization-pairs.mjs --baseline=.tmp-dev/profiling-ed73365-current --candidate=.tmp-dev/optimization-acceptance-build --image-requests=16 --output=.tmp-dev/recheck-vs-ed73365
node .tmp-dev/run-profiling-optimization-pairs.mjs --baseline=.tmp-dev/fixed-http-a-build --candidate=.tmp-dev/optimization-acceptance-build --image-requests=16 --output=.tmp-dev/recheck-vs-a
python .tmp-dev/profile-optimization-build.py --build=.tmp-dev/optimization-acceptance-build --output=.tmp-dev/recheck-profiles
python .tmp-dev/render-optimization-flames.py --profiles=.tmp-dev/recheck-profiles --output=.tmp-dev/recheck-flames
```

Microbenchmark manifests record each command and alternating order. Their
frozen test binaries can be rerun with `-test.run=^$`,
`-test.bench=^Benchmark(ReferenceLayoutRewrite|NativeThinkingContinuation)$`,
`-test.benchtime=300ms`, `-test.count=1` and `-test.benchmem`, using the recorded
working directories and environment. For a rebuild, use the recorded overlay
hashes; exact helper sources are in the frozen candidate source archive.
Separate 10-second CPU/memory profile commands are in
`.tmp-dev/optimization-semantic-profiles/report.json`.
