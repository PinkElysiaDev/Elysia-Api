# Backend profiling optimizations, 2026-11-12

The five changes reduce repeated text decoding, immutable JSON scanning,
temporary field maps, and bounded HTTP body buffer growth. Each was measured
against the preceding step, then the combined implementation was compared with
source baseline `8998e38a0ff9379605f66bf289e36b926f2f26c9`.

## Changes and semantic boundaries

1. `Value.IsString("")` compares the validated JSON spelling with `""`, including
   outer whitespace, instead of decoding the entire string. Other comparisons
   retain the existing decoder.
2. `normalizeDocumentMapping` borrows already measured, identity-based facts
   proving a Value needs no normalization. The document mutex protects access;
   normalized copies stay local. Mutable Core containers are still cloned and
   every occurrence is still checked against its current resource budgets.
3. `decodeContent` reuses an already canonical ASCII JSON string. Escaped text,
   HTML characters, Unicode, invalid UTF-8 and outer whitespace keep the existing
   decode/encode normalization.
4. Chat choices and deltas read selected members into stack slots using
   `ReadObjectMembers`. This avoids two temporary maps and their key copies.
   Selected Values still own storage; duplicate decoded keys remain last-wins.
   Unknown-field validation/native preservation remains in the existing pipeline.
5. Gateway request bodies use a Content-Length allocation hint for positive
   sizes up to 8 MiB and within the active byte limit. Reads remain bounded by
   limit+1 and declared-length mismatches remain errors. Unknown/large sizes use
   the original chunked reader. No pooled large buffers or unsafe aliases are added.

## Incremental gateway measurements

Apple M5, Go 1.27.4, macOS/arm64; GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB,
PGO off. Current default built-in codec execution is called `legacy` by the
test harness; all binaries contain the current source baseline plus their
specified steps, with no archived version substituted.

Client, gateway and synthetic provider run in separate processes. Each side
uses three fresh processes, with three measured batches per process. Before/
after order alternates between processes; all measurement runs are serial,
after the full tests and race checks completed. Entries are medians of nine
batch-level observations, not nine independent processes. A batch contains
64 requests (24 for images), concurrency one, with body/media logging disabled.

| Increment | Allocated KiB/request | Allocations/request | Gateway CPU ms/request |
| --- | ---: | ---: | ---: |
| 1. Empty string predicate / stream256 | 8496.2 → 8183.2 (-3.7%) | 79628 → 77575 | 9.428 → 9.168 (-2.8%) |
| 2. Document normalization facts / image2 | 23813.5 → 23880.4 (+0.3%) | 3596 → 3588 | 15.304 → 12.676 (-17.2%) |
| 3. Canonical text / history256k | 12124.4 → 10573.9 (-12.8%) | 29150 → 28500 | 6.061 → 5.549 (-8.4%) |
| 4. Selected Chat fields / stream256 | 8319.3 → 7667.1 (-7.8%) | 77568 → 73954 | 9.260 → 8.989 (-2.9%) |
| 5. Sized bounded body / image2 | 23725.2 → 19675.9 (-17.1%) | 3584 → 3556 | 12.782 → 12.119 (-5.2%) |

Step 2 targets CPU work; its small allocation-byte increase is observed, not
claimed as an allocation improvement. Sonic pools and GC affect allocation byte
totals, so allocation counts are also reported. Small CPU changes should be
treated as directional; allocation reductions and the separate microbenchmarks
provide additional evidence.

## Combined gateway measurements

Same settings and alternating order, comparing step 0 with step 5.

| Case | Allocated KiB/request | Gateway CPU ms/request | Latency P50 ms |
| --- | ---: | ---: | ---: |
| chat-short | 132.4 → 132.5 (+0.1%) | 0.542 → 0.501 (-7.5%) | 0.488 → 0.479 |
| tools32 | 528.7 → 521.7 (-1.3%) | 1.999 → 1.974 (-1.3%) | 1.900 → 1.876 |
| history256k | 12098.7 → 10196.2 (-15.7%) | 6.062 → 5.458 (-10.0%) | 5.308 → 4.730 |
| stream256 | 8466.1 → 7772.8 (-8.2%) | 9.370 → 9.048 (-3.4%) | 6.630 → 6.433 |
| image2 | 23914.0 → 19866.2 (-16.9%) | 15.387 → 12.143 (-21.1%) | 14.368 → 11.780 |

These are local synthetic gateway workloads, not model latency or a production
throughput forecast. CPU is gateway process CPU time divided by requests.
Allocation is cumulative allocation divided by requests, not live memory/RSS.
The macOS harness does not sample RSS. This work does not claim a leak fix.

## Microbenchmarks

Five samples for each before/after comparison; same harness, runtime settings,
and no PGO. Each step's baseline includes the preceding retained changes.

| Increment and benchmark | Median ns/op before → after | B/op before → after | allocs/op before → after |
| --- | ---: | ---: | ---: |
| 1. StreamTextDecode (256 text frames + finish) | 962,767 → 894,232 | 2,252,873 → 2,130,595 | 17,303 → 16,278 |
| 2. DocumentNormalizationPipeline/image2 | 309,898 → 171,782 | 1,440 → 1,312 | 7 → 6 |
| 3. RequestWorkloads/same/history256k | 2,217,263 → 1,961,499 | 7,731,349 → 6,520,167 | 12,516 → 11,874 |
| 4. StreamTextDecode (256 text frames + finish) | 886,780 → 858,649 | 2,105,409 → 1,911,902 | 16,278 → 14,480 |
| 5. BoundedBodySize/2097152/known-true | 227,229 → 72,692 | 4,702,026 → 2,105,416 | 28 → 3 |

The document benchmark isolates measurement followed by normalization, and the
body benchmark isolates reading. Their percentage gains must not be presented
as complete gateway gains. Short/unknown/31 MiB body controls and short/tool
request controls were also measured; unknown and greater-than-8-MiB sizes retain
the original algorithm.

## Validation and reproduction

- `GOMAXPROCS=4 go test -C backend ./...`
- Targeted `-race` checks for document facts/concurrent normalization, selected
  fields, canonical text, empty predicates and stream lifecycle/native replay.
- Each gateway run verifies exact response content, upstream generation count,
  execution identity, compiler version, package hashes and conversion policies.
- Regression coverage includes duplicate/escaped keys, independent field
  storage, invalid UTF-8, escaping/normalization, mutated output containers,
  tighter resource budgets, untrusted body lengths, short reads and read errors.

Re-run the microbenchmarks from the repository root:

```sh
GOMAXPROCS=4 go test -C backend -pgo=off ./protocol \
  -run '^$' -bench '^(BenchmarkDocumentNormalizationPipeline|BenchmarkBoundedBodySize)$' \
  -benchmem -benchtime=500ms -count=5
GOMAXPROCS=4 go test -C backend -pgo=off ./protocol/builtin \
  -run '^$' -bench '^(BenchmarkStreamTextDecode|BenchmarkRequestWorkloads)$' \
  -benchmem -benchtime=700ms -count=5
```

Raw profiles, test binaries and local measurement scripts are excluded from
the PR. The benchmark harnesses and regression tests are included.

Local evidence is in `.tmp-dev/optimization-implementation/`: source overlays
and SHA256-labelled binaries for stages 0–5, exact commands, per-run settings,
raw measurements, microbenchmark logs, test logs and `validated-summary.json`.
`run-stages.py` builds the stage binaries. `run-stages-clean.py` performs the
serial comparisons; `run-incremental-confirm.py` independently confirms steps
1 and 5 with unique evidence directories. The earlier exploratory gateway series
overlapped test execution and is excluded from these tables. Formal incremental
steps 2–4 use their respective groups in `gateway-results-clean-grouped.json`;
steps 1/5 use `gateway-results-incremental.json`. Combined results use only the
`combined-*` groups. `summarize.py` regenerates this document from those files.
