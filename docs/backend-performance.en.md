# Backend performance benchmarks

[简体中文](backend-performance.md) · [Hertz migration and performance brief](backend-hertz-migration.md)

Run with Node.js, the project's Go toolchain and loopback networking. Providers,
credentials and databases are local fixtures. Keep raw measurements, profiles
and source snapshots in the output directory rather than the source tree.
Earlier algorithm reports remain available in [Git history](https://github.com/PinkElysiaDev/Elysia-Api/tree/ba6f856/docs).

```sh
# Full collection: microbenchmarks, HTTP matrix, profiles and cancellation.
node scripts/benchmark-performance.mjs --output=.tmp-dev/performance-baseline

# Common HTTP paths; use a new, empty output directory.
node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,burst4,stream256,stream256-cross,image2 \
  --requests=128 --repeats=3 --idle-ms=1000 \
  --output=.tmp-dev/performance-measure

# Attribution runs are separate from formal measurements.
node scripts/benchmark-performance.mjs --mode=profile \
  --profiles=chat-short,stream256-cross,image2 --seconds=15 \
  --output=.tmp-dev/performance-profiles
```

Use `--source=/path/to/worktree` for a comparison source. Hertz uses Sonic by
default; the collector adds `-tags=sonic` for Gin. PGO is disabled. Runtime
defaults are `GOMAXPROCS=4`, `GOGC=100`, `GOMEMLIMIT=2GiB`, overridable via env.

## Scenarios and boundaries

- Short requests: `chat-short`, `anthropic-short`, `responses-short`, `gemini-short`.
- Cross-protocol/complex requests: `chat-anthropic`, `chat-responses`, `prefix32k`, `history256k`, `tools32`, `candidates4`, `reload-chat`.
- Concurrency: `burst4`, `burst4-body`; streaming: `stream16`, `stream256`, `stream1024`, `stream256-cross`, `stream256-burst4`.
- Images: `image2`, `image2-media`. Images use four requests/repeat and reload uses one, regardless of `--requests`.
- Client, gateway and provider are separate processes. Allocation, process CPU, GC and Heap/RSS counters cover only the gateway, including authentication, request lifecycle and asynchronous SQLite usage persistence.
- Counters wait for usage settlement; HTTP latency ends when the response is consumed. Body logging `off` does not disable usage persistence. Closed-loop throughput is not maximum server capacity.
- Cold requests are separate. Formal measurements do not force GC. Memory peaks are sampled every 20ms and can miss short spikes; cumulative allocation, HeapAlloc and RSS are different quantities.
- Run matching ABBA comparisons serially on the same machine. Preserve regressions and conflicting results; do not generalize small changes or low-sample tail latency.

## Evidence

Outputs include `report.json` with environment/commands/binary SHA256,
`source/backend/` and `source-manifest.json` including untracked sources,
per-request/per-repeat `measurements.json`, recovery timelines, profiles, top
reports and checksums. `summary.md` includes allocation, CPU, latency and memory.
Profile runs are separate diagnostics; heap profiles are sampled attribution,
not exact per-object accounting.

```sh
go tool pprof -top -sample_index=alloc_objects \
  -base .tmp-dev/performance-profiles/memory-stream256-cross/alloc-before.pprof \
  .tmp-dev/performance-profiles/memory-stream256-cross/alloc-after.pprof
```
