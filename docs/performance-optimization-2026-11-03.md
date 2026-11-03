# Profiling-driven optimization evidence

This work follows the seven-step optimization plan for `ed73365a / dev.148`.
All workloads use isolated databases and the fixed local provider. No supplier
traffic or production preset switch is part of these changes. Results below
are Windows amd64 evidence, not cross-platform acceptance.

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
