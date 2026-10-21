# Algorithm follow-up #91 — item 3: bounded incremental request partitions

Depends on #93 (`de9f314`). This is a deliberately narrow trial of incremental computation, not a general persistent semantic tree or candidate copy-on-write implementation.

## Algorithm and selected boundary

At compilation, identify unconditional contiguous runs of at least **four** built-in `stream_options`, `responses_include`, `responses_storage`, or `gemini_safety_settings` rules. Their mutable partition is Tools/Parameters/ClientOutput; all other root fields belong to the fixed partition. Only already-normalized owned requests with at least 64 content nodes or a large native document opt in. A generated walk measures the fixed partition once. Every selected rule still executes and meters/normalizes the mutable partition against its current limits. Facts contain only numeric counts/depth, never authorization verdicts or payload references.

Any other action or JSON execution boundary invalidates the summary. Exact failure counts/precedence use the established full check. Root-field metadata is derived from the current model plan rather than a second hand-maintained field list. Failed/unsupported summary setup falls back safely. Fixed-schema separators, omission, repeated occurrences and nesting remain accounted for.

For V unchanged history nodes and R eligible local rules, boundary normalization/metering changes from repeated O(RV) traversal to one O(V) summary plus the actual mutable-part work per rule. Initial cloning, phase validation, rule effects and final encoding still have their own costs. Arbitrary JSON mappings and rules that change history do not use this optimization.

## Rejected wider trials and restricted acceptance

The first rollout attempted summary setup even for one local action. The first history256k ABBA showed CPU **66.04 → 72.01 ms (+9.0%)** despite allocations **33,530 → 31,988**. Switching initial summary creation to the generated meter still showed **57.94 → 68.31 ms (+17.9%)**. These versions are archived under `item-3/` and `item-3-final/` with source patches and all measurements; neither is the accepted source.

The accepted version restricts setup to amortizable unconditional runs of four or more rules. The standard short/default runs take the established path. The history256k allocation difference disappears as expected (**33,523 → 33,529**, effectively flat). Its longer ABBA (128 requests × five repeats × two processes/version) still shows CPU **67.16 → 71.84 ms (+7.0%)**, P50 **58.39 → 64.02 ms (+9.6%)**. There is **no claim of a default-history HTTP performance gain or proven elimination of timing regression**.

Accepted 1024-node conversion helper, including cloning, initial checks and every rule, hash-recorded ABBA with three 500 ms repeats/process:

| Rules | Median ns/op before → after | allocations/op before → after |
| --- | ---: | ---: |
| 1 (direct-path control) | 9,467,460 → 12,469,866 | 47,139 → 47,139 |
| 16 (incremental path) | 28,109,762 → 13,472,008 | 77,981 → 45,245 |

The 16-rule helper reduces allocation count **42.0%** and observed time **52.1%**. The one-rule control fluctuates/regresses **31.7%** despite unchanged allocation count; wall-time results are not significance-tested. Earlier full 64/256/1024-node × 1/4/16-rule runs (including unfavorable times) are also retained. Acceptance is for scoped algorithmic work/allocation reduction and parity, not universal HTTP speedup.

## Correctness and selected HTTP checks

- Targeted protocol/builtin/server race tests cover conversion/budget/parity, request projection, native prepared encoding, thinking/candidate isolation and encrypted reasoning continuation. New partition tests compare every rule against the full-walk oracle, all exported fixed fields, mutable omission/normalization and tighter byte/node/depth budgets.
- Compile selection tests cover short runs and conditional barriers. Exact diagnostics and encoded documents are compared across selected actions and a content-mutating invalidation barrier.
- Differential partition fuzz: **93,007 executions**, ten seconds/two workers. The final selection/generator changes then passed focused race reruns. Initial test-only invalid rule ordering and invalid `onUnsupported:warn` fixtures were corrected to valid contracts; their failing logs remain archived.
- Final six-case primary HTTP acceptance (492 counted requests plus warm-ups), common five-case ABBA and dedicated history comparison all pass response/content/usage/provider-call checks. The common five-case table below reports 396 requests/version; history is separately reported above.
- Final adjacent ABBA has cross-stream CPU **+20.4%**, image2 **+5.2%**, Chat **-5.3%**. These control/mostly-ineligible paths do not show a stable general timing gain. Primary results differ and are retained below.

No full suites ran. [Evidence](artifacts/performance-algorithm-91/), including all unaccepted source variants and exact binary hashes. Reproduce helper with `go test -tags=sonic -pgo=off ./protocol -run '^$' -bench '^BenchmarkAlgorithmConversion$' -benchmem -benchtime=200ms -count=3`; ABBA helpers use the same fixture overlaid on `de9f314` and the accepted source, with commands/hashes in `validation/item-3-accepted-paired-micro-identities.log`.

## Selected HTTP comparison: item-2 → item-3-accepted

| Case | CPU ms/request | KiB/request | allocations/request | P50 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 8.40 → 5.16 | 139.5 → 141.1 | 2641 → 2649 | 5.85 → 3.16 | 15.50 → 15.82 | 66.91 → 67.87 |
| candidates4 | 12.03 → 12.50 | 254.4 → 249.1 | 5106 → 5114 | 9.26 → 9.71 | 13.74 → 15.07 | 67.74 → 68.88 |
| burst4 | 2.63 → 5.07 | 139.5 → 145.4 | 2638 → 2648 | 2.15 → 4.86 | 14.26 → 14.77 | 68.21 → 68.63 |
| stream256-cross | 139.48 → 132.24 | 10682.7 → 10669.6 | 132214 → 132188 | 108.97 → 104.60 | 10.02 → 10.39 | 64.24 → 65.66 |
| image2 | 159.67 → 119.10 | 21795.7 → 21794.1 | 4898 → 4893 | 129.04 → 112.75 | 37.57 → 37.70 | 94.16 → 94.00 |

32 requests × three repeats; images use four. Five cases, 396 counted requests/version plus warm-ups. Go 1.27.2/Sonic/PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Isolated client/gateway/provider; CPU/allocation include usage settlement, response latency does not. Medians across repeat measurements; peaks are sampled maxima. No profiler/forced GC in formal timing. No significance or project-wide speedup claim.

## Hash-verified ABBA

64 requests × three repeats × two processes/version (images four).

| Case | CPU ms/request | B/request | allocations/request | P50 ms |
| --- | ---: | ---: | ---: | ---: |
| chat-short | 8.569 → 8.117 | 143650 → 144670 | 2644 → 2640 | 6.070 → 6.004 |
| stream256-cross | 111.481 → 134.168 | 10911108 → 10899418 | 132200 → 132194 | 67.100 → 109.536 |
| image2 | 131.001 → 137.798 | 22388281 → 22363628 | 4899 → 4894 | 105.153 → 111.519 |
