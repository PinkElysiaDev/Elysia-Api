# Protocol v2 system validation — 2026-10-03

All results below are local Windows amd64 results on a Ryzen 5 7600X.
No provider credentials were used. No remote CI was run, and no build was
published or deployed. Fixtures demonstrate protocol behavior, not model output
equivalence or provider cache hit rates.

## Executed gates

| Gate | Result |
| --- | --- |
| `go -C backend test ./... -count=1` | Passed; server package 189.737 s |
| `go -C backend vet ./...` | Passed |
| WebUI `tsc --noEmit` | Passed |
| `git diff --check` | Passed |
| Real Go/SQLite + Chromium editor/Agent | 3 tests passed; 5.3 s |
| Concurrent revision activation/reload, durable jobs, cancellation, long replay | 20 repetitions passed |
| Compiler fuzz, 20 s | 107,465 executions, passed |
| Mapping presence fuzz, 20 s | 968,085 executions, passed |
| JSON resource-limit differential fuzz, 20 s | 588,700 executions, passed |
| JSON object/array differential fuzz, 20 s | 1,069,725 executions, passed |
| Event replay fuzz, 20 s | 18,045 executions, passed |
| Race | Not executed: Windows lacks a usable GCC/CGO toolchain |

The checked-in `protocol.yml` workflow runs the full backend suite, vet, race,
fixed-duration fuzzing, stress repetitions, benchmarks and real browser tests
on Linux. Adding this workflow is not evidence it has run. Local toolchain
download attempts failed with connection resets; no partial toolchain was used.

The browser runner creates a fresh database and random panel token, starts only
its own backend and Vite children, and closes those children on completion. It
does not terminate services by port or executable name. Test data is retained
in the printed temporary directory for inspection.

## Coverage map

| Requirement | Evidence |
| --- | --- |
| Four-wire HTTP and stream combinations; arbitrary IDs | `server/cache_wire_matrix_test.go`, `preset_parity_test.go`, `cross_protocol_tools_e2e_test.go`, `protocol/builtin/stream_test.go` |
| Standalone ingress/upstream, no builtin shapes | `protocol/testdata/text-{alpha,beta}.json`, `server/custom_wire_v2_test.go`, `gateway_public_test.go` |
| Function/custom/hosted tools and incompatible histories | `builtin/module_test.go`, `stream_wire_test.go`, `server/tool_fidelity_e2e_test.go` |
| Cache TTL, five scopes, edits/delete/null, stable prefix | `builtin/cache_test.go`, `server/cache_custom_v2_test.go` |
| Cached read/creation, missing/zero, tails, SQLite | `builtin/stream_usage_test.go`, `server/gateway_accounting_test.go`, `gateway_usage_details_test.go` |
| Explicit aliases and observed-zero override | `protocol/usage_alias_test.go` |
| Native extension identity, scope, updates and deletions | `protocol/runtime_test.go`, `native_test.go`, `builtin/module_test.go` |
| SSE/NDJSON, slow read, no total stream timeout | `protocol/transport_http_test.go`, `relay/stream_idle_test.go`, `server/custom_cumulative_v2_test.go` |
| WebSocket tools/media/backpressure/close | `protocol/session_transport_test.go`, `relay/protocol_websocket_test.go`, `server/gateway_session_test.go` |
| Tasks: uncertain submit, restart, cancel, exactly-once settlement | `storage/protocol_job_test.go`, `server/gateway_job_test.go` |
| Immutable revisions and atomic refresh failure | `storage/protocol_revision_test.go`, `protocol_refresh_test.go`, `server/protocol_refresh_test.go` |
| Migration backup, edited definitions, failure rollback | `storage/protocol_upgrade_test.go`, `server/protocol_upgrade_runtime_test.go` |
| Editor and Agent use the shared validator | `packages/webui/tests/protocol-v2.spec.ts`, `server/agent_protocol_v2_test.go` |

Negative scenarios remain tests: unknown mechanisms, unsupported capabilities,
unassociated tools, invalid JSON arguments, changed cumulative prefixes, missing
terminal events and stale evidence must fail. Runtime refresh tests inject a
failure after revision writes and verify no partial graph or success receipt
is persisted. Retained long-stream state is checked after 16 MiB of text.

## Performance findings and remaining cost

The request fixture is byte-identical to C01's cache fixture. Anthropic is the
valid equivalent target. Other targets cannot express its top-level breakpoint
and are not presented as successful performance comparisons.

| Boundary | Historical same-host runs | C16 before C17 optimization | C17 isolated final runs |
| --- | --- | --- | --- |
| Chat cache request → Anthropic | 29.2–30.8 µs; 18.0 KB; 322 allocs | 424–455 µs; 271 KB; 6,158 allocs | 90.8–96.3 µs; 53.8 KB; 752 allocs |
| 256 × 256-byte Chat stream decode | 3.58–4.56 ms; 9.98 MB | 15.0–15.3 ms; 9.48 MB | 3.04–3.34 ms; 2.41 MB |

Profiling identified repeated semantic JSON serialization, recursive container
decoding and allocating JSON-token limit checks. C17 replaces the limit walk
with an allocation-free scan over already validated JSON, uses container spans
with independent child storage, and adds typed module calls. The typed path
still checks limits, capabilities, provenance and native preservation. Declared
schemas, `after` and `initial` mappings force the common JSON execution path;
they cannot be bypassed by this optimization. The full shipped fixture suite
passes through both typed modules and wrappers exposing only the JSON interface.
Differential fuzzing checks container and limit behavior against `encoding/json`.

Request conversion still costs about three times the C01 boundary. The new
boundary retains native provenance and validates the full semantic contract,
including resources and limits, which C01 did not do. This is a measured
remaining performance cost, not a claim of zero regression. Limits are kept;
they were not removed to meet the old benchmark. Deployment throughput and
latency targets require measurement with the intended concurrency and hardware.

## Reproduction and release gates

```sh
go -C backend test ./... -count=1
go -C backend vet ./...
go -C backend test -race ./protocol/... ./storage ./relay ./server -count=1
go -C backend test ./protocol ./storage ./relay -run 'TestProtocolConcurrent|TestGenerationJob|TestRunSession|TestLongReplay' -count=20
go -C backend test ./protocol/builtin -run '^$' -bench 'BenchmarkCacheConversion|BenchmarkStreamTextDecode' -benchmem -count=3
node scripts/test-protocol-e2e.mjs
```

Fuzz functions are in `backend/protocol/fuzz_test.go`; CI runs each with
`-run '^$' -fuzz '^NAME$' -fuzztime=20s -parallel=2`.

Before release, obtain an actual passing race run and verify the intended real
upstream/model/account combinations. To validate caching, repeat requests with
a sufficiently long stable prefix within the declared TTL under a fixed account
and model, and inspect both wire policy and returned usage. No local mock result
substitutes for this evidence. Six-platform builds and host smoke evidence are
recorded separately in the release-readiness document.
