# Issue #77: targeted Go gateway performance evidence

[Final report](../../backend-performance-targeted-77-final.md) · [machine-readable summary](summary.json) · [issue #77](https://github.com/PinkElysiaDev/Elysia-Api/issues/77)

Baseline: `8e732c7`, backend source-identical to `5a8115e`. Integrated source: signed `fbee14a`, following `d0e7bc0` and `f603cf2`. Collected 2026-10-20/21 on the same Linux/amd64 Ryzen 7 8845H machine with Go 1.27.2, Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB.

## Contents

| Path | Contents |
| --- | --- |
| `baseline/`, `batch-1/`, `batch-2/`, `batch-3/` | Selected nine-case HTTP acceptance, raw per-request measurements/timelines, source patches, runtime/binary identities, commands, and logs |
| `baseline-profiles/`, `final-profiles/` | Independent CPU/allocation profiling for Chat, 256-frame stream and 2 MiB image |
| `baseline-legacy/`, `final-legacy/` | Fresh nodes/events/31 MiB JSON/SSE measurements and independent profiles |
| `*-paired/`, `*-confirmed/` | ABBA binary comparisons; `summary.json`, gzip-compressed original `report.json`, stdout/stderr logs, and reusable runner |
| `cancellation-before/`, `cancellation-after/` | Five independent 2 MiB × four-stream cancellation/recovery batches per version |
| `summary.json`, `cpu-samples.json` | Source/binary identities, final comparisons, diagnostic ranges and CPU sample accounting |
| `manifest.json`, `verify.mjs` | SHA256/size verification of the complete archive; no server or test binary required |

There are 60 raw pprof files (12 CPU, 48 heap/allocation); binary profiles are marked binary to prevent line-ending corruption. CPU profiles embed symbols. Matching `server.test` binaries stay local and are excluded from Git; their SHA256 values are recorded in reports. `final-profiles` is a clean signed source build and has the same binary hash as batch-3 acceptance. Original source patches and manifests are copied unchanged; parent+patch provenance for pre-commit measurements must not be mistaken for a clean parent-source measurement. New regression test source is in the corresponding signed commits.

Original harness manifests retain their historical relative/absolute paths and exclude binaries. Stage-specific archive manifests cover migration additions; the root manifest covers **all** archived files, including nested manifests and the verifier. Paired reports are byte-preserving gzip copies of the original generated JSON; logs are separately preserved in `run-logs.txt`. All fixtures are synthetic and contain no production accounts or databases.

## Verify and inspect

```sh
node docs/artifacts/performance-targeted-77/verify.mjs

go tool pprof -http=127.0.0.1:0 \
  docs/artifacts/performance-targeted-77/final-legacy/cpu-image31/cpu.pprof

go tool pprof -http=127.0.0.1:0 -sample_index=alloc_space \
  -base docs/artifacts/performance-targeted-77/final-profiles/memory-image2/alloc-before.pprof \
  docs/artifacts/performance-targeted-77/final-profiles/memory-image2/alloc-after.pprof

gzip -dc docs/artifacts/performance-targeted-77/final-paired/report.json.gz
```

See the final report for exact test filters/reproduction, per-batch links, limitations and regressions. Allocation profiles are cumulative sampled allocation, not peak memory; CPU subtrees overlap. No full suite/benchmark, universal latency/RSS improvement, 31 MiB × four-concurrency capacity, or prolonged leak-freedom claim is made.
