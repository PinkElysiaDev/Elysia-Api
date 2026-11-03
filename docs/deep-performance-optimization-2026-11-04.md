# Deep protocol optimization evidence

Baseline: `951da69d` (implementation), `62d852a7` (preceding documentation).
DSL, Spec and Mapping contracts remain separate and unchanged. Work uses only
isolated databases and local fixtures; no production switch or remote push.

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

## HTTP versus C0

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
