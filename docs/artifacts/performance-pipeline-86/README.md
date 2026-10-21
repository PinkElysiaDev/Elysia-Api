# Pipeline ownership and traversal (#86)

Fresh baseline: merged `deploy` at `8099841`, including #82–#84. #85 contains the preceding final profile report, not this series' baseline measurements.

Each stage contains original source patches, binary hashes, selected HTTP measurements and command logs. JSON measurement/paired reports are gzip-compressed byte-preservingly; the root manifest verifies the migrated archive. Original harness manifests describe the original local uncompressed layout. Binaries stay in `.tmp-dev/perf-pipeline/`.

Run `node docs/artifacts/performance-pipeline-86/verify.mjs`. ABBA runs use the archived runner against hash-verified binaries. Per-item reports preserve normal-matrix and interleaved results, including regressions. Timings are observations, not significance-tested project-wide gains.
