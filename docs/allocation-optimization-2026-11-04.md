# Allocation and continuation optimization evidence

Starting implementation: `7f33ecac` (production code `f2b298f8`). DSL, Spec,
Mapping, carrier format and resource limits remain unchanged. Measurements use
the existing isolated process runner, fixed upstream and driver, Go 1.27.2,
GOMAXPROCS=2, GOGC=100, GOMEMLIMIT=2GiB, CGO/PGO off.

Evidence lives in `.tmp-dev/allocation-optimization-2026-11-04`. Each frozen
candidate records its parent commit, complete backend source hashes, binary
hashes and build commands. Timing, sampling and diagnostic call counters are
separate. Candidates are compared with their direct implementation parent.

The initial benchmarks cover 8/32/64/128-message recovery with and without an
authenticated carrier, 32 B/4 KiB/2 MiB node digests, semantic ownership,
existing field/container shapes, reference rewriting and Native continuation.
The original digest implementation is retained only as a test compatibility
oracle. Candidate results and remaining acceptance work are recorded below.

## Results

### Skip recovery without an input channel

Frozen snapshots `p0` / `skip`, `skip-micro`, `skip-http`, `skip-confirm`.
Ten fixed-iteration micro rounds, five alternating HTTP pairs for short,
history, media and SSE. The 64-message no-carrier recovery benchmark falls
from 5,124,029.5 to 8,102 ns/op and 16,661,779 to 37,168 B/op (the benchmark
still clones its request). The authenticated path is retained; its 64-message
time ratio is 0.9935, CI [0.9865, 1.0106].

HTTP CPU/request: history ratio 0.4972 [0.4661, 0.5312], media 0.9309
[0.9097, 0.9759]. Initial short p95 and SSE first-frame intervals showed tiny
regressions. One predefined five-pair confirmation was added for these two
scenarios; all ten pairs are included in `confirmed-comparison.json`, not
substituted for the first batch. Their CPU ratios are 1.0171 [0.9716, 1.0407]
and 1.0000 [0.9796, 1.0162]. Both latency findings become inconclusive, with
no stable regression in the collected HTTP metrics. Exact process allocation
counts and diagnostic profiles are separate follow-up batches.

Targeted server continuation/Native/preparation regressions passed (11.437 s),
digest/ownership tests passed, and targeted server race passed (133.564 s).
The original history is not captured by a delayed callback: the selected
attempt completes its parent before preparation releases the request.

Remaining candidates and final acceptance are still in progress. These results
do not establish production-switch acceptance.
