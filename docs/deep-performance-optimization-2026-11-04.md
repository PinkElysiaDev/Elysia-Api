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
