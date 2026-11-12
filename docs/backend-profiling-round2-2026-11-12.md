# Backend profiling optimization, round 2 — 2026-11-12

Baseline: `a94f101f2754a9a9a38857406fd5bad78a28a19f` (the previous
optimization round). Three changes were implemented and measured sequentially.

## Changes and boundaries

1. Raw-string resource metering proves short ASCII strings (up to 32 bytes)
   need no HTML escaping or UTF-8 repair in one small loop. Longer strings keep
   the standard library's bulk searches. The two searches for literal U+2028 /
   U+2029 are skipped when their required first byte, `0xe2`, is absent. HTML
   escaping, invalid UTF-8 repair, escaped spellings and byte-limit boundaries
   retain the existing behavior. An initial eight-byte-at-a-time implementation
   regressed large-string benchmarks by 16–26% and was discarded.
2. History wire validation reads selected fields instead of copying entire
   message objects and their unrelated text payloads. Chat tool association
   checks select role, tool-call ID and calls; call checks select only ID.
   Chat content is read only for the assistant reasoning association check.
   Anthropic/Gemini role checks select only role. Responses retains content
   for its assistant content-block checks. The final semantic association
   validation remains. This reduces repeated copies without changing the
   general fragment-sharing threshold, introducing borrowed storage or a cache.
3. Chat wire validation selects choice index/message/delta and only the role
   inside the message. Stream lifecycle validation selects index, delta, finish
   reason, role, tool calls and tool index as needed. Maps for unrelated fields
   are avoided. Required-object errors, identity binding, terminal-state rules,
   tool-index bounds/gaps and duplicate-choice detection remain. Selected values
   still own storage; duplicate decoded keys retain last-occurrence semantics.

## Measurement method

Apple M5, macOS arm64, Go 1.27.4; `GOMAXPROCS=4`, `GOGC=100`,
`GOMEMLIMIT=2GiB`, PGO off. Client, gateway and synthetic provider run in separate
processes. Current built-in codec execution is named `legacy` by the harness.
Each comparison uses three fresh processes per side, alternating before/after
order, and three batches per process. Each batch has 64 requests (24 for
images), concurrency one, logging off. All 48 gateway runs were serial, without
competing tests. Each checked exact output and one upstream call per request.
Stage binaries were built with Go source overlays, with source and binary hashes
recorded. Stages 0/1/2/3 contain baseline / plus meter / plus history / plus stream.

Tables report medians of nine batches, which are **three independent processes**.
CPU is gateway process CPU time divided by requests; allocated bytes are total
allocation, not live heap or RSS. macOS RSS is unavailable in this harness.
These synthetic measurements do not predict model latency or production capacity.

## Incremental gateway results

| Change / case | Allocated KiB/request | Allocations/request | CPU ms/request | P50 ms |
| --- | ---: | ---: | ---: | ---: |
| 1 / image2 | 20369.8 → 20331.8 (-0.2%) | 3561 → 3558 | 12.178 → 11.485 (-5.7%) | 11.865 → 11.351 |
| 2 / history256k | 10117.1 → 9357.8 (-7.5%) | 28479 → 27836 | 5.371 → 5.129 (-4.5%) | 4.625 → 4.524 |
| 3 / stream256 | 7853.6 → 7215.7 (-8.1%) | 73943 → 69814 | 8.984 → 8.789 (-2.2%) | 6.370 → 6.271 |

Paired process-median CPU reductions were respectively 2.1–7.3%, 1.0–5.1%,
and 1.1–3.1%. History and stream allocation-byte reductions were positive in
all three process pairs. Image allocation changes fluctuate; step 1 is retained
for CPU work reduction. Small timing deltas, especially for streams with high
system-call overhead, should be treated as directional.

## Combined gateway results

A separate alternating comparison measures baseline against all three changes.

| Case | Allocated KiB/request | Allocations/request | CPU ms/request | P50 ms |
| --- | ---: | ---: | ---: | ---: |
| chat-short | 132.4 → 131.0 (-1.0%) | 2032 → 2004 | 0.490 → 0.489 (-0.3%) | 0.450 → 0.428 |
| history256k | 10090.3 → 9318.5 (-7.6%) | 28472 → 27818 | 5.471 → 5.155 (-5.8%) | 4.781 → 4.568 |
| tools32 | 521.1 → 524.3 (+0.6%) | 6798 → 6776 | 1.952 → 1.527 (-21.8%) | 1.880 → 1.420 |
| stream256 | 7787.6 → 7237.5 (-7.1%) | 73949 → 69814 | 9.054 → 8.851 (-2.2%) | 6.447 → 6.277 |
| image2 | 19901.1 → 19475.8 (-2.1%) | 3550 → 3537 | 11.932 → 11.706 (-1.9%) | 11.887 → 11.413 |

Image CPU gains vary between the incremental and combined comparisons, so the
5.7% incremental figure is not a universal speedup. Short-request CPU is within
noise (paired changes range from -11.8% to +6.0%). Tool allocation bytes do not
improve; CPU falls in all three pairs (19.9–23.6%). Many short JSON strings in
tool schemas benefit from step 1, beyond the large-image use case.

## Microbenchmarks

Five samples per side, 300–400 ms per sample, no competing workload. Each
baseline includes preceding changes. Medians:

| Benchmark | ns/op before → after | B/op before → after | allocs/op before → after |
| --- | ---: | ---: | ---: |
| RawStringMeter/short | 23.86 → 7.86 | 0 → 0 | 0 → 0 |
| RawStringMeter/image2 | 170260 → 152173 (-10.6%) | 0 → 0 | 0 → 0 |
| RawStringMeter/image8 | 690762 → 610503 (-11.6%) | 0 → 0 | 0 → 0 |
| RawStringMeter/image31 | 2936282 → 2580796 (-12.1%) | 0 → 0 | 0 → 0 |
| RawStringMeter/html | 544.2 → 478.3 | 0 → 0 | 0 → 0 |
| RawStringMeter/unicode | 63549 → 63775 (+0.4%) | 0 → 0 | 0 → 0 |
| RequestWorkloads/same/history256k, step 2 | 1873875 → 1867896 | 6432560 → 6060420 (-5.8%) | 11874 → 11870 |
| RequestWorkloads/anthropic/history256k, step 2 | 990848 → 998993 | 4017320 → 3798536 (-5.4%) | 7387 → 7384 |
| FinalWireValidation, step 3 | 1369715 → 1305572 (-4.7%) | 2074583 → 1615976 (-22.1%) | 36189 → 31294 (-13.5%) |

History's isolated conversion microbenchmark shows memory improvement, without
consistent timing improvement. Larger gateway allocation savings include other
validation passes. Byte totals can vary with Sonic pools and GC, so allocation
counts and process pairs are also recorded.

## Validation and evidence

New tests cover raw-string lengths around the fast-path boundary, HTML, literal
and escaped separators, partial/invalid UTF-8 and exact byte-limit boundaries.
Wire tests cover escaped duplicate roles/deltas/tool IDs/indexes, null final
occurrences, association failures and tool-index gaps. Existing full tests cover
wire/schema parity, ownership, history ordering and stream lifecycle failures.

Local evidence is in `.tmp-dev/optimization-round2/` (ignored): `run.py`,
`summarize.py`, stage source overlays and binary/source hashes, benchmark outputs,
48 gateway run directories, `gateway-results.json` and `summary.json`.
Microbenchmarks use `go test -pgo=off ./protocol -run '^$' -bench
'^BenchmarkRawStringMeter$' -benchmem -benchtime=400ms -count=5` and the matching
`./protocol/builtin` request-workload / final-wire-validation benchmarks.

Remaining work: general history fragment clones and container encoding still
exist; this round avoids unnecessary validation copies without weakening lifetime
bounds. Stream root-field materialization and system-call overhead also remain.

Final verification: `go test -pgo=off ./...` and `go vet ./...` passed.
`go test -race ./protocol` passed. The complete `./protocol/builtin` race suite
hit Go's default 10-minute timeout; no data-race report was emitted before the
timeout. It is not counted as a passing check. A focused builtin race run passed and covers
selected-member duplicate-key tests, wire lifecycle accept/reject tests, wire
view ownership/isolation, final Chat tool ordering and escaped roles.
