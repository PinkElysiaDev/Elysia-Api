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
counts and diagnostic profiles were collected in separate follow-up batches.

Five exact-counter pairs (`skip-counters`) put history at 25,924,515 →
9,488,690 B/request, ratio 0.3660 [0.3659, 0.3662]. Media remains 44,399,801 →
44,340,063 B/request, inconclusive: its large digest buffers still need work.
The twelve interactive CPU/alloc/inuse and difference graphs are indexed at
`skip-profiles/profiles/index.html`. History's digest allocation subtree drops
from an estimated 17,645,285 to 1,012,083 B/request. Separate CPU profiles show
the digest subtree at 3.945 → 0.117 ms/request and GC at 7.109 → 2.891 ms/request;
these samples explain the change, not substitute for the timed comparison.

Targeted server continuation/Native/preparation regressions passed (11.437 s),
digest/ownership tests passed, and targeted server race passed (133.564 s).
The original history is not captured by a delayed callback: the selected
attempt completes its parent before preparation releases the request.

### Reuse authenticated history prefixes

Frozen parent `skip`, candidate `history-local`, `history-micro` and
`history-http`. Each micro iteration creates a fresh digest table; only
candidates within one preparation can share it. The table is a local variable
of preparation, not a field retained by a gateway or delivery object.

Ten rounds of the authenticated 64-message recovery benchmark change time
5,276,086.5 → 360,428.5 ns/op, bytes 16,699,301 → 1,100,911.5 B/op and objects
18,784 → 1,941. The no-carrier case stays at 37,168 B/op and 67 allocs/op.
All authenticated 8/32/64/128-message shapes improve time and allocation;
the no-carrier timing differences are inconclusive, without stable regression.
Five HTTP control pairs each for short and history show no stable regression.
These controls contain no carrier, so they are not an end-to-end claim for
authenticated recovery. Dedicated recovery profiling and HTTP supplements
remain part of final acceptance.

Prefix parity covers every root boundary, nested/inherited roles, tool role
changes, empty history and cancellation. Existing authenticated, persisted and
encrypted-reasoning gateway regressions pass (7.920 s); the frozen candidate's
targeted server race passes (130.335 s). Authenticated node-content checking is
unchanged. The earlier `history` snapshot was superseded before measurement;
only `history-local` is used in this comparison.

### Hash large payloads without complete JSON buffers

The universal projection trial `digest-all` was rejected after ten rounds:
32-byte nodes regressed from 504.7 to 840.8 ns/op and 853 to 2,200 B/op.
It remains archived; no HTTP improvement is claimed for that candidate.

Accepted candidate `digest-select` chooses the projection for encoded payloads
of at least 2 KiB, including nested media, tool input and metadata. Smaller nodes
use the existing Sonic encoding directly as SHA input, removing the intermediate
Value/string conversion. Both choices are made before encoding; errors do not
re-execute an alternate implementation. Original canonicalization and hashes
remain compatible. Existing public Value ownership is unchanged.

Matched benchmark overlays `history-fixture` / `digest-fixture` retain frozen
parent/candidate production code and record the same updated test-source hashes.
Ten alternating rounds use 100,000 iterations for 32/1024/2048 bytes, 20,000
for 4096 and 1,000 for 2 MiB and nested media. Small-node time improves 4.98%,
1024 bytes 13.70%, 2048 bytes 35.51%, 4096 bytes 43.91%; nested media improves
64.77%, with allocation 12,725,376 → 3,440 B/op. All measured shapes improve
time and allocation without stable regression. An initial grouped regex omitted
the pure-text 2 MiB case; its separate `digest-select-text-large` batch completes
ten rounds, improving time 64.81% and allocation 99.98%. It was not inferred from
the nested-media case.

Five standalone HTTP pairs (`digest-http`) cover short/history/media/SSE.
Media CPU/request ratio is 0.7594 and throughput 1.1828, both improvements;
all collected HTTP metrics have no stable regression. Five independent exact
counter pairs (`digest-counters`) put media at 44,296,215 → 15,883,193 B/request,
ratio 0.3586 [0.3583, 0.3593]; history is 9,495,230 → 8,980,501 B/request,
ratio 0.9458 [0.9445, 0.9459].

Twelve CPU/alloc/inuse and difference graphs are in
`digest-profiles/profiles/index.html`. The media digest allocation subtree changes
from an estimated 28,235,895 to 4,097 B/request. CPU profiling locates digest work
at 5.3125 → 1.875 ms/request and GC at 9.375 → 5.859 ms/request; diagnostic
profiling is separate from the above timed measurements.

Exact compatibility tests include long HTML/Unicode payloads, nested nodes,
duplicate and escaped keys, invalid UTF-8, exact integers and exponents.
Frozen-candidate protocol/server race passed (2.569 / 159.839 s), including
authenticated/persisted/encrypted gateway recovery and preparation release.

### Compact field views: rejected

`fields-micro` compares ten alternating 50,000-iteration rounds against the
matched `digest-fields-fixture` parent. Four-key objects improve, but eight-key
owned/borrowed reads regress 6.28% / 8.41%, and nine-key map promotion increases
allocation. The candidate passed protocol, builtin and selected server tests;
correctness alone does not qualify it for adoption.

A revised candidate (`fields-lookup`, ten rounds of 100,000 iterations) avoids
copying field values during lookup and reduces map reservation on promotion.
Nine-key allocation improves from 1304 to 1048 B/op (owned) and 1016 to 760
(borrowed). However, eight-key lookup still regresses 6.06% / 6.33%, with time
ratio intervals [1.0317, 1.0998] / [1.0285, 1.0896]. This revised algorithm was
measured as a distinct candidate, not substituted into the first batch.

Both source snapshots, binaries and results are retained in the evidence
directory. Neither implementation is retained in production; the scoped-read
benchmark is retained for future alternatives. No HTTP or race acceptance is
claimed for a candidate already rejected by its targeted measurement.

Remaining candidates and final acceptance are still in progress. These results
do not establish production-switch acceptance.
