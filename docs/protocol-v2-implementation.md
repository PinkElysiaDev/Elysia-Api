# Protocol v2 implementation ledger

Base: `8a0b23f`. Branch: `feat/protocol-v2-gateway`. Started: 2026-10-02.

This ledger records implemented and verified work; unchecked stages are not delivered capabilities. Commits remain local.

| Stage | Status | Validation / evidence |
| --- | --- | --- |
| C01 Cache fidelity | Complete | Backend full tests, vet, WebUI type check; cache audit and 96-case HTTP matrix; conversion benchmark baseline |
| C02 Tool definitions and lifecycle | Complete with documented interim boundary | Full backend tests and vet passed; native/preset/arbitrary-ID HTTP regression and native stream fidelity passed; legacy custom streams block unsupported tools pending C05/C06 |
| C03 Ordered model and provenance | Complete | protocol/relay tests passed: presence, long integers, native JSON, call order/association, scopes, contract roundtrip and transitional snapshots |
| C04 Native preservation and diagnostics | Complete | Full backend tests and vet passed; transactional edits, native/scoped isolation, capability and association failures covered; native tool renderer uses shared preservation |
| C05 Shared wire adapters | Pending | |
| C06 Stream and accounting | Pending | |
| C07 Bidirectional compiler | Pending | |
| C08 Capability verification | Pending | |
| C09 Revisions and activation | Pending | |
| C10 Custom ingress and routing | Pending | |
| C11 WebSocket sessions | Pending | |
| C12 Persistent async jobs | Pending | |
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
