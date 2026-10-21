# Algorithmic follow-up (#91)

Baseline: merged `deploy` at `bdb02c6`, including #85/#87/#88/#89. #90 was not merged when this baseline was pinned and is not silently included in this series.

- [Final report](../../backend-performance-algorithm-91-final.md) and [machine summary](summary.json).
- Historical item reports: [item 1](https://github.com/PinkElysiaDev/Elysia-Api/blob/0db0de2321da4aa0faa063d13b258719748691cf/docs/backend-performance-algorithm-91-item-1.md), [item 2](https://github.com/PinkElysiaDev/Elysia-Api/blob/0db0de2321da4aa0faa063d13b258719748691cf/docs/backend-performance-algorithm-91-item-2.md), [item 3](https://github.com/PinkElysiaDev/Elysia-Api/blob/0db0de2321da4aa0faa063d13b258719748691cf/docs/backend-performance-algorithm-91-item-3.md).
- PR stack #92 → #93 → #94; accepted source `4fc5011`. Clean signed-source rebuild matches accepted/profiled binary SHA256.
- `item-3/` and `item-3-final/` are **unaccepted wider trials**, not the final implementation. `item-3-accepted/` is the selected gated implementation. All conflicting results are retained.
- `final-paired/`: integrated five-case ABBA; `final-image-confirmation/`: larger 16-request × five-repeat image control with the same binaries.
- `final-profiles/`: 40 raw profiles, eight CPU windows each exceeding ten accumulated CPU sample seconds, plus fresh image31 JSON/SSE measurements.
- `final-cancellation/`: five batches/version; short forced-GC diagnosis, not normal timing or a soak.
- `validation/`: targeted race/fuzz and scaling logs, including corrected test-fixture failures and original small-map negative findings.

The latest final report is the working-tree entry point; per-item narratives and previous rounds' archives are retained in Git history. This complete #91 archive still retains selected HTTP comparisons, size-scaling helpers, race logs, exact source patches and binary hashes, including unfavorable and rejected results. Gzip measurements preserve the original bytes; original harness manifests describe the uncompressed local layout. Binaries remain under `.tmp-dev/perf-algorithm/`. Run `node docs/artifacts/performance-algorithm-91/verify.mjs` to verify archive integrity.

After synchronization, `deploy@6a16292` includes #90 and #92–#94. These historical measurements still describe `4fc5011`, without #90; they are not a measured baseline for the merged backend. No measurements or profiles were changed during cleanup; only this index and its archive checksum were updated.

Helpers measure algorithmic growth and specific owners, not universal gateway throughput. No full test/benchmark suites, weakened validation or automatic merges. CPU, cumulative allocation, retained heap and sampled peaks are distinct outcomes.
