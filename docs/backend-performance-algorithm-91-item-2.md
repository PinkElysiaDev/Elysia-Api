# Algorithm follow-up #91 — item 2: bounded span indexes and metric identities

Depends on #92 (`281a3d7`). A large-document reader builds a bounded structural end index lazily, in one sequential scan. It records only spans large enough to borrow under the existing ownership policy, with at most 4096 entries and a 256-container stack; exhaustion falls back to scanning without changing resource verdicts. Subsequent reads of borrowed descendants skip their payload bytes. Public reads still detach children, maps/slices remain caller-owned, duplicate/escaped keys retain last-key-wins behavior. The index belongs to one adapter invocation and is initialized safely under concurrent reads.

Metric caches now use immutable storage identity (typed `*byte` plus length) rather than content as map keys. `unsafe.StringData` is used only to obtain a typed identity pointer: no mutation, dereference, uintptr conversion or pointer arithmetic. The pointer keeps its storage alive, preventing address recycling while cached. Independently owned equal values safely miss. Public `Value` representation/equality is unchanged. Cache accounting still limits logical retained fragments, not exact backing allocations or RSS; this does not implement a global arena or cross-request index.

## Scaling evidence and limits

Same helper source is overlaid onto baseline `bdb02c6`; affected reader/cache production code is identical to item 1. Three 300 ms repeats; medians below. Timings fluctuate.

| 8-level document, four traversals including index setup | Before ns/op | After ns/op |
| --- | ---: | ---: |
| 64 KiB leaf | 120,639 | 23,215 |
| 1 MiB leaf | 509,980 | 89,605 |
| 4 MiB leaf | 1,416,429 | 100,724 |

Allocation cost: **11,434 → 12,002 B/op**, **128 → 134 allocs/op**. The index saves repeated payload traversal, not caller-owned container allocations; initial scanning remains linear in input bytes. A one-off shallow read need not benefit.

| Metric-cache lookup, 12 distinct values | Before ns/op | After ns/op |
| --- | ---: | ---: |
| 64 KiB value | 848.4 | 22.88 |
| 1 MiB value | 15,977 | 27.20 |
| 4 MiB value | 60,333 | 24.82 |

Lookup-only, setup excluded; both zero allocations. **The first four-value experiment did not show size-dependent lookup cost**: Go's small-map path was already about 13–16 ns/op. That original log is retained (`item-2-before-small-map.log`); the 12-value experiment specifically crosses the small-map boundary. These are conditional gains, not a claim that all cache hits previously hashed the full payload.

## Acceptance

- Targeted race in protocol/builtin/server: indexed reads, large views/detachment, metric budget parity/concurrent contexts, native replay, typed wire schemas and HTTP prepared encoding. The initial test compilation failed on a nonexistent public `ReadArray`; corrected to the existing `readArray` oracle, then protocol rerun passed. The builtin/server run had already passed. Both logs retained.
- `FuzzIndexedContainerParity`, ten seconds/two workers: **110,366 executions**, including duplicate keys, exact numeric spelling, escaped strings and nested containers.
- Deep-index fallback, storage extent distinction, GC between identity uses and concurrent reader initialization have explicit tests.
- Five selected HTTP cases and hash-verified ABBA passed content/usage/provider-call acceptance. Image2 ABBA CPU **-27.5%**, allocation count **+1.0%**, sampled peak heap **+5.1%**. Primary candidate CPU/P50 regress materially and Chat/cross-stream timing changes direction across datasets. All results are retained, with no significance or universal speedup claim.

Reproduce helpers with `go test -tags=sonic -pgo=off ./protocol -run '^$' -bench '^BenchmarkAlgorithm(DocumentIndex|MetricIdentity)$' -benchmem -benchtime=300ms -count=3`. Fuzz/race commands and output are in [the archive](artifacts/performance-algorithm-91/validation/). No full test/benchmark suite.

## Selected HTTP comparison: item-1 → item-2

| Case | CPU ms/request | KiB/request | allocations/request | P50 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 8.02 → 8.40 | 140.7 → 139.5 | 2650 → 2641 | 5.91 → 5.85 | 16.02 → 15.50 | 67.48 → 66.91 |
| candidates4 | 7.52 → 12.03 | 251.3 → 254.4 | 5111 → 5106 | 4.59 → 9.26 | 14.51 → 13.74 | 67.77 → 67.74 |
| burst4 | 4.73 → 2.63 | 139.4 → 139.5 | 2638 → 2638 | 4.39 → 2.15 | 15.15 → 14.26 | 67.99 → 68.21 |
| stream256-cross | 133.90 → 139.48 | 10634.4 → 10682.7 | 132208 → 132214 | 107.52 → 108.97 | 10.19 → 10.02 | 64.79 → 64.24 |
| image2 | 176.51 → 159.67 | 21832.5 → 21795.7 | 4841 → 4898 | 143.93 → 129.04 | 37.35 → 37.57 | 92.64 → 94.16 |

32 requests × three repeats; images use four. Five cases, 396 counted requests/version plus warm-ups. Go 1.27.2/Sonic/PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Isolated client/gateway/provider; CPU/allocation include usage settlement, response latency does not. Medians across repeat measurements; peaks are sampled maxima. No profiler/forced GC in formal timing. No significance or project-wide speedup claim.

## Hash-verified ABBA

64 requests × three repeats × two processes/version (images four).

| Case | CPU ms/request | B/request | allocations/request | P50 ms |
| --- | ---: | ---: | ---: | ---: |
| chat-short | 5.837 → 5.402 | 143684 → 147474 | 2642 → 2643 | 4.503 → 3.981 |
| stream256-cross | 148.346 → 137.053 | 10914140 → 10957061 | 132210 → 132205 | 124.993 → 106.001 |
| image2 | 157.083 → 113.956 | 22420332 → 22330707 | 4846 → 4892 | 133.335 → 90.812 |
