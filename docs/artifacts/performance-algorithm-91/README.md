# Algorithmic follow-up (#91)

Baseline: merged `deploy` at `bdb02c6`, including #85/#87/#88/#89. #90 was not merged when this baseline was pinned and is not silently included in this series.

- [Final report](../../backend-performance-algorithm-91-final.md) and [machine summary](summary.json).
- [Item 1](../../backend-performance-algorithm-91-item-1.md), [item 2](../../backend-performance-algorithm-91-item-2.md), [item 3](../../backend-performance-algorithm-91-item-3.md).
- PR stack #92 → #93 → #94; accepted source `4fc5011`. Clean signed-source rebuild matches accepted/profiled binary SHA256.
- `item-3/` and `item-3-final/` are **unaccepted wider trials**, not the final implementation. `item-3-accepted/` is the selected gated implementation. All conflicting results are retained.
- `final-paired/`: integrated five-case ABBA; `final-image-confirmation/`: larger 16-request × five-repeat image control with the same binaries.
- `final-profiles/`: 40 raw profiles, eight CPU windows each exceeding ten accumulated CPU sample seconds, plus fresh image31 JSON/SSE measurements.
- `final-cancellation/`: five batches/version; short forced-GC diagnosis, not normal timing or a soak.
- `validation/`: targeted race/fuzz and scaling logs, including corrected test-fixture failures and original small-map negative findings.

Per-item reports retain selected HTTP comparisons, size-scaling helpers, race logs, exact source patches and binary hashes. Gzip measurements preserve the original bytes; original harness manifests describe the uncompressed local layout. Binaries remain under `.tmp-dev/perf-algorithm/`. Run `node docs/artifacts/performance-algorithm-91/verify.mjs` to verify archive integrity.

Helpers measure algorithmic growth and specific owners, not universal gateway throughput. No full test/benchmark suites, weakened validation or automatic merges. CPU, cumulative allocation, retained heap and sampled peaks are distinct outcomes.
