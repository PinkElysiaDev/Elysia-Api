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
