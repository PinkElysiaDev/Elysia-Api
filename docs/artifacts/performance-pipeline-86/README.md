# Pipeline ownership and traversal (#86)

Fresh baseline: merged `deploy` at `8099841`, including #82–#84. #85 contains the preceding final profile report, not this series' baseline measurements.

## Reports and scope

- [Final integrated report](../../backend-performance-pipeline-86-final.md), [machine summary](summary.json).
- [Item 1](../../backend-performance-pipeline-86-item-1.md), [item 2](../../backend-performance-pipeline-86-item-2.md), [item 3](../../backend-performance-pipeline-86-item-3.md), [item 4](../../backend-performance-pipeline-86-item-4.md).
- Implementation PRs: #87 → #88 → #89 → #90; final measured source `89673ff`, clean signed rebuild matches the archived binary hash.
- `final-profiles/`: 40 profiles (eight CPU, 32 heap/allocation), all CPU windows >10 accumulated sample seconds; separate fresh single-concurrency image31 JSON/SSE measurements.
- `final-paired/`: baseline/final five-case ABBA; `final-burst-confirmation/`: longer 1,024-request × five-repeat burst diagnosis. Conflicting results are retained.
- `item-4-held/`: isolated four-stream retained-memory experiment. Forced GC here is diagnostic, not normal-path timing, peak memory or natural reclamation.
- `validation/`: targeted regression/fuzz/helper logs, including the initial stale recovery-context failure and its passing correction.

The results do not prove overall project-wide speedup. Final Chat/cross-stream CPU and image31 peak heap/RSS regressions remain explicit in the report.

Each stage contains original source patches, binary hashes, selected HTTP measurements and command logs. JSON measurement/paired reports are gzip-compressed byte-preservingly; the root manifest verifies the migrated archive. Original harness manifests describe the original local uncompressed layout. Binaries stay in `.tmp-dev/perf-pipeline/`.

Run `node docs/artifacts/performance-pipeline-86/verify.mjs`. ABBA runs use the archived runner against hash-verified binaries. Per-item reports preserve normal-matrix and interleaved results, including regressions. Timings are observations, not significance-tested project-wide gains.
