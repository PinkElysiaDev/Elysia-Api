# Structural performance follow-up (#81)

Tracking: [issue #81](https://github.com/PinkElysiaDev/Elysia-Api/issues/81), following merged #78–#80. Baseline: upstream `deploy` at `1ddf7af`.

- [Item 1: nested output construction](../../backend-performance-structural-81-item-1.md)
- `baseline/`, `item-1-final/`: selected HTTP acceptance, original reports/commands/source patches and binary hashes.
- `item-1-confirmed/`: initial prototype timing investigation, explicitly not the accepted implementation.
- `item-1-final-confirmed/`: accepted implementation ABBA confirmation and raw command logs.
- `validation/`: targeted race/fuzz/helper benchmark logs. No full suite/benchmark is claimed.

`measurements.json.gz` is a byte-preserving gzip copy of the original JSON. Stage `compressed-measurements.json` lists its original path and SHA256. Original harness manifests are preserved but refer to the original uncompressed layout; the root `archive-manifest.json` verifies the migrated layout and the verifier also checks decompressed measurement hashes. Paired reports are gzip-compressed with adjacent summaries and logs. Matching test binaries remain in `.tmp-dev/perf-structural/` and are not committed.

```sh
node docs/artifacts/performance-structural-81/verify.mjs
gzip -dc docs/artifacts/performance-structural-81/item-1-final/measure-image2/measurements.json.gz
```

Runs use Go 1.27.2/Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB, Linux/amd64 Ryzen 7 8845H. Figures are not significance-tested or production P99; sampled peaks can miss spikes. Source patches identify pre-commit measurements; the subsequent signed commit identifies reviewed code. All fixtures are synthetic, with no production providers, credentials or databases.
