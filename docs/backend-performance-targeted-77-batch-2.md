# Profiling-driven optimization — batch 2

Tracking: [#77](https://github.com/PinkElysiaDev/Elysia-Api/issues/77). Depends on [PR #78](https://github.com/PinkElysiaDev/Elysia-Api/pull/78).

## Changes

- Boundary-local immutable readers share large payload fragments during final wire validation and continuation preprocessing. Mutable containers remain independent and small fields remain detached; public Value readers still copy.
- Optional object-aware wire validator/decoder interfaces share the current frame's root object across final shape checks, cross-frame checks, and typed decoding. Generic modules retain the previous path; final lifecycle replay and typed/JSON normalization remain.
- Read-only JSON string/length access avoids materializing byte copies for length checks and disabled body capture; framing uses the codec's immutable string output.
- Regression tests check shared payload ownership, independent maps, exact numbers, immutable string access, parsed-validator parity, and required-array rejection.

## Targeted validation

Passed; no full suite or full benchmark run:

```sh
GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off -race -p=1 -count=1 -timeout=6m \
  ./protocol ./protocol/builtin ./server \
  -run '^Test(ValueImmutableString|RequestViews|RequestResources|WireViews|WireObjectValidation|ContinuationExtraction|FinalWireValidationTypedParity|WireStreamLifecycle|WireContractEscapedRoles|FinalWirePolicyCannotErase|ChatFinalWireRejects|TypedStreamConversionAndEncodingParity|BuiltinStreamExtensionsStayNative|GatewaySSEAndNDJSON|GatewayBrokenStream|GatewayStreamWorkspace|GatewayChatWireContractFailure|GatewayCanceledRequest|StreamCancellationUsage)'

node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,responses-short,tools32,candidates4,history256k,burst4,stream256,stream256-cross,image2 \
  --requests=16 --repeats=3 --idle-ms=1000 --output=NEW_EMPTY_DIRECTORY
```

## Regression investigation: paired existing binaries

The first sequential matrix showed large CPU/latency fluctuations, including apparent regressions. Those observations remain below rather than being overwritten. Before signing, the affected paths were rerun in ABBA order with hash-verified batch-1/batch-2 binaries. A short investigation (three repeats per process) and a longer confirmation (64 requests × five repeats per process, two processes per version/case) are archived separately.

| Confirmatory case | KiB/request before → after | allocs/request before → after | CPU ms/request before → after | response P50 ms before → after |
| --- | ---: | ---: | ---: | ---: |
| tools32 | 620.7 → 608.4 | 10261 → 10259 | 22.19 → 23.01 (+3.7%) | 22.94 → 23.60 (+2.9%) |
| stream256-cross | 22870.2 → 22208.0 | 153606 → 143995 | 199.33 → 194.11 (-2.6%) | 154.52 → 148.92 (-3.6%) |

The short ABBA image2 run measured 102825.8 → 67965.2 KiB/request, CPU 286.75 → 277.77 ms, P50 222.02 → 181.89 ms, and maximum sampled RSS 121.43 → 91.52 MiB. The initial large cross-stream and tools timing regressions did not reproduce at their original magnitude with larger samples. The modest tools32 CPU/latency increase is retained as a trade-off; this batch targets allocation and large-payload memory and does not claim a tools32 speedup or statistical significance.

Evidence and reusable runner: [short paired run](artifacts/performance-targeted-77/batch-2-paired/summary.json), [longer confirmation](artifacts/performance-targeted-77/batch-2-confirmed/summary.json). Their full run commands, environments, hashes, per-request observations and timelines are in `report.json.gz`; stdout/stderr logs are in the adjacent `run-logs.txt`. Reproduction from the repository root, with matching binaries still present at the paths in the two reports:

```sh
node docs/artifacts/performance-targeted-77/batch-2-confirmed/runner.mjs \
  .tmp-dev/perf-targeted/batch-1/report.json .tmp-dev/perf-targeted/batch-2/report.json \
  NEW_EMPTY_DIRECTORY tools32,stream256-cross 64 5
```

## Incremental results: batch-1 → batch-2

| Case | KiB/request | allocation change | allocs/request | CPU ms/request | response P50 ms | sampled peak heap MiB | sampled peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 144.7 → 147.1 | +1.7% | 2856 → 2852 | 7.94 → 4.28 | 6.20 → 2.30 | 15.65 → 15.95 | 66.78 → 67.72 |
| responses-short | 167.8 → 168.3 | +0.3% | 3270 → 3265 | 9.26 → 9.86 | 6.79 → 6.91 | 16.20 → 15.86 | 67.84 → 69.69 |
| tools32 | 608.4 → 616.0 | +1.3% | 10258 → 10263 | 19.39 → 28.04 | 12.78 → 25.06 | 13.31 → 16.92 | 67.12 → 68.04 |
| candidates4 | 254.8 → 256.5 | +0.7% | 5675 → 5682 | 13.41 → 12.71 | 9.99 → 9.94 | 14.92 → 15.20 | 67.57 → 68.16 |
| history256k | 12293.0 → 11255.1 | -8.4% | 34423 → 34423 | 61.72 → 53.62 | 26.72 → 19.84 | 14.76 → 13.69 | 69.44 → 67.62 |
| burst4 | 150.2 → 144.6 | -3.7% | 2863 → 2855 | 5.94 → 4.12 | 6.63 → 4.22 | 15.84 → 15.34 | 68.46 → 68.59 |
| stream256 | 8602.6 → 8036.5 | -6.6% | 109706 → 100126 | 102.91 → 106.89 | 68.50 → 80.94 | 9.35 → 9.18 | 61.97 → 62.59 |
| stream256-cross | 22826.3 → 22342.2 | -2.1% | 153589 → 144028 | 122.86 → 198.98 | 49.05 → 147.73 | 10.28 → 10.81 | 65.50 → 66.35 |
| image2 | 102930.3 → 67968.6 | -34.0% | 5231 → 5204 | 239.06 → 314.29 | 206.27 → 251.99 | 63.98 → 36.99 | 121.26 → 93.85 |

Medians of three repeats; sampled peaks are maxima across repeats. Sixteen requests/repeat, except image2 is fixed at four. Four warm-up requests. The HTTP harness validates complete responses, usage, preserved request content, and upstream call counts. Allocation/CPU accounting includes asynchronous usage settlement; HTTP latency does not. Formal measurements do not enable profiling or force GC.

Go 1.27.2, Linux/amd64, Sonic build, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Runs are sequential, not randomized A/B; CPU/latency changes are observations, not significance-tested speedups. Small-sample tail percentiles are not production P99. Sampled heap includes garbage and RSS is not live heap. No blanket peak-memory improvement is claimed.

Evidence: [before](artifacts/performance-targeted-77/batch-1/report.json), [after](artifacts/performance-targeted-77/batch-2/report.json). Measurements record their parent commit and dirty source patch; the subsequent signed implementation commit identifies the reviewed source. New regression test source is in that commit. Binary SHA256, commands, runtime/environment, per-request measurements, timelines, and original harness manifests are retained; binaries remain local. `archive-manifest.json` covers the migrated files; original manifests retain historical paths and do not cover the additional targeted test log.
