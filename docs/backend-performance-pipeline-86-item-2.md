# Pipeline refactor #86 — item 2: generated semantic metering

Depends on #87 (`647ab51`). A deterministic generator traverses the actual exported semantic model and JSON tags, emitting typed resource walkers for closed Request/Response/Event/Node roots. It rejects unsupported field kinds, custom marshalers and tags. Generation uses a bootstrap build tag with the reflective oracle, so deleted/renamed fields in stale generated code do not block regeneration. Production success paths avoid reflection; precise failure/unsupported paths retain the established oracle. Clone/normalize walkers remain reflective and are not claimed as migrated.

## Acceptance and results

- Targeted race: `GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off -race -p=1 ./protocol ./protocol/builtin ./internal/architecture -run '^Test(GeneratedMeter|TypedMeter|SemanticCache|DocumentMetrics|RequestWireSchemaTypedParity|Resource|NativeRequestCanShrink|PackageBoundaries)' -count=1 -timeout=5m`.
- Deterministic freshness: `go run -tags=semanticmeter_bootstrap ../internal/semanticmeter --check` from `backend/protocol`, also executed by `TestGeneratedMeterCurrent`. Regenerate with `go generate -C backend ./protocol`.
- `FuzzTypedLimitEquivalence`, ten seconds/two workers: 36,262 executions. Existing independent JSON-token oracle now also compares generated/reflective bytes, nodes and depth. Filled models exercise every exported field, raw numbers/escaping, omission, nil/empty, exact budgets, cycles and malformed values.
- `BenchmarkTypedTraversal`, three 300 ms repeats: 64-message structural check median **19,377 → 8,490 ns/op (-56.2%)**, both **0 B/op / 0 allocs/op**.
- Five selected HTTP cases pass. ABBA: Chat CPU **6.83 → 6.53 ms (-4.4%)**, cross-stream **178.87 → 172.36 (-3.6%)**, burst **3.42 → 3.71 (+8.6%)**. Chat P50 also rises slightly in ABBA. The initial cross-stream matrix regresses and image peak heap/RSS increase. All observations remain below; no significance or universal speedup claim.

Evidence and exact source/binary identities are in [the archive](artifacts/performance-pipeline-86/). No full tests or benchmark suite ran.

## Incremental selected HTTP results: item-1 → item-2

| Case | KiB/request | allocations/request | CPU ms/request | P50 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 142.6 → 140.2 | 2851 → 2684 | 8.17 → 6.71 | 6.14 → 5.70 | 15.92 → 15.74 | 68.20 → 66.65 |
| candidates4 | 261.2 → 249.1 | 5693 → 5181 | 12.76 → 9.04 | 9.99 → 8.35 | 14.79 → 15.43 | 68.05 → 67.65 |
| burst4 | 142.6 → 144.9 | 2848 → 2689 | 5.68 → 6.32 | 4.87 → 6.08 | 15.54 → 16.52 | 68.52 → 69.58 |
| stream256-cross | 20823.5 → 20755.4 | 137473 → 137264 | 182.22 → 208.13 | 137.11 → 153.74 | 10.83 → 10.61 | 67.04 → 67.23 |
| image2 | 21835.3 → 21836.3 | 5092 → 4891 | 150.66 → 135.66 | 130.59 → 100.77 | 31.31 → 37.74 | 89.73 → 96.80 |

32 requests × three repeats (image: four), four warm-ups, one-second idle: 396 counted requests/version. Separate client/gateway/provider processes; response/content/usage/call counts checked. Go 1.27.2, Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. CPU includes usage settlement; latency does not. Per-repeat medians and sampled peak maxima; no significance or universal latency/RSS claim. No forced GC or profiler in formal HTTP timing.

## Hash-verified ABBA confirmation

64 requests × three repeats × two processes/version (image stays four).

| Case | CPU ms/request | P50 ms | B/request change |
| --- | ---: | ---: | ---: |
| chat-short | 6.83 → 6.53 | 5.55 → 5.66 | -2.9% |
| burst4 | 3.42 → 3.71 | 3.30 → 3.98 | -2.5% |
| stream256-cross | 178.87 → 172.36 | 131.33 → 129.89 | -0.0% |
