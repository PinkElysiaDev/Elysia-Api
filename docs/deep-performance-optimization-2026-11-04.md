# Deep protocol optimization evidence

Baseline: `951da69d` (implementation), `62d852a7` (preceding documentation).
DSL, Spec and Mapping contracts remain separate and unchanged. Work uses only
isolated databases and local fixtures; no production switch or remote push.

Final implementation: `f2b298f8`. Six HTTP workloads, each with five alternating
pairs against C0, show no detected stable regression. SSE CPU/request improves
12.61% and allocated bytes/request improve 14.92%. The previous compilation
regression and the intermediate reference-allocation regression are closed.
Full Go tests, vet, staticcheck and the relevant race selections pass.
**Historical A performance and production-entrypoint gates remain blocked.**

[Interactive report and 27 flamegraphs](../.tmp-dev/deep-optimization-report/index.html)
include final HTTP/compilation/reference/Native profiles, C0 micro profiles and
the intermediate candidate. [Machine-readable acceptance](../.tmp-dev/deep-optimization-report/acceptance.json)
retains intervals, execution identities and the failed production gate. These
large local artifacts are ignored by Git; this document and benchmark sources
are committed. The final results below supersede intermediate measurements,
which remain available to explain rejected experiments and corrections.

## Diagnostic workloads

`BenchmarkDeepCompilation` separates authored decoding, contract compilation and
policy composition. `BenchmarkDeepExpressionDecode` varies nesting; scalar-read
benchmarks exercise direct and rebound input, and the Core object benchmark
preserves repeated-object merging. Existing package, reference, Native and HTTP
benchmarks remain the acceptance workloads.

`ELYSIA_DEEP_DIAGNOSTICS=1` enables `TestDeepCompilationDiagnostics` in a fresh
process. It records exact post-GC heaps for four packages and sixteen policies,
and unique reachable runtime instructions/plans. Shallow graph sizes are not
total retained heap. Codec warmup is excluded from the retained-heap delta.

Exploratory compiler profiles and four standalone flamegraphs are in
`.tmp-dev/deep-plan-2026-11-04/`; they are diagnosis, not timed acceptance.
Formal runs use Go 1.27.2, GOMAXPROCS=2, GOGC=100, GOMEMLIMIT=2GiB and PGO off.
Stable regressions remain blockers regardless of the 5% investigation flag.

The benchmark smoke run passes. Baseline test binaries are
`.tmp-dev/deep-c0-{protocol,builtin}.test.exe`; retained diagnostics pass in
`deep-c0-diagnostics-fixed.log`. The first invocation used the wrong fixture
working directory and failed before compilation; its log is retained. All
protocol benchmark binaries must run from `backend/protocol` (builtin binaries
from `backend/protocol/builtin`). No smoke/profile timing is acceptance data.

## Expression decoding and field contracts

Typed decoding now shares its input string with a key-only presence scan, rather
than decoding a second RawMessage tree. Private field bits and catalog-derived
allowed/required masks replace per-expression map/reflect membership checks.
Actual Go values are checked independently; authored empty fields and nonstandard
key spelling still participate. Public source serialization is unchanged.

Protocol/builtin tests, related compiler race and a 15-second field-presence
fuzz run (355,774 executions) pass. Ten alternating whole-package pairs versus
C0 improve compilation time by 10.97%/23.67%/24.10%/23.22% for chat, Responses,
Anthropic and Gemini; allocated bytes fall 27.69%/20.20%/32.07%/31.58%.
All intervals indicate improvement. Nested-expression microbenchmarks have
their own ten-pair report. Evidence: `.tmp-dev/deep-expression-{compilation,
nested}/` and `deep-expression-{tests,race,fuzz}.log`. These are compilation
results; final HTTP and historical-A gates remain open.

## Retained conversion declarations

Prepared controls/recovery and resolved model options release authored trees
after their plans are compiled. Runtime diagnostics keep only reporting fields;
their reason/location expressions live in the executable plans. Raw public
rules and definitions remain available for overrides, hashes and export.

The first candidate also pre-encoded source normalization and discarded its
authoring tree. Ten compilation pairs found stable allocation-count increases
of 0.195%/0.197% for Anthropic/Gemini. That part was **removed**, not accepted
under the 5% flag. Target/source declarations needed by default composition
therefore still use the original representation. Its code diff and measurements
remain in `.tmp-dev/deep-options-compilation/`.

The corrected ten-pair run classifies all compilation metrics as inconclusive,
with no stable regression. Related tests/race pass. Exact diagnostic post-GC
heap for four packages plus sixteen policies changes from 93,564,968 to
76,285,040 bytes; retained delta after codec warmup changes from 84,933,480 to
67,417,120 bytes. These single diagnostic runs establish ownership reduction,
not final HTTP memory acceptance. Evidence: `deep-options-fixed-{compilation/,
diagnostics.log,tests.log}` and `deep-options-race.log` under `.tmp-dev/`.

## Ordered Core object scans

Raw Core object writers now scan validated JSON members in order without Sonic
ASTs or temporary field slices. Generated writers keep their original direct
field loop for constructed plans. Materializing callers retain an ordered-field
helper; small retained Core values still detach from large input bodies.

The first iterator applied to both raw and constructed objects. Ten pairs
showed about 10% slower constructed writes; that implementation was corrected,
and the original evidence remains in `deep-core-{objects,semantic}/`.
Corrected ten-pair results: repeated raw Core objects improve time by 77.52%,
bytes by 91.70%, allocations by 72.97%; constructed/materialized request metrics
are inconclusive with no stable regression. Native continuation improves time
by 5.96%, bytes by 21.06%, allocations by 7.19%; reference rewrite is inconclusive.
Related tests and race pass, including repeated fields, exact numbers, first
errors and owned small selections. Evidence: `.tmp-dev/deep-core-fixed-
{objects,semantic}/`, `deep-core-{iterator-tests,fixed-tests,race}.log`.

## Non-escaping scalar reads

Statically selected read/literal predicates borrow their intermediate fragments
with a stateless reader; sibling/output reads keep their ownership rules. Small
operand lists use the existing two-element stack workspace. Evaluation remains
eager, with unchanged node, cancellation, byte and error boundaries.

Ten alternating pairs show direct predicate time improvements of 3.94–6.44%,
with one fewer allocation (16.67–20%). Scoped cases are inconclusive with no
stable regression. Reference and Native continuation time is inconclusive;
allocations improve 1.12% and 9.16%, respectively. Full protocol/builtin tests
and targeted protocol race pass. The race selector matched no builtin tests;
it is not recorded as builtin race coverage. Evidence: `deep-scalar-{reads/,
semantic/,tests.log}` and `deep-scalar-initial-race.log` under `.tmp-dev/`.

## Initial semantic measurements

The initial successful typed walk now lazily records large immutable raw values
in the same segment cache used by Core reads and encoding. Small walks allocate
no cache storage; failed walks publish no cache. Occurrence aggregation and
smaller-budget failure diagnostics remain independent.

The first candidate reduced large encoding time but added one allocation: its
cache-limit pointer escaped. Escape analysis identified the cause; copying the
limits into the meter removes it. Corrected ten-pair results improve 2 MiB Core
encoding time by 26.04%, with unchanged bytes and allocations. Native/reference
metrics are inconclusive with no stable regression. Protocol/builtin tests and
targeted protocol race pass. Evidence: `deep-initial-{encoding/,escape.log,
tests.log}`, `deep-initial-fixed-{encoding/,semantic/,race.log}`. The unsuccessful
candidate remains archived. String-scan fusion has not been introduced.

## Instruction metadata

Cold Native, stream and diagnostic fields now share a single allocation with
only the instructions that need them. Hot instructions shrink from 296 to
224 bytes. Four-package-plus-sixteen-policy diagnostic heap is 65,449,000 bytes;
the two retained graph types and cold metadata are counted separately. This is
a diagnostic snapshot, not an HTTP memory claim.

The enum-dispatch candidate was rejected: Native continuation time regressed
1.77%, then 1.24% with a hotter field layout, and 1.68% with cold fields restored
inline. CPU samples still locate substantial work in buildNode and its defer
boundaries. Runtime operation/source/policy names therefore remain strings.
Evidence and source are archived in `deep-instructions-{semantic/,reads/,
hot-native/,inline-native/,rejected/}` and the parent/candidate Native profiles.

Cold metadata alone passes ten pairs: compile bytes improve 1.99–3.20%; Native
and reference metrics are inconclusive without stable regression. Evidence:
`.tmp-dev/deep-cold-{semantic/,diagnostics.log,race.log}`.
The broad race selection passes protocol but times out in builtin at 300s;
that builtin run is not a pass. Full non-race protocol/builtin tests pass with
the subsequent selector candidate; targeted builtin race is run separately.

## Static selection experiment and execution boundary

The generated Response/Event/Node/Usage selector experiment passed full tests
and scope/reader parity checks. Its ten-pair primary semantic metrics were
inconclusive, while equal-with-input regressed 6.06%. It was removed; the
existing Request selector remains. Code and results are preserved in
`deep-selectors-{semantic/,reads/,rejected/,tests.log,scopes.log}`. A proposed
additional direct-selector benchmark in that archive was not run and is not
performance evidence.

After the new Native CPU samples, dispatch now borrows the caller's local
evaluation scope and uses one explicit return boundary for byte limits and
error location. Node advancement and reader detection also borrow their local
scope. Nested operations still copy their scope as before; no shared mutable
environment or extra environment allocation was added. The original step,
cancellation, eager operand and precise error checks remain.

Ten pairs improve reference rewrite time by 10.58%, Native continuation by
14.83%, and scalar/scope cases by 6.78–12.75%. Large encoding is inconclusive;
no metric shows a stable regression. Full protocol/builtin tests and targeted
protocol and three builtin Native race tests pass. Evidence:
`deep-dispatch-{semantic/,reads/,tests.log,race.log}`, `deep-native-race.log`.

## First frozen candidate and correctness

Candidate: `0d2cd64b` (production implementation frozen at `a61fa9c2`; the last
commit only corrects a test helper's error-result position). Its source,
binary, toolchain, fixture and provider identities are preserved in
`.tmp-dev/deep-final-build/`. C0, historical A and the canonical-container
source archives and binary hashes were verified before comparison; see
`.tmp-dev/deep-final-baselines.json`.

Full `go test ./... -count=1 -timeout=8m` and `go vet ./...` pass, including the
16-direction local JSON/SSE gateway matrix. Staticcheck v0.8.2 initially
reported ST1008 in the new test helper; the correction and targeted test pass,
then staticcheck passes without diagnostics. Full tests were not repeated for
that test-only signature change. Targeted protocol, builtin Native and server
race runs pass. Server race covers prepared encodings, reload identity drift,
in-flight revisions, Native reconstruction and stream delivery ranges (139.082s).
The earlier broad builtin race timeout remains a failed/incomplete run.

Evidence logs under `.tmp-dev/`: `deep-final-{go-test,vet,staticcheck,
staticcheck-fixed,server-race}.log`, `deep-dispatch-race.log`,
`deep-native-race.log`. No supplier, production database, log, Swift, preset
default or public contract was changed. Windows checks do not establish other
platform, complete Agent, or WebUI performance acceptance.

## Intermediate HTTP versus C0 (`0d2cd64b`)

Five alternating pairs per scenario, identical frozen local provider,
GOMAXPROCS=2/GOGC=100/GOMEMLIMIT=2GiB, profiler disabled. Percentages below are
median changes; `uncertain` means the bootstrap interval includes no change.
Every request verifies complete output and exactly one upstream generation.

| Workload | CPU/request | Allocated bytes/request | Allocations/request |
|---|---:|---:|---:|
| Short | −8.43% (uncertain) | −1.70% | −3.16% |
| 256 KiB history | −10.00% | −9.64% | −2.20% |
| 32 tools | −2.78% (uncertain) | −2.26% | −1.66% |
| Four concurrent short requests | −15.79% | −2.18% | −3.35% |
| 256-frame SSE | −9.57% (uncertain) | −14.89% | −6.06% |
| 2 MiB media | −22.22% | +0.79% (uncertain) | −1.77% |

No measured metric has a stable regression against C0, including latency,
first frame, throughput, peak memory and recovery heap. Some of those metrics
remain noisy; this is absence of a detected regression in these workloads,
not a universal performance guarantee. Complete intervals and samples:
`.tmp-dev/deep-final-http-c0/report.json`.

## Follow-up: text consumed by joins

Final warmed microbenchmarks exposed a reference-rewrite allocation regression
against C0: +0.51% allocated bytes, reproduced at +0.27% with ten longer 2-second
pairs. Both failures remain in `deep-final-builtin-c0/` and
`deep-final-reference-investigation/`; the 5% investigation flag did not excuse
them. Matched 1,000-iteration allocation profiles expose substantial local
text cloning in join. Sampling cannot attribute the entire small net increase
to that site, but the unnecessary copy is independently measurable.

Join now uses the existing borrowed text reader before immediately copying
each fragment into its owned builder/output. All operand evaluation, decoded
byte limits and error ordering stay in place. No buffer pool, cache limit or
GC setting changes. Ten parent/candidate pairs improve short/ASCII/Unicode
join time by 6.66%/22.63%/15.79%; escaped text is inconclusive without stable
regression. Reference rewrite improves another 2.29% in time and 5.68% in bytes;
Native time is inconclusive and allocations improve.

Ten longer pairs against C0 now improve reference rewrite time by 13.13%, bytes
by 5.40%, and allocations by 1.29%, closing the observed allocation regression.
Boundary tests compare exact codec errors, resource counts and retained output
ownership. Evidence: `deep-join-{strings/,semantic/,reference-c0/,tests.log,
race.log,benchmark-source.json}`; the parent test-only overlay is recorded
without modifying its frozen source archive.

## Final HTTP acceptance (`f2b298f8`)

The accepted snapshot is `.tmp-dev/deep-accepted-build/`, created from a clean
worktree. Its source identity SHA-256 is
`671b69cd496acf055633e8d836a89a50ac9a82cf62aaede3f874e03dc51e9059`;
server test binary SHA-256 is
`164ec67f24b94ac0abccced81f49a5b0f20e03617e5260f2c77afe0487fec85e`.
It uses Go 1.27.2 on Windows/amd64, CGO_ENABLED=0, GOMAXPROCS=2, GOGC=100,
GOMEMLIMIT=2GiB, PGO off, the standard transport and one frozen local provider.
No limits, supplier configuration or GC settings were changed to obtain gains.

Each scenario has five alternating pairs without profiling. The existing
comparator reports a seeded 10,000-resample bootstrap interval for the ratio
of independent medians; alternating collection is not a paired statistical
estimator. `uncertain` means its 95% interval includes one. Every measured
request verifies complete output and exactly one upstream generation. The
`passed` field in collection reports means collection succeeded; individual
metric assessments determine acceptance. Per-pair request counts are
1,024/64/128/512/32/4 for short/history/tools/concurrent/SSE/media. The small
media sample contributes to its unresolved CPU and throughput results.

| Workload | CPU/request | Allocated bytes/request | Allocations/request | Throughput |
|---|---:|---:|---:|---:|
| Short | −10.84% (uncertain) | −1.72% | −3.54% | +8.73% |
| 256 KiB history | −13.79% | −14.89% | −3.52% | +12.53% |
| 32 tools | 0.00% (uncertain) | −2.29% | −1.73% | +4.60% |
| Four concurrent short requests | −13.51% | −1.71% | −3.54% | +16.41% |
| 256-frame SSE | −12.61% | −14.92% | −6.52% | +16.30% |
| 2 MiB media | −11.11% (uncertain) | −0.77% (uncertain) | −2.18% | −10.65% (uncertain) |

No measured metric has a stable regression against C0, including latency,
first frame, throughput, peak heap/RSS and recovery heap. In particular, media
throughput is unresolved, not an improvement; the earlier candidate's media
CPU result cannot replace this final result. Peak heap improves in all six
scenarios; recovery heap is inconclusive except for media. Evidence:
`.tmp-dev/deep-accepted-http-c0/report.json` with all raw samples and commands.

The historical A comparison also uses five alternating pairs and the same
provider. Ratios below are final/A; every CPU ratio is a stable regression.

| Workload | CPU/request ratio | Allocated bytes/request ratio |
|---|---:|---:|
| Short | 2.000× | 1.903× |
| 256 KiB history | 1.655× | 0.856× |
| 32 tools | 2.188× | 2.704× |
| Four concurrent short requests | 2.000× | 1.892× |
| 256-frame SSE | 4.478× | 2.307× |
| 2 MiB media | 1.750× | 0.878× |

Allocation counts, peak heap and recovery heap also remain worse than A in all
six workloads. These results continue to block production switching; local
improvements do not establish historical parity. Complete assessments are in
`.tmp-dev/deep-accepted-http-a/report.json`.

## Compilation and execution acceptance

Ten alternating warmed pairs against C0 and the canonical-container snapshot
close the earlier compilation regression. A warm run uses two benchmark
passes in each fresh process and records only the second, retaining the first
as explicit warmup. The canonical snapshot was dirty when archived: its commit
alone is insufficient identity. Verified archive hashes, all source files,
compiler binary and matching fixtures are recorded in
`.tmp-dev/deep-final-baselines.json`.

| Package | Compile time vs C0 | Compile time vs canonical | Bytes vs canonical | Allocations vs canonical |
|---|---:|---:|---:|---:|
| Chat | −21.12% | −20.91% | −38.90% | −36.77% |
| Responses | −29.81% | −23.17% | −20.32% | −20.60% |
| Anthropic | −23.11% | −23.07% | −33.35% | −34.95% |
| Gemini | −22.34% | −22.28% | −32.98% | −33.05% |

All intervals indicate improvement. These measurements use `0d2cd64b`; the
final join follow-up changes execution only, leaving compiler implementation
unchanged. Final `f2b298f8` compiler profiles were collected independently.
Reports: `deep-final-builtin-c0/` and `deep-final-compile-canonical/`.

The separate cold experiment uses `1x` in ten alternating fresh-process pairs:
chat includes initial codec startup and improves 5.63%; later Responses,
Anthropic and Gemini improve 23.87%/17.85%/21.46%. Those later packages can
benefit from earlier codec initialization in the same process, so this is a
fresh-process four-package pass, not four independently cold starts. Evidence:
`deep-final-compile-cold/`.

Other ten-pair C0 comparisons at `0d2cd64b` show nested expression decode time
improving 29.33–42.69%, scalar/scoped predicates 10.59–18.74%, repeated raw Core
objects 73.55% and 2 MiB Core-plan encoding 26.54%. Native continuation improves
21.78% in time, 23.20% in bytes and 15.69% in allocations. Final join versus its
parent has no stable Native time/byte change and improves allocation count;
the Native percentages above are not relabeled as direct final/C0 results.
Reports: `deep-final-protocol-c0/`, `deep-final-builtin-c0/`,
`deep-join-semantic/`.

The final reference rewrite comparison is direct against C0, ten warmed
2-second pairs: time −13.13%, bytes −5.40%, allocations −1.29%. It supersedes
the earlier +0.51%/+0.27% byte regressions; those failures remain archived.
The shorter 300ms and longer 2-second runs are separate comparisons; their
absolute timings must not be combined. Evidence: `deep-join-reference-c0/`.

## Final profiles and remaining costs

Final micro CPU and memory profiles run in separate processes at fixed
iterations: 30 per package, 1,000 reference rewrites and 10,000 Native
continuations. Fixture compilation is included in these diagnostic profiles.
HTTP CPU profiling uses a fixed duration; completed request counts differ, so
sample totals alone cannot be compared as request cost. The final/C0 counts
are 14,336/13,312 short requests, 320/256 SSE requests and 464/424 media
requests. CPU and allocation timing acceptance remains profiler-free.

| Observed path | C0 → final diagnostic evidence | Interpretation |
|---|---|---|
| Compilation | Total CPU samples 11.68s → 8.15s; `checkExpressionFields` cumulative 1.35s → 0.05s | Static masks remove most field-contract work without removing checks. |
| Expression decode | Cumulative allocated space 5,646.22 → 2,653.77 pprof MB; CPU 2.34s → 2.04s | The second value tree is gone; recursive typed decoding remains. Its final CPU percentage rises because total CPU falls faster. |
| Native Core writes | `ast.newLazyObject` accounts for 498.49 pprof MB in C0 and has no sampled allocation in the final run | Ordered Core scanning removes that construction path; absence in a sample does not prove zero allocation everywhere. |
| Reference join | Cumulative join allocation 1,606.77 → 1,341.34 pprof MB; total sampled `strings.Clone` allocation 987.62 → 702.08 MB | Borrowing locally consumed text reduces a real copy; owned outputs and provenance remain. |
| Native dispatch | `buildNode` flat 0.64s plus deferred boundaries 0.18s → `buildNode` + `buildOperation` flat 0.60s | Compare both new functions together; moving dispatch does not eliminate its work. |

These are sampled costs, not confidence intervals. Cumulative values overlap
and are never summed as independent savings. Raw top/cumulative files and
commands are in `deep-accepted-micro-profiles/`,
`deep-final-c0-micro-profiles/` and
`deep-optimization-report/profile-comparison.json`.

The final fresh-process retained-graph diagnostic keeps the same four packages
and sixteen policies as C0: 100,631 expression nodes, 100,631 output plans and
1,974 cold detail records. Post-GC heap changes from 103,722,848 to 66,499,976
bytes; delta after codec warmup changes from 91,533,360 to 53,080,176 bytes.
These single snapshots describe retained ownership, not a repeated HTTP memory
estimate. Public definitions, runtime plans and source/target composition
declarations remain retained intentionally. `deep-accepted-diagnostics-fixed.log`
passes; the first diagnostic invocation's PowerShell flag-parsing failure is
preserved separately and did not run the test.

Remaining hotspots provide boundaries for future work, not extra accepted
optimizations in this round:

- SSE field views still allocate: `readObjectFieldsWithin` is 15.35% of final
  sampled allocation. Immediate local consumers may offer more borrowing
  opportunities, but values entering Core, state or later frames still need
  independent ownership. Expanding caches or reusing mutable readers is not
  justified by this evidence.
- `buildNode` plus `buildOperation` still contribute 8.19% flat CPU in SSE and
  8.09% in short requests. Numeric dispatch and broader generated selectors
  were measured and rejected; this is not grounds to reinstate them or add a VM.
- Media `typedMeter.scanRaw` remains 25.92% cumulative CPU; this includes
  necessary first scans and distinct semantic boundaries. Segment-local facts
  cannot establish validity for a rewritten document or another permission
  scope. String-scan fusion remains unimplemented and has no acceptance claim.
- Final short-request drained heap samples attribute 54.44% to expression and
  output-plan allocations. Runtime graphs remain necessary; further retention
  work must distinguish public authored configuration, executable plans and
  temporary proof data before removing any owner.
- Native allocation now emphasizes `JSONOutput.encodeWithin` (12.15% flat),
  field views (11.51%) and output-part assembly (9.59%). Native source identity,
  semantic checks and delivery transactions still define separate boundaries.

## Final correctness, production identity and scope

After the join follow-up, `go test ./... -count=1 -timeout=8m`, `go vet ./...`
and staticcheck v0.8.2 all pass. The full server suite takes 133.799s and
includes the 16-direction local JSON/SSE matrix. Join-related protocol race
passes in 3.902s. Prior targeted protocol, three builtin Native cases and
server race remain applicable; server coverage includes prepared encodings,
revision drift, in-flight snapshots, Native reconstruction and delivery
ranges. Logs: `deep-final-fixed-{go-test,vet,staticcheck}.log`,
`deep-join-race.log`, `deep-dispatch-race.log`, `deep-native-race.log` and
`deep-final-server-race.log`.

The earlier 300s broad builtin race timeout is incomplete coverage, not a pass.
The initial staticcheck test-helper diagnostic was fixed and rechecked. No
cross-platform, complete Agent or WebUI performance acceptance is claimed.
The generated Core writer was changed through its generator; Swift is untouched.

The final frozen binary was also run once in `production` execution mode
without injecting packages, against the isolated local provider. The identity
guard rejects its active legacy `openai-chat` codec at `decode_request`, revision
`984c7f97c1b524cdfa4b6b2d09c232470df0ffe1927524e66299e6a794b6aaf9`.
This is a **blocked production gate**, not a passed package run. Evidence and
exact command: `.tmp-dev/deep-accepted-production/result.json` and `run.log`.
No production preset switch is included in this work.

## Local commits and reproduction

| Commit | Change |
|---|---|
| `ecc32257` | Compiler/execution diagnostics and directed benchmarks |
| `ec4142c7` | Single expression value decode and field masks |
| `05d972db` | Release redundant authored option trees |
| `b2c6f7df` | Ordered raw Core scans and generator changes |
| `df0196e6` | Non-escaping scalar reads and small operand workspace |
| `73169b2c` | Reuse initial immutable semantic measurements |
| `9753a6a3` | Compact cold instruction metadata; retain string dispatch |
| `a61fa9c2` | Smaller evaluation copying and explicit success boundary |
| `0d2cd64b` | Staticcheck correction in the test helper |
| `f2b298f8` | Borrow text immediately consumed by joins |

Authored model pre-encoding, numeric opcodes, expanded Core selectors and the
first constructed-node iterator were rejected or corrected based on measured
regressions. The original recursive executor, scope ownership and Request
selector remain. No shared mutable evaluation environment was introduced.

From the repository root, rebuild the final micro binaries from the frozen
source, then use the archived runners with a new output directory:

```powershell
$env:CGO_ENABLED = '0'
go -C .tmp-dev/deep-accepted-build/source/backend test -pgo=off -c -o D:/DevProjects/Elysia-API/Elysia-Api/.tmp-dev/deep-accepted-builtin.test.exe ./protocol/builtin
python .tmp-dev/measure-deep-stage.py --baseline=.tmp-dev/deep-c0-builtin.test.exe --candidate=.tmp-dev/deep-accepted-builtin.test.exe --package=protocol/builtin --bench='^BenchmarkReferenceLayoutRewrite$' --warm --time=2s --output=.tmp-dev/deep-reference-reproduction
python .tmp-dev/profile-deep-micro.py --binary=.tmp-dev/deep-accepted-builtin.test.exe --source-build=.tmp-dev/deep-accepted-build --output=.tmp-dev/deep-micro-reproduction
python .tmp-dev/profile-optimization-build.py --build=.tmp-dev/deep-accepted-build --output=.tmp-dev/deep-http-profile-reproduction
python .tmp-dev/render-optimization-flames.py --profiles=.tmp-dev/deep-micro-reproduction --output=.tmp-dev/deep-micro-flame-reproduction
Copy-Item -LiteralPath .tmp-dev/deep-accepted-http-c0/runner-source.mjs -Destination .tmp-dev/deep-http-reproduction.mjs
node .tmp-dev/deep-http-reproduction.mjs --baseline=.tmp-dev/optimization-acceptance-build --candidate=.tmp-dev/deep-accepted-build --output=.tmp-dev/deep-http-c0-reproduction
```

Micro runners set the fixed runtime environment and reuse
`scripts/verification/compare.mjs`. HTTP `runner-source.mjs`, each invocation's
arguments/environment, binary/source hashes and provider identity are archived
under `deep-accepted-http-{c0,a}/`; replay with new output paths and retain the
same scenario sizes and alternating order. Baseline profiles are separate from
timing runs. The prepared PowerShell wrapper was blocked by local execution
policy and was not used; evidence came from direct Go/Python/Node commands.

The copied HTTP runner must stay directly under `.tmp-dev/` so its relative
imports resolve. For a portable regeneration, build the committed benchmark
sources at the listed revisions and replay the archived Go test arguments and
environment. Retain the verified historical canonical source archive because
its dirty snapshot is not reconstructible from a commit alone. Raw local
profiles and rejected experiments are evidence artifacts, not production inputs.
