# Allocation and continuation optimization evidence

Completed on Windows amd64 with local deterministic providers. Accepted changes
reduce unnecessary continuation recovery, reuse authenticated history digests,
hash large bodies through fragments, omit unused Native capture scopes, and
generate semantic cloning/normalization. A late escaped-leaf regression was
measured, fixed and revalidated. Compact field views and exact Core capacities
were rejected after stable microbenchmark regressions; their production changes
are absent. DSL / Spec / Mapping and the production preset selection are unchanged.

Relative to P0 package execution, corrected final history CPU/request falls
53.50% and allocation 65.55%; media CPU falls 25.59% and allocation 64.14%.
Same-protocol SSE allocation falls 4.61%, with inconclusive CPU timing. After one
history confirmation, standalone timings and exact allocation metrics have no
stable P0 regression. Historical CPU and allocation gaps remain: this is **not
production-switch acceptance**. Full tables, uncertainty and rejected candidates
are retained below.

- [Interactive flamegraph index](../.tmp-dev/allocation-optimization-2026-11-04/index.html): 304 figures, including intermediate/rejected-correction evidence.
- [Corrected final CPU/alloc/inuse differences](../.tmp-dev/allocation-optimization-2026-11-04/accepted-profiles/profiles/index.html).
- [Matched absolute medians](../.tmp-dev/allocation-optimization-2026-11-04/accepted-confirmed-medians.json) and [ratios/95% intervals](../.tmp-dev/allocation-optimization-2026-11-04/accepted-confirmed-comparisons.json).
- [Execution and build identity manifest](../.tmp-dev/allocation-optimization-2026-11-04/identity-manifest.json).

The linked local evidence directory is intentionally excluded from Git. This
document and the reusable measurement tools are committed; retain the directory
to reproduce the exact archived binaries, profiles and measurements.

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
oracle. Candidate results and completed acceptance are recorded below.

## Results

The stage notes below retain the order of experiments; later acceptance sections
supersede their then-pending measurements.

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
authenticated recovery. Dedicated recovery profiling and HTTP supplements are
reported in the completed supplement and final acceptance sections below.

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
performance. The subsequent matched acceptance batches are reported below.

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
remain inconclusive. Independent exact counters and profile follow-ups are
reported below; the micro gains are not treated as HTTP allocation or latency
gains.

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
substituted for HTTP timings. Completed exact allocation and profile follow-ups
are recorded in the supplement and final sections below.

Protocol/builtin suites pass (2.112 / 19.990 s), covering populated and empty
roots against the old implementation, key collisions, absent Values and private
host fields. Frozen targeted protocol race passes (12.106 s). Held/cancelled
retention results above use this same frozen candidate.

### Final correctness and structural counters

The frozen final implementation is `c0c2c249-source-6bc731d23cf5`; its complete
backend source hash matches `normalize`. The final Go full suite, vet,
staticcheck v0.8.2, relevant protocol/builtin/server race, measurement and audit
self-tests, both generator checks and source inventory check pass. Full server
tests include `TestDefaultPackagesGatewayMatrix` (16 JSON/SSE directions).
Commands, elapsed times and individual logs are in `final-checks`.

Separate test-only attribution overlays in `final-attribution` confirm that
64-message history changes from 33 history calculations and 1120 node digests
to one history calculation and 64 node digests. The original total consists of
1088 prefix nodes plus 32 recovery-node probes; the no-carrier fixture has no
authenticated restoration or response signature to count. This distinction is
preserved rather than calling all 1120 operations prefix hashing.

For the 256-frame SSE fixture, Native child scopes change from 2855 to 260 per
request. The 2595 skipped scopes comprise 1551 iteration and 1044 filter scopes;
all 260 consumers requiring capture retain their scopes. The diagnostics are
compiled only into frozen test overlays; their timing and allocation totals
are excluded from acceptance.

The following supplements and corrected final acceptance close the stage
measurements. Historical-version performance still blocks a production switch.

### Authenticated recovery and semantic allocation supplements

All supplements use five alternating pairs, separate standalone timing and
exact-counter batches, fully consumed responses and verified restored upstream
signatures. `recovery-http` / `recovery-counters` compare P0 with frozen final:

| Scene | CPU ms/request, before → after | Throughput ratio [95% CI] | Bytes/request, before → after |
| --- | ---: | ---: | ---: |
| Authenticated carrier | 23.3765 → 11.5967 | 1.9002 [1.8861, 1.9224] | 27,409,289 → 9,549,179 |
| Persistent session | 26.5503 → 13.6108 | 1.7162 [1.7029, 1.7369] | 27,457,986 → 9,596,131 |

CPU ratios are 0.49608 [0.46835, 0.50653] and 0.51264 [0.50575, 0.52204].
Allocation ratios are 0.34839 [0.34824, 0.34849] and 0.34948 [0.34943, 0.34956].
No collected standalone metric has a stable regression.

`digest-recovery-http` / `digest-recovery-counters` independently compare
`history-local` with `digest-select` using a real carrier and the full 2 MiB
media body. CPU/request changes 57.7393 → 36.1328 ms, ratio 0.62579 [0.60888,
0.64271]; throughput improves 29.29%. Exact allocation changes 63,013,148 →
22,452,614 B/request, ratio 0.35632 [0.35595, 0.35689]. Thus the buffering
improvement also applies during authenticated recovery, independently of
skipping recovery when no carrier/session exists. No standalone metric regresses.

`semantic-counters` rotates `native`, `clone` and `normalize` in five rounds.
Clone-only SSE bytes remain inconclusive (15,293,858 → 15,292,150 B/request),
while objects improve slightly (210,199.14 → 210,171.59). Generated normalization
then reduces SSE bytes to 15,219,167, ratio 0.99523 [0.99510, 0.99548], and objects
to 205,909.80. Short-request normalization changes 263,439 → 262,206 B/request,
ratio 0.99532 [0.98421, 0.99729]. These smaller independent effects are not
presented as the main allocation reduction. Counter-build timing is excluded
from standalone timing conclusions.

The added candidate benchmark runs ten alternating 100-iteration rounds with
one/four attempted dispatch parents, with/without a valid carrier. It always
clones candidate inputs and excludes transport time. Each authenticated attempt
must report its restored node and unchanged parent. Four-carrier candidates
change 27.3159 → 0.3429 ms/op, 70,788,444 → 832,225 B/op and 79,570.5 → 4,182
objects/op. All four shapes improve without stable regression. The P0 overlay
adapts only the old method signature and eager outgoing-parent calculation;
both frozen implementations and the common benchmark hash are recorded under
`{p0,final}-candidate-fixture` and `candidate-micro`.

The strengthened history-sharing regression separately rejects changed owner,
provider scope and restored-node content after a successful cached preparation.
It passes with the related history tests under race. Production source is
unchanged by this supplemental fixture.

### Expanded-leaf regression found during final acceptance

The first complete same-window matrix (`final-http`, 280 standalone runs;
`final-counters`, 240 exact-counter runs) completed with verified outputs and
stable identities. It is retained as **intermediate evidence**, not final
acceptance: package history showed a higher uncollected one-second heap boundary,
and SSE first-frame latency showed a 26.7 µs regression. Both require resolution;
neither is waived by a five-percent threshold. History GC counts fell from about
128 to 45 per batch; boundary heap is not automatically retained memory.

The separate ten-round large escaped-leaf benchmark then found a real digest
regression that ordinary base64 media did not expose: 9.7711 → 12.6418 ms/op,
ratio 1.29379 [1.28343, 1.30186]. Its unchanged ~25.2 MB/op reflects the exceptional
leaf's codec buffers, unlike ordinary media's eliminated whole-Node buffers.
`escaped-profile` CPU samples identify rescanning the expanded normalized string
(`utf8.ValidString` contributes 25.23% cumulatively in the candidate sample).
These profiles locate the issue; fixed-iteration timings establish the regression.

Candidate `digest-leaves` retains the normalized leaf identities already produced
by the private digest projection. Once projection finishes, only its normalized
output is consumed; original lookup keys can be retired without increasing cache
capacity or retaining a request. The digest cache has no typed-meter facts.
Inline/map entries stay under the existing budget; uncached values retain the
existing behavior. Mixed repeated/escaped leaves are checked against the original
digest oracle, with input ownership preserved.

Ten alternating large-payload rounds compare the original implementation,
the preceding final candidate and this correction in one batch. Escaped-leaf
time changes 12.7533 → 10.1815 ms against the preceding candidate, ratio 0.79834
[0.79412, 0.80173]. Against the original implementation it changes 10.3101 →
10.1815 ms, ratio 0.98753 [0.98208, 0.99018]. Ordinary media keeps 3440 B/op and
has inconclusive timing change. Four small-node shapes (ten ×100000 iterations)
also have no stable regression. Exact exceptional-leaf allocations remain about
25.2 MB/op; no large allocation saving is claimed for those leaves.

Protocol/builtin suites and targeted digest race pass. Five standalone HTTP pairs
for short/history/media (`digest-leaves-http`) have no stable regression. History
CPU ratio is 0.98529 [0.93627, 0.99510]; ordinary-media timings are noisy and
inconclusive, with no claimed gain. The correction is retained for its definite
escaped-leaf benefit. The corrected final comparison follows below. The
earlier 520-run matrix and its anomalies remain archived; it is not relabelled as
the corrected binary's result. Browser smoke passes all newly rendered profile
groups, including hover, pivot, difference graphs and held/cancelled heaps.

## Corrected final same-window comparison

Frozen final: `8a019137-source-68f96a100690`. Official/rebuilt 1.7.0 are
`9b72829a`; A is `b2844846 / dev.101`, **not 1.7.0**. All versions use
isolated state and the same deterministic upstream/driver. `accepted-http`
contains 280 standalone runs and `accepted-counters` contains 240 separate
exact-counter runs. The one permitted history confirmation adds 35 standalone
runs for all seven conditions. History timing uses all ten observations per
condition; other timing and all counters use five. Old percentages are not
spliced into these tables. Ratios use seeded bootstrap independent medians.

### Standalone CPU milliseconds per completed request

| Scene | 1.7.0 release | 1.7.0 rebuild | A | P0 default | Final default | P0 package | Final package |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Short | 0.6142 | 0.6142 | 0.6256 | 0.5722 | 0.6065 | 0.9766 | 0.9766 |
| 256 KiB history | 7.0190 | 7.0190 | 6.9275 | 7.0190 | 6.3782 | 20.9351 | 9.7351 |
| 32 tools | 2.1057 | 2.1210 | 2.0905 | 2.0752 | 1.9989 | 3.4485 | 3.2959 |
| Four concurrent | 0.5035 | 0.5074 | 0.5150 | 0.5074 | 0.4959 | 0.8774 | 0.8583 |
| 256-frame SSE | 12.6953 | 11.8408 | 13.0615 | 13.3057 | 12.2070 | 37.9639 | 36.4990 |
| 2 MiB media | 20.2637 | 19.1650 | 18.9209 | 19.5312 | 19.5312 | 36.2549 | 26.9775 |
| Chat → Anthropic JSON | 0.6409 | 0.6409 | 0.6714 | 0.7629 | 0.7324 | 0.9460 | 0.8240 |
| Chat → Anthropic SSE | 16.1133 | 16.2354 | 16.4795 | 15.7471 | 15.2588 | 36.2549 | 35.1562 |

### Exact cumulative allocation bytes per completed request

These are rebuilt test-collector binaries. The official release does not have
exact allocation counters; no exact release number is inferred from pprof.

| Scene | 1.7.0 rebuild | A | P0 default | Final default | P0 package | Final package |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Short | 158,140 | 161,085 | 151,680 | 147,293 | 272,010 | 263,752 |
| 256 KiB history | 9,916,614 | 9,922,589 | 9,910,175 | 9,823,776 | 25,919,153 | 8,929,067 |
| 32 tools | 582,567 | 585,856 | 569,746 | 525,036 | 1,537,476 | 1,495,940 |
| Four concurrent | 158,990 | 161,883 | 152,954 | 148,118 | 270,009 | 263,090 |
| 256-frame SSE | 6,939,905 | 6,930,995 | 7,010,542 | 6,812,822 | 15,958,373 | 15,221,901 |
| 2 MiB media | 18,471,095 | 18,466,440 | 18,433,931 | 18,425,969 | 44,358,506 | 15,908,867 |
| Chat → Anthropic JSON | 167,129 | 169,799 | 153,372 | 155,638 | 223,873 | 219,518 |
| Chat → Anthropic SSE | 9,523,547 | 9,612,562 | 9,327,580 | 9,079,578 | 15,096,468 | 15,025,550 |

### P0 package → final package ratios with 95% intervals

`*` means inconclusive. Lower CPU/allocation is better; higher throughput is better.

| Scene | CPU/request | Throughput | Bytes/request | Objects/request |
| --- | --- | --- | --- | --- |
| Short | 1.0000 [0.9453, 1.0562] * | 1.0149 [1.0080, 1.0325] | 0.9696 [0.9693, 0.9796] | 0.9639 [0.9637, 0.9675] |
| 256 KiB history | 0.4650 [0.4558, 0.4751] | 1.6242 [1.6148, 1.6473] | 0.3445 [0.3444, 0.3446] | 0.6820 [0.6820, 0.6821] |
| 32 tools | 0.9558 [0.9038, 1.0088] * | 1.0315 [1.0265, 1.0420] | 0.9730 [0.9720, 0.9731] | 0.9179 [0.9175, 0.9179] |
| Four concurrent | 0.9783 [0.9654, 1.0391] * | 1.0159 [0.9729, 1.0305] * | 0.9744 [0.9690, 0.9802] | 0.9655 [0.9636, 0.9679] |
| 256-frame SSE | 0.9614 [0.9421, 1.0193] * | 1.0320 [0.9752, 1.0609] * | 0.9539 [0.9536, 0.9541] | 0.9676 [0.9676, 0.9676] |
| 2 MiB media | 0.7441 [0.5692, 0.9057] | 1.2575 [0.9156, 1.6403] * | 0.3586 [0.3579, 0.3593] | 0.9539 [0.9538, 0.9541] |
| Chat → Anthropic JSON | 0.8710 [0.8065, 0.9667] | 1.0064 [0.9896, 1.0205] * | 0.9805 [0.9369, 1.0351] * | 0.9538 [0.9350, 0.9762] |
| Chat → Anthropic SSE | 0.9697 [0.9365, 0.9832] | 1.0199 [1.0163, 1.0309] | 0.9953 [0.9951, 0.9957] | 0.9763 [0.9762, 0.9764] |

### Remaining historical gap

| Scene | CPU vs release 1.7.0 | CPU vs A | Allocation vs rebuilt 1.7.0 | Allocation vs A |
| --- | --- | --- | --- | --- |
| Short | 1.5901 [1.5031, 1.8264] | 1.5610 [1.4756, 1.7297] | 1.6678 [1.6568, 1.6790] | 1.6374 [1.6247, 1.6464] |
| 256 KiB history | 1.3870 [1.3171, 1.4174] | 1.4053 [1.2903, 1.4727] | 0.9004 [0.8996, 0.9010] | 0.8999 [0.8994, 0.9006] |
| 32 tools | 1.5652 [1.5105, 1.6522] | 1.5766 [1.5105, 1.7231] | 2.5678 [2.5656, 2.5698] | 2.5534 [2.5491, 2.5556] |
| Four concurrent | 1.7045 [1.6304, 1.8106] | 1.6667 [1.6187, 1.7704] | 1.6548 [1.6457, 1.6646] | 1.6252 [1.6102, 1.6415] |
| 256-frame SSE | 2.8750 [2.7642, 3.0481] | 2.7944 [2.5556, 3.1386] | 2.1934 [2.1881, 2.1981] | 2.1962 [2.1945, 2.1977] |
| 2 MiB media | 1.3313 [1.2701, 1.6205] | 1.4258 [1.3457, 1.7355] | 0.8613 [0.8582, 0.8639] | 0.8615 [0.8590, 0.8663] |
| Chat → Anthropic JSON | 1.2857 [1.0833, 1.5882] | 1.2273 [0.8182, 1.4500] * | 1.3135 [1.2562, 1.3375] | 1.2928 [1.2330, 1.3547] |
| Chat → Anthropic SSE | 2.1818 [1.9384, 2.2677] | 2.1333 [1.9790, 2.2677] | 1.5777 [1.5757, 1.5805] | 1.5631 [1.5609, 1.5658] |

### Latency, throughput and memory evidence

Full absolute medians for all seven conditions, including throughput, p50/p95,
first-frame, sampled peak working set and boundary heap, are in
`accepted-confirmed-medians.json`. Every ratio/interval is retained in
`accepted-confirmed-comparisons.json`; raw records retain p99, GC counts,
recovery observations and upstream calls. Timing does not force GC.

No standalone CPU, throughput or latency metric has a stable P0 → final
regression for either default or package execution. History initially showed
a high one-second uncollected heap boundary; the single confirmation was
merged with all original observations. Its ten-pair ratio is 1.12567
[0.80334, 1.57377], inconclusive. The original five-pair finding is preserved.
Exact allocation bytes/objects have no stable regression. Counter-build SSE
first-frame differs by 0.8 µs, ratio 1.00052 [1.00007, 1.00104]; it is retained
as a collector-build observation and is not substituted for standalone timing.

Each SSE response has 256 content frames and 260 observed wire frames. Final
same-protocol allocation is 59460.55 B/content-frame (58545.77 B/wire-frame), 804.27
objects/content-frame. Cross-protocol allocation is 58693.56 B/content-frame
(57790.58 B/wire-frame), 693.96 objects/content-frame.

### Final profile attribution and retention

CPU and memory profiles use separate fixed-workload batches. Allocation samples
subtract the settled pre-batch profile and are normalized by completed requests;
inuse profiles remain whole-process retained bytes. Categories are disjoint by
stack classification, but are still sampling estimates, not exact counters.
Profiler allocations are a separate category. A missing sampled stack is not
proof of zero allocations. Raw stacks and top-80 contributing stacks are saved
in each group's `attribution.json` and `*.raw.txt`.

| Stack category and scenario | P0 sampled B/request | Final sampled B/request | P0 → final sampled objects/request |
| --- | ---: | ---: | ---: |
| History: continuation digests | 17,044,481 | 484,972 | 19,919.6 → 652.2 |
| Media: continuation digests | 28,412,754 | 4,096 | 11.1 → 51.2 |
| SSE: Native child scopes | 659,615 | 102,425 | 3,056.6 → 400.1 |
| Short: semantic ownership | 7,297 | 3,073 | 125.1 → 18.1 |
| Carrier recovery: continuation digests | 19,320,245 | 1,460,727 | 20,709.6 → 2,171.4 |
| Session recovery: continuation digests | 19,370,067 | 1,368,269 | 21,284.7 → 2,137.8 |

The large media byte reduction can coexist with more sampled small objects:
projection replaces a few very large buffers with small fields and fragments.
Do not treat estimated object counts as diagnostic call counts. Independent
exact probes establish 1120 → 64 node-digest calls on the no-carrier history
fixture and 2855 → 260 Native child calls on SSE. Authenticated content rechecks
and response capture remain present. Those probes precede the last private
normalization-cache correction; that correction changes neither call boundary.

Final history CPU sampling locates continuation at 0.1953 ms/request versus
3.9453 at P0, and GC at 2.6563 versus 7.5391. Media continuation samples change
2.8125 → 1.2500 ms and GC 9.4531 → 4.6094. These explain the timed gains; their
sample totals and percentages are not used to calculate the formal speedup.

Remaining final allocations have the following absolute scale:

| Scenario / category | Sampled B/request | Sampled objects/request | Representative path |
| --- | ---: | ---: | --- |
| SSE field tables | 2,790,016 | 17,261.2 | `planObjectReader.readFields → readObjectFieldsWithin` |
| SSE operand evaluation | 1,925,278 | 34,127.9 | `buildNode → buildOperandValues` |
| SSE owned fragments | 1,576,780 | 12,597.7 | `valueReader.fragment → strings.Clone` |
| SSE stream state | 1,188,594 | 10,548.2 | `packageStreamTransaction` / index / range paths |
| Tools field tables | 595,084 | 3,790.7 | scoped raw-object reads |
| Short Core projections | 55,185 | 571.4 | generated `readCore* → finishCoreOutput` |

The unchanged call-boundary probes count 7,828 field tables, 3,958 operand
evaluations and 5,811 detached fragments per SSE request. Its 578 Core containers
reserve 3,690 fields for 1,981 used fields. These counts establish investigation
scope, not permission to remove ownership, resource, reference or delivery checks.

Media's remaining large stacks include `ReadBoundedBody → io.ReadAll` (~4.81 MB),
`Value.Bytes → forwardGateway` (~2.13 MB), `ParseValue → prepareGatewayPlan`
(~2.13 MB), and three materialization/encoding stacks (~2.08–2.13 MB each).
Public copying contracts and transport/log lifetimes still apply; their presence
does not make them automatically redundant.

Corrected-final held/cancelled diagnostics repeat five batches of four 2 MiB
streams with a real isolated master key. Preparation admission/body reservations
are zero during delivery; active requests and queued/retained log bytes are zero
after cancellation. GC-observed held heaps are P0
56.832/56.862/56.862/56.862/56.863 MiB and final
56.827/56.856/56.861/56.861/56.861 MiB. Cancelled heaps settle near 48.59 MiB in
both. No additional retained body or accumulating state is observed.

The separately drained history inuse samples are 53.70 MiB at P0 and about
45.13 MiB at final. Sampling variance and startup compilation prevent treating
that difference as a measured retained-memory saving. Compiled expressions and
output plans remain the largest retained categories; their footprint is not
charged as per-request allocation growth. The exact held diagnostics, rather
than a single sampled heap total, support the lifetime conclusion.

### Follow-up boundaries supported by the remaining profiles

These are future candidates, not unfinished implementations from this round.
Each needs its own parent comparison, at least ten micro rounds and five HTTP
pairs, with the same no-stable-regression gate.

1. **Avoid repeated field-table construction before changing its representation.**
   Investigate the 7,828 SSE table constructions within existing reader scopes.
   Keep the rejected eight-key compact lookup evidence; a new attempt must cover
   four/eight/nine keys, escaped duplicate keys, final-value lookup semantics and
   ownership release. Do not enlarge the root/subobject cache.
2. **Reduce operand and projection work for immediate consumers.** Investigate
   the 1.93 MB SSE operand subtree and 55 KB short Core projection subtree.
   Preserve eager evaluation and error/cancellation order. The exact-capacity
   generator trials failed selector timing; an alternative must isolate both
   full-object construction and single-field selection before HTTP adoption.
3. **Separate required owned wire bytes from redundant materializations.** Follow
   the media encoding stacks and SSE fragment/state stacks through their actual
   holders. Keep public `Value.Bytes`, Native provenance and delivery commits
   unchanged. Reject any alternative that pins large input bodies or increases
   cancellation/slow-consumer retained heap.
4. **Keep escaped-leaf codec buffers and compiled graph residency separate.**
   Escaped digest payloads still allocate ~25.2 MB/op. Further normalization work
   must retain exact Unicode/HTML behavior and digest compatibility. Retained
   compiler graphs require separate cold compile/reload/old-snapshot evidence;
   they cannot explain the same-request allocation ratios above.

### Final verification, identities and reproduction

At production commit `8a019137`, all final checks pass: Go full suite (184.313 s
wall time), vet, staticcheck v0.8.2, protocol/builtin/server race (6.562 / 16.313 /
158.109 s), measurement and audit self-tests, Core/semantic-meter generator
checks and source inventory. The server suite includes the 16-direction JSON/SSE
matrix and continuation/Native/reference/delivery regressions. The new mixed-leaf
digest parity and history authentication checks also pass. No timed/profile
batch overlaps a build, correctness test or browser run.

All 304 generated figure files are indexed. Browser smoke checks every saved
file's presence and renders one representative of each sample/difference kind
per group, including hover details and pivot interaction. It does not claim
manual inspection of every flamegraph.

Final identities:

```text
production commit: 8a019137
frozen identity: 8a019137-source-68f96a100690
backend source SHA-256: 68f96a100690d96d11054c68ba3b3b7f75322108a651f9f1ac849d958b0f95dd
standalone binary SHA-256: 863fd51f06a97cec99e461e71aeff8fadc0e4eb2953083bd4442934a8d045dbc
counter binary SHA-256: 2fafbb182a04f9816c265596d692557358f973306c74b717110149a5984371da
```

`identity-manifest.json` records official/rebuilt/A and both frozen builds,
build flags/toolchains, 587 completed primary/confirmation/profile runs, per-run
config and active-revision hashes, upstream/driver/runner hashes and commands.
Recovery fixtures and held overlays have separate manifests. Configs, databases,
logs and master keys are confined to the isolated evidence directories.

Run from the repository root; use a fresh label because the runner refuses to
overwrite evidence. Run the commands sequentially, outside other test/build
activity. The frozen archives must remain available.

```powershell
python -B scripts/allocation-optimization.py matrix --stages p0,final-v2 --label reproduce-http
python -B scripts/allocation-optimization.py matrix --stages p0,final-v2 --label reproduce-counters --phase counters
python -B scripts/allocation-optimization.py http --stages p0,final-v2 --label reproduce-profiles/standalone-cpu --scenes short,history,tools,stream,image,cross-stream --mode cpu --rounds 1
python -B scripts/allocation-optimization.py http --stages p0,final-v2 --label reproduce-profiles/standalone-memory --scenes short,history,tools,stream,image,cross-stream --mode memory --rounds 1
python -B scripts/allocation-optimization.py render --stages p0,final-v2 --label reproduce-profiles
python -B scripts/allocation-recovery.py --stages p0,final-v2 --driver recovery-driver-media --label reproduce-recovery --scenes carrier,session
node scripts/allocation-flame-smoke.mjs .tmp-dev/allocation-optimization-2026-11-04/reproduce-profiles/profiles
```

This is Windows amd64/local-provider acceptance. It does not establish cross-
platform runtime, full Agent/WebUI performance, real-provider latency, or a
general memory bound from five held-stream batches. References/Native/slow-write
correctness remains covered by the suites; those combinations are not all
separate end-to-end throughput scenarios. Candidate retry microbenchmarks exclude
transport time. No Swift, existing user database/log, public configuration,
resource limit, GC setting, provider-specific Go branch or production preset was
changed. Nothing was pushed or published.

Implementation is split into local commits `2934b62e`, `0d15fd4a`, `83643bcc`,
`4920040d`, `620879c5`, `c0c2c249` and `8a019137`; measurement/rejection evidence
is in `542a351e`, `7e9ae257`, `5508fb5b`, `f1a11c70`, `6e28b571`, `fe30b669`
and `b9965e53`. The final report is committed separately. No production switch
is authorized by these results: the remaining 1.7.0/A CPU and allocation gaps
are explicit in the historical comparison table.
