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

### Exact Core projection capacity: rejected

The generator trial `projection` computes presence once and reserves only live
fields. Ten 100,000-iteration rounds reduce sparse Node allocation 776 to 152
B/op and dense allocation 5256 to 3608 B/op. Whole-object execution improves,
but the unchanged single-payload selector regresses 2.89% [2.28%, 3.65%].

Separating full projection into a helper (`projection-split`) retains the large
allocation benefit. Ten 200,000-iteration rounds plus the one permitted matched
confirmation batch still show a selector regression: 26.880 to 27.185 ns/op,
ratio 1.01135 [1.00760, 1.01489]. A distinct pointer-argument helper candidate
(`projection-pointer`) removes the extra whole-struct argument copy, but its
selector regresses 2.98% [2.49%, 4.06%]. All candidate source and measurements
remain archived. These small stable regressions are not waived at a 5% cutoff.

The production generator and generated code are restored to the parent. Dense/
sparse parity tests and micro fixtures remain; protocol and builtin correctness
passed. HTTP/profile/race acceptance was not run for these rejected candidates.
Future projection work must also isolate selector call costs. No projection
allocation saving is included in the accepted implementation's results.

The measurement comparator now merges a micro confirmation batch without
discarding its initial observations, frozen builds record `go version -m`, and
diagnostic call-count overlays accept a frozen stage identity instead of a
hardcoded current commit. These are measurement-only changes.

### Skip unused Native iteration scopes

Frozen `native` (`5508fb5b-source-9042d1fbdd37`) is compared with the accepted
`digest-select` production parent; `digest-native-fixture` overlays only the
matching micro fixtures. Capture requirements include source capture,
`is_carrier`, lexical bindings, input scopes and the active recursive walk body.
The recursion flag occupies existing plan padding; no new cache is introduced.

Ten alternating 10,000-iteration rounds (`native-micro`) reduce a 32-item plain
read from 6906.5 to 3716.5 ns/op, 13264 to 4304 B/op and 137 to 41 allocs/op.
Disabled Native preservation similarly improves. Required source/Native
capture keeps its original allocation and has no stable timing regression.

Five standalone pairs for short/tools/SSE/cross-protocol SSE (`native-http`)
show no stable regression; their timing differences remain inconclusive. Five
separate exact-counter pairs (`native-counters`) show short allocation
269224.71 to 265204.94 B/request, ratio 0.98507 [0.97984, 0.98619]. SSE changes
15955246.13 to 15290197.13 B/request, ratio 0.95832 [0.95827, 0.95841], and
212795.40 to 210199.30 objects/request. This is a definite allocation improvement,
not a claim of established HTTP latency improvement.

Twelve CPU/alloc/inuse and difference flamegraphs are indexed at
`native-profiles/profiles/index.html`. Protocol/builtin suites passed (4.242 /
20.380 s), and frozen-candidate targeted protocol race passed (6.307 s).
The added indirect-recursion regression checks exact nested wire positions
with Native disabled and source capture enabled.

### Supplemental recovery measurement contract

`allocation-recovery.py` reuses the same application lifecycle, metrics proxy,
driver consumption and profile collection as the primary runner. Its separate
Go provider requires every follow-up to restore the signed Gemini text, all
63 earlier messages and roles, and (for the media variant) the complete 2 MiB
payload. Client responses must preserve text, usage, termination and the newly
issued carrier. No production endpoint or per-request diagnostic was added.

The `recovery-driver-media` overlay is built against frozen `native` production
with two test fixtures only. Its SHA-256 is
`78f3d4ee975db099458e625e4e196105bdc79e4656bfe1003b46762817c2d3bd`.
The carrier, persistent-session and media-carrier smoke runs each pass one
initial request plus 32 warmups and 4 consumed follow-ups: 37 upstream calls,
36 verified recoveries. These small smoke runs validate the fixture, not
performance. Matched acceptance batches are still required.

The runner also supports a same-window rotating matrix containing release
1.7.0, rebuilt 1.7.0, A, and both default/package paths of the frozen starting
and final states. Previous percentages must not substitute for that batch.

### Generated semantic cloning

The post-Native SSE profile still samples approximately 184360 B/request in
semantic ownership stacks. This is a secondary cost, not the main allocation
growth source. Frozen `clone` (`5508fb5b-source-0e8edde2ee01`) changes cloning
alone; normalization still uses the preceding implementation in this snapshot.
The generator shares the closed Core field catalogue, preserves private host
receipts by whole-struct assignment and shares immutable Value storage. Value
roots remain addressable for the existing normalization boundary. Recursive
reflection cloning is retained only as a test oracle.

Ten 10000-iteration rounds (`clone-micro`) improve 1/32/128-node copying by
49.58% / 48.84% / 48.91%. The 128-node case changes 48107 to 24578 ns/op,
88712 to 84560 B/op and 519 to 260 objects/op. Nested 128-message history
changes 60354 to 22312 ns/op, 81176 to 78080 B/op and 259 to 130 objects/op.
Ten 100000-iteration event rounds (`clone-event-micro`) reduce the combined
copy/unchanged-normalization boundary from 671.6 to 426.8 ns/op; its 592 B/op
and six objects are unchanged. Every measured micro shape has no stable
regression.

Fifty standalone runs (`clone-http`) cover five pairs of short/history/tools/
SSE/cross-protocol SSE. None of the collected metrics shows stable regression.
Tools throughput improves 1.80%; most HTTP timing differences, including SSE,
remain inconclusive. Independent exact counters and profile follow-ups remain
part of final acceptance; the micro gains are not treated as HTTP allocation
or latency gains.

Protocol/builtin suites passed (1.830 / 20.397 s), including all-root parity
with the reflection oracle and mutable-container ownership. Frozen targeted
protocol race passed (4.110 s). Native profile browser smoke passes eight
representative kinds, including differences, hover details and interactive
pivoting; all twelve figure files are present.

### Held stream and cancellation diagnosis

`allocation-held.py` overlays only the existing test harness on frozen `p0`
and `normalize` production. Each runs five batches of four simultaneous 2 MiB
media streams. An opt-in test setting creates an isolated master-key file, so
continuation preparation is enabled. The fixed provider holds the stream until
cancellation. Admission and reserved request-body bytes must already be zero
while four streams remain active; after cancellation, active requests and
queued/retained log payloads must be zero. Both candidates pass all batches.

GC-observed held heaps (MiB) are P0 56.828/56.853/56.854/56.855/56.857 and
candidate 56.831/56.856/56.856/56.855/56.857. Cancelled heaps settle near 48.58
MiB in both. No additional body lifetime or accumulating retention is observed
in these batches. Heap and goroutine profiles are archived under
`held-retention/{p0,normalize}`. This existing isolated test gateway is a
retention diagnostic, not the standalone release startup used for formal HTTP
timings. Forced GC results are never mixed into throughput or allocation rates.

### Generated semantic normalization

Frozen `normalize` (`620879c5-source-6bc731d23cf5`) is compared with the
unchanged cloning implementation using `clone-normalize-fixture`. Typed
normalizers share the existing Value cache and Unicode rules. Invalid map keys
retain the original sorted collision order; nil, empty and omitted containers
remain distinct. Cloning and normalization are separate traversals. The original
reflection normalizer is retained only as a test oracle.

All four ten-round batches pass without stable regression. `normalize-micro`
(10000 iterations) reduces the 128-node copy/normalize boundary from 59415 to
40311.5 ns/op, 90752 to 84560 B/op and 647 to 260 objects/op. Unchanged cloning
controls remain inconclusive. `normalize-formats` (100000) improves ASCII,
Unicode, escaped and invalid-UTF-8 time by 36.80%, 36.69%, 21.83% and 33.01%,
respectively, with lower allocation. The 2 MiB `normalize-media` batch (1000)
has inconclusive timing, 1072 to 1024 B/op and eight to five objects/op.
`normalize-event` (100000) changes 438.8 to 232.35 ns/op, 592 to 528 B/op and
six to two objects/op.

Six scenarios have five standalone pairs (`normalize-http`). History initially
shows a peak-working-set regression, so exactly one five-pair history confirmation
is added (`normalize-confirm`). All ten history pairs remain in
`confirmed-comparison.json`: peak RSS median ratio 1.02738, CI [0.93781,
1.05384], inconclusive. No metric retains a stable regression in the combined
report. Several throughput gains are approximately 1–2%; micro gains are not
substituted for HTTP timings. Exact allocation and profile follow-ups are pending.

Protocol/builtin suites pass (2.112 / 19.990 s), covering populated and empty
roots against the old implementation, key collisions, absent Values and private
host fields. Frozen targeted protocol race passes (12.106 s). Held/cancelled
retention results above use this same frozen candidate.

Final acceptance and evidence collection are still in progress. These results
do not establish production-switch acceptance.
