# Profiling-driven optimization — batch 1

Tracking: [#77](https://github.com/PinkElysiaDev/Elysia-Api/issues/77). This batch replaces read-only operation deep copies, prepares fixed usage/rollup SQL once per log store (with transaction-local bindings), avoids re-encoding unchanged continuation history, and allocates unknown-field maps only when extensions exist. Public mutable metadata copies, transactional idempotence, rollback, reconnect behavior, and FULL log durability remain intact.

## Targeted validation

Passed; no full suite or full benchmark run:

```sh
GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off -race -p=1 -count=1 -timeout=6m \
  ./protocol ./protocol/builtin ./storage ./server \
  -run '^Test(CollectUnknown|ContinuationExtraction|CompiledOperations|RouteChecks|VerificationBinding|LogDatabaseIsolation|LogReaders|UsageBatchRollback|UsagePrepared|SaveUsageRecordJSONDuplicate|RollupParity|GatewayFourPublic|GatewayMediaTool|GatewayMessagesGeminiContinuation|GatewayContinuationPersistence|GatewayResponsesEncryptedReasoning|UsageQueue|StopUsageWriter|ResetUsage)'

node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,responses-short,tools32,candidates4,history256k,burst4,stream256,stream256-cross,image2 \
  --requests=16 --repeats=3 --idle-ms=1000 --output=NEW_EMPTY_DIRECTORY
```

The baseline and this batch each completed 396 formally counted HTTP requests, plus independent warm-up/first-request observations. The image2 sampled peak heap/RSS increased in this run (58.41 → 63.98 / 114.91 → 121.26 MiB); allocation savings are not a claim of improved peak memory. The subsequent batches and final cancellation/recovery diagnosis will evaluate the remaining large-payload behavior. Latency/CPU changes are noisy observations rather than isolated causal attribution to one helper.

## Incremental results: baseline → batch-1

| Case | KiB/request | allocation change | allocs/request | CPU ms/request | response P50 ms | sampled peak heap MiB | sampled peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 197.1 → 144.7 | -26.6% | 3464 → 2856 | 9.60 → 7.94 | 6.53 → 6.20 | 15.05 → 15.65 | 67.20 → 66.78 |
| responses-short | 234.0 → 167.8 | -28.3% | 3883 → 3270 | 11.61 → 9.26 | 8.01 → 6.79 | 16.60 → 16.20 | 69.37 → 67.84 |
| tools32 | 721.9 → 608.4 | -15.7% | 11188 → 10258 | 21.05 → 19.39 | 22.31 → 12.78 | 16.19 → 13.31 | 67.61 → 67.12 |
| candidates4 | 414.9 → 254.8 | -38.6% | 7346 → 5675 | 15.38 → 13.41 | 11.84 → 9.99 | 15.88 → 14.92 | 69.58 → 67.57 |
| history256k | 12708.6 → 12293.0 | -3.3% | 35364 → 34423 | 69.26 → 61.72 | 52.21 → 26.72 | 15.73 → 14.76 | 69.21 → 69.44 |
| burst4 | 197.4 → 150.2 | -23.9% | 3466 → 2863 | 9.04 → 5.94 | 7.39 → 6.63 | 17.59 → 15.84 | 69.30 → 68.46 |
| stream256 | 9637.2 → 8602.6 | -10.7% | 113423 → 109706 | 135.43 → 102.91 | 99.16 → 68.50 | 9.75 → 9.35 | 63.47 → 61.97 |
| stream256-cross | 23804.9 → 22826.3 | -4.1% | 157136 → 153589 | 216.68 → 122.86 | 139.83 → 49.05 | 10.43 → 10.28 | 66.29 → 65.50 |
| image2 | 105644.1 → 102930.3 | -2.6% | 6240 → 5231 | 335.59 → 239.06 | 231.30 → 206.27 | 58.41 → 63.98 | 114.91 → 121.26 |

Medians of three repeats; sampled peaks are maxima across repeats. Sixteen requests/repeat, except image2 is fixed at four. Four warm-up requests. The HTTP harness validates complete responses, usage, preserved request content, and upstream call counts. Allocation/CPU accounting includes asynchronous usage settlement; HTTP latency does not. Formal measurements do not enable profiling or force GC.

Go 1.27.2, Linux/amd64, Sonic build, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Runs are sequential, not randomized A/B; CPU/latency changes are observations, not significance-tested speedups. Small-sample tail percentiles are not production P99. Sampled heap includes garbage and RSS is not live heap. No blanket peak-memory improvement is claimed.

Evidence: [before](artifacts/performance-targeted-77/baseline/report.json), [after](artifacts/performance-targeted-77/batch-1/report.json). Measurements record their parent commit and dirty source patch; the subsequent signed implementation commit identifies the reviewed source. New regression test source is in that commit. Binary SHA256, commands, runtime/environment, per-request measurements, timelines, and original harness manifests are retained; binaries remain local. `archive-manifest.json` covers the migrated files; original manifests retain historical paths and do not cover the additional targeted test log.
