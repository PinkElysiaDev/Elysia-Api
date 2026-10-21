# Algorithmic follow-up (#91)

Baseline: merged `deploy` at `bdb02c6`, including #85/#87/#88/#89. #90 was not merged when this baseline was pinned and is not silently included in this series.

Per-item reports retain selected HTTP comparisons, size-scaling helpers, race logs, exact source patches and binary hashes. Gzip measurements preserve the original bytes; original harness manifests describe the uncompressed local layout. Binaries remain under `.tmp-dev/perf-algorithm/`. Run `node docs/artifacts/performance-algorithm-91/verify.mjs` to verify archive integrity.

Helpers measure algorithmic growth and specific owners, not universal gateway throughput. No full test/benchmark suites, weakened validation or automatic merges. CPU, cumulative allocation, retained heap and sampled peaks are distinct outcomes.
