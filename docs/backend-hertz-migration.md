# Native Hertz migration brief

The HTTP stack now uses **Hertz v0.10.6** with its standard transport. Gin and
HTTP-handler compatibility adapters are removed. Authentication uses native
Hertz middleware; business operations receive an explicit `context.Context`.

## Implementation

- Bounded streaming request reads enforce application admission and body budgets.
- Native chunk writers preserve SSE/NDJSON flushing, failure trailers, response
  commitment and the prohibition on generation retries after output begins.
- Agent/MCP/A2A event delivery uses Hertz's SSE writer. MCP implements the SDK's
  public transport interfaces; A2A uses a project-maintained JSON-RPC/SSE binding
  over the SDK's public `RequestHandler` and version-conversion APIs.
- WebSocket upgrades use `hertz-contrib/websocket`. Cancellation interrupts I/O
  while connection ownership and cleanup stay with Hertz.
- `OnRun` starts background services; failures share the application shutdown
  path. `Tracer.Finish` releases request resources and records completion after
  response serialization, including BodyStream and hijacked handlers. Completion
  callback panics still unwind the remaining resource releases.
- Shutdown acknowledgments trigger shutdown only after the response attempt
  completes. Requests and workers drain before usage/log persistence and storage
  closure. Hertz's concurrent `OnShutdown` hooks are not a storage-close barrier.
- Embedded assets use known-length `SetBodyStream` and native byte-range parsing.
  Build scripts no longer require Gin's `-tags=sonic`.

**Compatibility change:** A2A accepts canonical version headers `0.3` and `1.0`;
the patch-form aliases `0.3.0` and `1.0.0` are removed.

## Measured performance

Measured on 2026-10-21: Linux x86_64, Xeon Platinum 8259CL, 8 available logical
CPUs and 16 GiB RAM. Go 1.27.2, CGO/PGO disabled, `GOMAXPROCS=4`, `GOGC=100`,
`GOMEMLIMIT=2GiB`. Each of 14 scenarios ran Gin → Hertz → Hertz → Gin, with three
warm repeats per process: 128 requests/repeat, or four for 2 MiB image scenarios.
Client, gateway and provider were separate processes; only the gateway was
measured, including authentication, request lifecycle and SQLite usage settlement.
Both sides included the same preceding protocol optimizations.

| Scenario | Peak RSS MiB, Gin → Hertz | Allocations/request | CPU ms/request |
| --- | ---: | ---: | ---: |
| Short chat | 62.1 → 57.8 | 2647 → 2633 | 1.29 → 1.27 |
| Chat → Anthropic | 64.0 → 60.1 | 2306 → 2280 | 1.25 → 1.20 |
| Four concurrent short requests | 64.7 → 60.8 | 2646 → 2624 | 1.28 → 1.31 |
| 256-frame stream | 62.3 → 58.5 | 96532 → 96523 | 29.18 → 29.39 |
| 1024-frame stream | 62.6 → 58.8 | 376282 → 376296 | 110.66 → 111.42 |
| 2 MiB image | 87.0 → 85.5 | 4882 → 4862 | 57.60 → 55.66 |

Values are medians of six warm repeats; peaks are sampled every 20ms.
Non-image peak RSS decreased **3.3–4.4 MiB (5.0%–7.0%)**. Three short-request
paths reduced allocations **0.5%–1.1%** and CPU time **2.2%–4.3%**. Long-stream
allocations were essentially unchanged; long-history/concurrent/stream CPU costs
were flat or slightly higher. Cumulative allocated bytes increased **0.6%–3.3%**
on long-history, long-stream and image workloads. This is a memory improvement
with modest workload-dependent CPU changes, not a universal speedup.

Validation passed: full Go tests, `go vet`, targeted race/shuffle tests,
Linux/Windows/macOS amd64/arm64 builds, 56 remote scenario runs and 12 profile
runs. A previous full-repository race run timed out and is not counted as passing.
Use the [benchmark guide](backend-performance.en.md) to collect fresh evidence;
raw profiles and long-form reports are kept outside the source tree.
