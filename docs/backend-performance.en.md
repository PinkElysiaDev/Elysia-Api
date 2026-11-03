# Backend performance benchmarks

[简体中文](backend-performance.md) · [Hertz migration and performance brief](backend-hertz-migration.md)

Run with Node.js, the project's Go toolchain and loopback networking. Providers,
credentials and databases are local fixtures. Keep raw measurements, profiles
and source snapshots in the output directory rather than the source tree.
Earlier algorithm reports remain available in [Git history](https://github.com/PinkElysiaDev/Elysia-Api/tree/ba6f856/docs).

```sh
# Record one simulator build; every comparison uses this binary and snapshot.
refactor_commit=$(git rev-parse HEAD)
node scripts/benchmark-performance.mjs --execution=packages --expect-commit="$refactor_commit" \
  --mode=measure --cases=chat-short --requests=1 --repeats=1 \
  --output=.tmp-dev/performance-provider

# Common HTTP paths; use a new, empty output directory.
node scripts/benchmark-performance.mjs --execution=packages --expect-commit="$refactor_commit" \
  --provider-build=.tmp-dev/performance-provider --mode=measure \
  --cases=chat-short,burst4,stream256,stream256-cross,image2 \
  --requests=128 --repeats=3 --idle-ms=1000 \
  --output=.tmp-dev/performance-measure

# Attribution runs are separate from formal measurements.
node scripts/benchmark-performance.mjs --execution=packages --expect-commit="$refactor_commit" \
  --provider-build=.tmp-dev/performance-provider --mode=profile \
  --profiles=chat-short,stream256-cross,image2 --seconds=15 \
  --output=.tmp-dev/performance-profiles
```

Use `--source=/path/to/worktree` for a comparison source. Hertz uses Sonic by
default; the collector adds `-tags=sonic` for Gin. PGO is disabled. Runtime
defaults are `GOMAXPROCS=4`, `GOGC=100`, `GOMEMLIMIT=2GiB`, overridable via env.
Execution and the full source commit are required: `packages` installs current packages in an
isolated database; `production` checks actual defaults and fails while those still use legacy
definitions. `legacy` only accepts reference A, `b2844846ca111492972264c508825e2f56cc4ff1`.
Its identity and fixed provider wiring use a test overlay without modifying archived sources.
The old microbenchmarks in `all`/`micro` are restricted to legacy and are not package evidence.

`--provider-build` selects a completed build from the current collector. It checks the recorded
binary and package hashes and starts the simulator from that build's `source/backend/server`,
so later workspace edits cannot change its fixtures. Use the same directory for every version.
Without it, the simulator changes with the measured version, preventing independent attribution
of latency and throughput differences to the gateway.

## Scenarios and boundaries

- Short requests: `chat-short`, `anthropic-short`, `responses-short`, `gemini-short`.
- Cross-protocol/complex requests: `chat-anthropic`, `chat-responses`, `prefix32k`, `history256k`, `tools32`, `candidates4`, `reload-chat`.
- Concurrency: `burst4`, `burst4-body`; streaming: `stream16`, `stream256`, `stream1024`, `stream256-cross`, `stream256-burst4`.
- Images: `image2`, `image2-media`. Images use four requests/repeat and reload uses one, regardless of `--requests`.
- Client, gateway and provider are separate processes. Allocation, process CPU, GC and Heap/RSS counters cover only the gateway, including authentication, request lifecycle and asynchronous SQLite usage persistence.
- Counters wait for usage settlement; HTTP latency ends when the response is consumed. Body logging `off` does not disable usage persistence. Closed-loop throughput is not maximum server capacity.
- Client semantic validation runs after each request's latency timer but still affects closed-loop throughput; comparisons across versions must also fix the client validator before attributing throughput changes.
- Cold requests are separate. Formal measurements do not force GC. Memory peaks are sampled every 20ms and can miss short spikes; cumulative allocation, HeapAlloc and RSS are different quantities.
- Run matching ABBA comparisons serially on the same machine. Preserve regressions and conflicting results; do not generalize small changes or low-sample tail latency.

## Evidence

Outputs include `report.json` with environment/commands/binary SHA256,
`source/backend/` and `source-manifest.json` including untracked sources,
per-request/per-repeat `measurements.json`, recovery timelines, profiles, top
reports and checksums. `summary.md` includes allocation, CPU, latency and memory.
Profile runs are separate diagnostics; heap profiles are sampled attribution,
not exact per-object accounting.
`execution.json` records the gateway identity. Fixed simulators also write `provider-execution.json`
with their commit, compiler, execution mode and target package hash; the provider field in
`report.json` identifies the binary and fixture sources.

```sh
go tool pprof -top -sample_index=alloc_objects \
  -base .tmp-dev/performance-profiles/memory-stream256-cross/alloc-before.pprof \
  .tmp-dev/performance-profiles/memory-stream256-cross/alloc-after.pprof
```
