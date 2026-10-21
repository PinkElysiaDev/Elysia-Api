# Algorithmic backend follow-up (#91): final results

## Outcome and source identities

The strongest result is removal of repeated-prefix work in collected streams: final cross-stream cumulative allocation **-47.9%**, CPU **-24.6%**, P50 **-17.5%** in the integrated ABBA. Large-document indexing and bounded metric identity reuse show conditional helper gains; long local-rule runs now have an incremental path. **This series does not establish universal CPU, latency or peak-memory improvement.** Image2 timing changes direction with sample size, burst CPU regresses, and item-3 default-path controls remain noisy/unfavorable.

| Item | Signed implementation / PR | Accepted scope |
| --- | --- | --- |
| 1 | `281a3d7`, [#92](https://github.com/PinkElysiaDev/Elysia-Api/pull/92) | Deferred collected-text encoding, parent completion counters, stable ordinal frontier |
| 2 | `de9f314`, [#93](https://github.com/PinkElysiaDev/Elysia-Api/pull/93) | Invocation-local large-span boundary index and immutable storage identity cache keys |
| 3 | `4fc5011`, [#94](https://github.com/PinkElysiaDev/Elysia-Api/pull/94) | Numeric fixed/mutable request summaries for sufficiently long unconditional local-rule runs |

Baseline: merged `deploy` **`bdb02c61ec955cf83421e4fefc0e9be53b4c022c`**, including #85/#87/#88/#89. #90 was still open when pinned and on the final status check; its preparation/response-lifetime implementation is **not** part of this comparison. The baseline is therefore not the previous #86 fully integrated four-item branch.

Accepted integrated source: **`4fc5011216ca11ebe490f60419a7e222fc222273`**. Clean signed-source rebuild exactly matches the measured/profiled binary:

- Baseline SHA256: `6ca3caa5788010e8b6860a2ef2a6ccdb00ca9a7ce36c28b0e7317cd5ba01c52b`
- Integrated SHA256: `58c3f47f35cad2fcc4c23581eebdf8204e1d388c078b64393a6c1288f6e198d7`

All three implementations were accepted/scoped individually, signed with `TR0MX <ytang7543@gmail.com>` / `F8C0FF15D6227656C0748FD47A8E1E265270109C`, verified locally, and verified by GitHub. This final report changes documentation/evidence only. No automatic merges.

## Measurement and correctness

- Linux/amd64, Go 1.27.2, Ryzen 7 8845H, Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Builds, tests and measurements were run serially.
- Per-item targeted race tests and size-scaling helpers; **110,366 indexed-container** and **93,007 request-partition** fuzz executions. JSON/numeric/escape spelling, duplicate-key semantics, ownership, budget/error parity, terminal/partial snapshots, continuation and candidate behavior were checked. No full test or full benchmark suites.
- Primary five-case matrices have 396 counted requests/version plus warm-ups. The accepted item-3 matrix adds history256k for 492 total. Final five-case ABBA uses 64 requests × three repeats × two processes/version (images four), 1,560 counted requests/version.
- Client/gateway/provider run separately. Full content, response, usage and provider-call acceptance is checked. CPU and cumulative allocations include usage settlement; client latency does not. Tables report repeat medians and sampled peak maxima. No profiler or forced GC in normal HTTP timing.
- Separate final profiles: **40 raw files** (eight CPU and 32 heap/allocation), four selected scenarios/version. Each CPU profile contains **13.78–16.99 accumulated CPU sample seconds**. Profile windows are for attribution, not replacements for CPU/request measurements.
- Fresh image31 uses 31 MiB of input base64 text, single concurrency, one request × three repeats/transport plus warm-ups. This is a small separate control, not a capacity guarantee.
- Wall times fluctuated markedly even in unchanged/ineligible controls. ABBA mitigates ordering bias but is not a significance test. Every rejected trial, failing test-fixture log and conflicting dataset remains archived.

Per-item details: [1](backend-performance-algorithm-91-item-1.md) · [2](backend-performance-algorithm-91-item-2.md) · [3](backend-performance-algorithm-91-item-3.md).

## Integrated five-case ABBA

| Case | CPU ms/request | KiB/request | allocations/request | P50 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 7.391 → 6.650 (-10.0%) | 142.5 → 140.5 (-1.4%) | 2643 → 2639 | 5.70 → 5.27 | 15.99 → 15.29 | 68.05 → 67.04 |
| candidates4 | 11.289 → 9.878 (-12.5%) | 255.2 → 254.7 (-0.2%) | 5119 → 5110 | 9.39 → 8.89 | 15.50 → 14.82 | 69.74 → 69.07 |
| burst4 | 2.784 → 2.902 (+4.2%) | 141.1 → 143.8 (+1.9%) | 2635 → 2643 | 2.63 → 2.57 | 15.46 → 16.38 | 69.46 → 68.91 |
| stream256-cross | 186.567 → 140.740 (-24.6%) | 20478.2 → 10666.8 (-47.9%) | 133034 → 132217 | 144.81 → 119.50 | 10.77 → 10.79 | 68.75 → 66.37 |
| image2 | 114.287 → 124.218 (+8.7%) | 21846.9 → 21847.9 (+0.0%) | 4837 → 4892 | 97.20 → 101.27 | 33.71 → 37.57 | 93.07 → 94.30 |

## Fresh image31 single-concurrency comparison

| Transport | CPU ms/request | P50 ms | MiB/request | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| JSON | 906.04 → 593.63 (-34.5%) | 856.79 → 560.98 | 321.62 → 321.38 | 449.80 → 449.81 | 510.94 → 509.16 |
| SSE | 723.28 → 609.59 (-15.7%) | 719.54 → 663.42 | 321.75 → 321.83 | 450.08 → 418.98 | 510.87 → 503.29 |

Image31 JSON peak heap is essentially unchanged; SSE peak heap **-6.9%**. RSS is **-0.3% / -1.5%**. Cumulative allocations remain about 321 MiB/request, so this is mostly less repeated computation, not elimination of the large request's copies. These figures cannot be compared directly with #86's different baseline or used to claim resolution of all previous peak-memory regressions.

## Image2 confirmation: larger ABBA reverses the CPU direction

The main image2 comparison showed CPU **+8.7%**, P50 **+4.2%**, peak heap **+11.4%**. A dedicated confirmation kept the exact same binaries and increased image requests to **16 × five repeats × two processes/version**, 160 counted requests/version:

| Metric | Baseline → integrated |
| --- | ---: |
| CPU ms/request | 127.474 → 115.714 (**-9.2%**) |
| P50 ms | 121.806 → 110.131 (**-9.6%**) |
| B/request | 22,364,225 → 22,348,199 (**-0.1%**) |
| allocations/request | 4,824 → 4,877 (**+1.1%**) |
| sampled peak heap MiB | 37.40 → 39.49 (**+5.6%**) |
| sampled peak RSS MiB | 96.17 → 98.70 (**+2.6%**) |

This does not erase the short-run regression or prove a stable image2 CPU percentage. The additional index allocation cost and sampled peak increase remain. Burst4 CPU **+4.2%** and cumulative allocation **+1.9%** also remain in the main matrix; no all-scenario win is claimed.

## What changed algorithmically

### 1. Avoid constructing every accumulated text prefix

`response_collector.go` now appends deltas and only materializes text at real observation boundaries. For F fixed-size delta frames and a bounded number of observations, the repeated encoding component changes from Θ(F²) bytes to Θ(F). For 2048 × 256-byte deltas, the identical full-collector fixture's median cumulative allocation falls **1,217,722,144 → 6,703,156 B/op (-99.45%)**; time **755.92 → 21.09 ms/op**. Public partial snapshots still own their results; terminal comparison and snapshot-prefix verification are retained.

`EventReplay` maintains open-child counts rather than scanning all historical items on each message completion. A monotonic ordinal prefix frontier replaces repeated scans of prior messages, preserving the rule that an earlier open message can make a continuation ordinal unstable. Together, these remove avoidable quadratic bookkeeping. Logprob array accumulation is not migrated in this series. Consumers requesting a full snapshot on every delta still pay for that actual output volume.

### 2. Index large subtrees once and avoid hashing cached payloads

`value_index.go` builds a lazy invocation-local boundary index, bounded to 4096 large spans and a 256-container stack. A single sequential scan indexes borrowed subtrees; subsequent nested reads can skip their bytes. Exhaustion discards the optimization, not the request. Public detached reads and owned mutable maps/slices are unchanged. The 4 MiB/eight-level/four-traversal helper improves **1.416 → 0.101 ms**, adding **568 B / six allocations** for indexing.

`semantic_cache.go` keys facts by a typed storage pointer and extent. `unsafe.StringData` is identity-only: no mutation, dereference, uintptr conversion or pointer arithmetic; the typed pointer keeps storage alive. Equal bytes in different allocations safely miss. For 12 cached values, a 4 MiB lookup falls **60,333 → 24.82 ns**. The original four-value experiment was already approximately constant-time due to Go's small-map optimization, and is retained rather than presented as a gain.

This is not a global document arena, exact backing-allocation accounting or automatic lifetime-based detachment. Logical fragment accounting remains distinct from actual retained storage/RSS. Building an index for a shallow one-off read can cost more than direct scanning.

### 3. Use incremental summaries only where setup can amortize

`conversion_partition.go` separates Tools/Parameters/ClientOutput from the untouched normalized request. A fixed summary contains only byte/token/depth counts. Every eligible rule still executes, checks current limits and normalizes the mutable partition. Other mutations and authored JSON boundaries invalidate it; failure paths retain the full oracle.

The first broad trial's history CPU regressed **+9.0%**, and a generated-summary refinement still regressed **+17.9%**. Neither was accepted. The final gate requires an unconditional contiguous run of at least four known-local rules and a sufficiently large request. A 1024-node/16-rule full-conversion ABBA improves **28.11 → 13.47 ms**, allocations **77,981 → 45,245 (-42.0%)**. Setup, cloning and initial phase checks are included. The one-rule control keeps the direct path but wall time regresses **31.7%** in that experiment with effectively unchanged allocation count.

The accepted version's longer history256k control is **67.16 → 71.84 ms/request (+7.0%)**, allocation count effectively unchanged (**33,523 → 33,529**). Adjacent cross-stream CPU also regresses **+20.4%** in its item-specific ABBA, despite the integrated baseline/final comparison improving. These datasets measure different comparisons and remain explicit. Item 3 is a conditional algorithmic improvement, not proof of a default-policy HTTP win.

## Final profiles and remaining work

The fixed-size cross-stream allocation profile totals fall from about **636.83 → 337.33 MiB**; normal unprofiled per-request measurements establish the reported allocation reduction. In the final profile, object reading, string detachment, usage merging and conversion allocations are now prominent; `readObjectWithin` accounts for 18.1% cumulative allocation in that sample. CPU still includes syscall work (12.0% flat), object scanning (8.4% cumulative) and normalization (6.5% cumulative).

Image31 still spends substantial CPU in `indexbytebody` (**35.2%**), `countbody` (**16.6%**) and UTF-8 validation (**11.5% cumulative**). Indexing avoids some repeated structural scans, not every required string/resource/schema scan. Cumulative profile shares overlap; they are not summed into a predicted speedup.

The next evidence-driven targets are incremental logprob collection, bounded frame-field reuse and usage normalization. General dirty-subtree trees, candidate copy-on-write and lifetime-based region partitioning remain design work, not implemented features. A larger ownership model should first prove that saved traversal outweighs metadata retention and setup on real rule distributions.

## Cancellation and source verification

Each hash-verified binary ran five independent batches of four 2 MiB streaming requests. Clients waited for an actual event before cancelling. All batches passed cancellation/drain checks. After the harness's diagnostic reclamation, heap ranges were **3.611–3.619 MiB baseline**, **3.624–3.626 MiB integrated**. This is short forced-GC diagnosis, not natural idle reclamation, prolonged leak freedom or a normal timing measurement.

The accepted source was rebuilt cleanly after signing; its binary SHA256 exactly matches formal HTTP/profile acceptance. All raw profiles, commands, tests, intermediate unaccepted source patches and measurements are in [the archive](artifacts/performance-algorithm-91/); [machine summary](artifacts/performance-algorithm-91/summary.json).

## Reproduction

```sh
node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,candidates4,burst4,stream256-cross,image2,history256k \
  --requests=32 --repeats=3 --idle-ms=1000 --output=NEW_EMPTY_DIRECTORY

node docs/artifacts/performance-algorithm-91/paired-runner.mjs \
  .tmp-dev/perf-algorithm/baseline/report.json \
  .tmp-dev/perf-algorithm/item-3-accepted/report.json \
  NEW_EMPTY_DIRECTORY chat-short,candidates4,burst4,stream256-cross,image2 64 3

# Final optional argument overrides the usual four image requests per repeat.
node docs/artifacts/performance-algorithm-91/paired-runner.mjs \
  .tmp-dev/perf-algorithm/baseline/report.json \
  .tmp-dev/perf-algorithm/item-3-accepted/report.json \
  NEW_EMPTY_DIRECTORY image2 64 5 16

node docs/artifacts/performance-algorithm-91/final-profiles/runner.mjs \
  .tmp-dev/perf-algorithm/baseline/report.json \
  .tmp-dev/perf-algorithm/item-3-accepted/report.json NEW_EMPTY_DIRECTORY

node docs/artifacts/performance-algorithm-91/verify.mjs
go tool pprof -http=127.0.0.1:0 \
  docs/artifacts/performance-algorithm-91/final-profiles/after-cpu-stream256-cross/cpu.pprof
```

Runners require local matching binaries recorded in input reports. Profiles embed symbols. Harness `issue` labels are inherited from the original collector; this archive and reports track #91. No production-workload weighting, frontend improvement, all-platform release acceptance or universal performance claim is derived from these backend fixtures.
