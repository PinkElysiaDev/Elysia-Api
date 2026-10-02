# Protocol v2 implementation ledger

Base: `8a0b23f`. Branch: `feat/protocol-v2-gateway`. Started: 2026-10-02.

This ledger records implemented and verified work; unchecked stages are not delivered capabilities. Commits remain local.

| Stage | Status | Validation / evidence |
| --- | --- | --- |
| C01 Cache fidelity | Complete | Backend full tests, vet, WebUI type check; cache audit and 96-case HTTP matrix; conversion benchmark baseline |
| C02 Tool definitions and lifecycle | Complete with documented interim boundary | Full backend tests and vet passed; native/preset/arbitrary-ID HTTP regression and native stream fidelity passed; legacy custom streams block unsupported tools pending C05/C06 |
| C03 Ordered model and provenance | Complete | protocol/relay tests passed: presence, long integers, native JSON, call order/association, scopes, contract roundtrip and transitional snapshots |
| C04 Native preservation and diagnostics | Complete | Full backend tests and vet passed; transactional edits, native/scoped isolation, capability and association failures covered; native tool renderer uses shared preservation |
| C05 Shared wire adapters | Shared wiring complete; semantic cutover remains | Full backend tests/vet/type check passed; independent adapters, native extension and usage-alias preservation, preset hash upgrades and arbitrary-ID stream/HTTP tests. Modules still use the transitional Maheshvara boundary until v2 runtime integration/removal. |
| C06 Stream and accounting | Shared runtime rules complete; semantic cutover remains | Full backend tests and vet passed. 16 tail-frame combinations, zero/presence and TTL merges, persisted counters, tool input/association, cumulative rewrite, bounded state and sequence checks. See stream fidelity audit; local race blocked by C toolchain. |
| C07 Bidirectional compiler | Compiler and typed mappings complete; staged integration remains | Strict bounded v2 compiler, schema/catalog, native edit rules, four HTTP modules, conservative legacy import; two standalone protocol definitions, 16 request and 16 response combinations; full backend tests/vet. See definition reference for stateful module and migration boundaries. |
| C08 Capability verification | Complete; activation service follows in C09 | Shared offline verifier, direction-specific capability evidence, tool-history/event replay, native probes, hash-bound activation prerequisite and paired protocol verification. Full backend tests/vet; see verification reference. |
| C09 Revisions and activation | Complete; gateway consumption follows in C10 | SQLite drafts/revisions/reports/active pointers, optimistic concurrency, atomic registry, pinned requests, guarded rollback/reverification and authenticated management API. Persistence/concurrency/restart and API tests plus full backend tests/vet. |
| C10 Custom ingress and routing | Runtime integrated; final preset cutover remains | Verified custom HTTP/SSE/NDJSON forwarding, public endpoint dispatch to active revisions, persistent model/group bindings with hash-bound paired evidence, actual input/output capability checks, scoped provenance, cancellation and usage persistence. Full backend tests/vet, framing/HTTP/API regressions and stable legacy conversion benchmark. See gateway reference for remaining built-in event-module/migration boundary. |
| C11 WebSocket sessions | Complete | Shared bounded session replay, standalone alpha/beta definitions, fixed gateway upgrade route, secure maintained pure-Go transport and per-response SQLite accounting. Actual local socket tests cover tools, usage tails, binary bytes, ping/pong, auth/origin, slow clients, cancellation, close deadlines and no replay. Full backend tests/vet; see session reference. |
| C12 Persistent async jobs | Complete | Shared compiled task mappings, standalone alpha/beta task definitions, pinned HTTP execution and native/synchronous gateway flows. Real SQLite restart, reservation/uncertainty, cancellation race, immutable identity and settlement crash-window tests; local HTTP mapping/revision/accounting/wait-policy tests; full backend tests/vet. See [job reference](protocol-jobs-v2.md). |
| C13 Protocol editor | Pending | |
| C14 Agent authoring | Pending | |
| C15 Unified migration | Pending | |
| C16 Legacy removal and quality | Pending | |
| C17 System verification | Pending | |
| C18 Documentation and builds | Pending | |

The unrelated pre-existing `docs/debug-report-2026-09-18.md` is excluded. Offline fixtures do not establish real provider compatibility or cache hit rates.

Batch 2 performance: existing conversion benchmark allocations decreased by 2
per target versus C01 (Chat 336, Claude 320, Gemini 295, Responses 284).
Observed Chat CPU variation was checked against an isolated C01 archive on the
same host: C01 28.6–30.5 us, current 23.2–27.8 us, three runs each. This did not
reproduce a regression relative to the baseline. Native mutation/validation
benchmarks and final engine comparisons remain part of C17.

C05 uses one wire module implementation for native and custom decoding/encoding
and removes duplicated custom request shaping and preset event rules. The
generic adapter contract permits the current legacy semantic boundary during
the staged migration; it is not evidence that C07–C16 are complete. Native
response snapshots preserve unknown fields and exact numbers when unchanged;
model/ID/usage updates overlay them. Other legacy response edits fail explicitly
until they are expressed through the new ordered model's mutation rules.

C05 benchmark check: 20.3–23.6 us/op in one four-target run. Preserving numbers
at the wire JSON boundary adds 7 allocations and about 880 bytes versus C04;
this is the decoder buffer/number representation cost, with no observed CPU
regression in this run. Final comparisons require the C17 benchmark suite.

C06 stream evidence: [stream audit](stream-fidelity-audit-2026-10-02.md).
Shared stream state, native/custom/Agent drain rules, and provider accounting
are wired into runtime. Legacy Maheshvara projections still remain for C07–C16.
The 64 KiB stream decode benchmark decreased from about 9.98 MB to 1.02 MB
allocated per operation and from 1.96–2.32 ms to 0.83–0.94 ms on this host.
Request conversion allocation counts are unchanged from C05. Local race could
not build: CGO is disabled by default; installed clang uses an incompatible
MSVC target. Linux CI validation remains outstanding.
