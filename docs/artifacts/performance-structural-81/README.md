# Structural performance follow-up (#81)

Tracking: [issue #81](https://github.com/PinkElysiaDev/Elysia-Api/issues/81), following merged #78–#80. Baseline: upstream `deploy` at `1ddf7af`.

[Final integrated report](../../backend-performance-structural-81-final.md) · [machine-readable summary](summary.json). Requested items 1 → 2 → 4 are merged as #82 → #83 → #84. Integrated signed source is `053edf5`; the `8099841` merge has the same backend tree. This archive does not retroactively turn earlier baselines into measurements of later commits.

- [Item 1: nested output construction](../../backend-performance-structural-81-item-1.md)
- [Item 2: immutable request reuse](../../backend-performance-structural-81-item-2.md)
- [Item 4: preparation admission](../../backend-performance-structural-81-item-4.md)
- [Policy documentation](../../backend-preparation-admission.md)
- `item-4/`: default-policy normal-path acceptance after item 2.
- `item-4-memory/`, `item-4-default-memory/`: same-binary off/one-slot and off/default-eight-slot concurrency/memory attribution, including all counter/rejection/recovery checks.
- `item-2/`: incremental accepted HTTP results after item 1.
- `baseline/`, `item-1-final/`: selected HTTP acceptance, original reports/commands/source patches and binary hashes.
- `item-1-confirmed/`: initial prototype timing investigation, explicitly not the accepted implementation.
- `item-1-final-confirmed/`: accepted implementation ABBA confirmation and raw command logs.
- `validation/`: targeted race/fuzz/helper benchmark logs. No full suite/benchmark is claimed.
- `final-profiles/`: four selected workloads per version, 40 raw profiles (eight CPU and 32 heap/allocation), fresh single-concurrency 31 MiB JSON/SSE measurements, exact runner/commands and compressed raw metrics. All CPU profiles contain more than ten accumulated CPU sample seconds.
- `cancellation-before/`, `cancellation-after/`: five 2 MiB × four-stream cancellation/recovery batches per version; forced recovery is not a natural-reclamation or soak-test claim.

`measurements.json.gz` is a byte-preserving gzip copy of the original JSON. Stage `compressed-measurements.json` lists its original path and SHA256. Original harness manifests are preserved but refer to the original uncompressed layout; the root `archive-manifest.json` verifies the migrated layout and the verifier also checks decompressed measurement hashes. Paired reports are gzip-compressed with adjacent summaries and logs. Matching test binaries remain in `.tmp-dev/perf-structural/` and are not committed.

```sh
node docs/artifacts/performance-structural-81/verify.mjs
gzip -dc docs/artifacts/performance-structural-81/item-1-final/measure-image2/measurements.json.gz
```

Runs use Go 1.27.2/Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB, Linux/amd64 Ryzen 7 8845H. Figures are not significance-tested or production P99; sampled peaks can miss spikes. Source patches identify pre-commit measurements; the subsequent signed commit identifies reviewed code. All fixtures are synthetic, with no production providers, credentials or databases.

The integrated clean signed-source rebuild matches the accepted/profiled binary SHA256. Raw pprof/gzip files are marked binary to prevent line-ending corruption. Full backend tests/benchmarks and all protocol combinations were not run. Strong allocation reductions coexist with timing/peak-memory regressions; see the final report rather than treating lower B/request as a universal RSS improvement.
