# Protocol upgrade delivery and release readiness

[简体中文](protocol-release-readiness.md) · [User guide](protocol-guide.en.md) · [System evidence](protocol-system-validation.md)

For subsequent C19?C23 live, race and performance evidence, see [release validation](protocol-release-validation.en.md). The tables and six-platform artifacts below describe the C18 delivery, not execution evidence for the later optimized version.

Date: 2026-10-03. Branch: `feat/protocol-v2-gateway`. All commits and artifacts are local. Nothing was pushed, published or deployed.

## Delivered scope

The four presets, custom ingress/upstream protocols, editor, Agent, previews and verification share `backend/protocol`. The legacy execution kernel is removed. New protocols within the engine's supported semantics/transports declare directions, capabilities, mappings, operations and fixtures without Go changes. Unrepresentable capabilities produce diagnostics rather than silent deletion.

HTTP JSON, SSE, NDJSON, declarative WebSocket sessions and durable async jobs have local integration evidence. Editor and Agent share revisions, verification and activation; saving does not activate and old evidence does not validate changed content. The Agent does not execute client business tools.

This does not claim support for every new mechanism of every API. Both endpoints must express the required semantics equivalently. Identical model output, provider caching policy and cross-account reuse of resources/signatures are outside the conversion guarantee.

## Verification status

| Gate | Actual result |
| --- | --- |
| C17 complete backend tests, vet, WebUI type check | Passed; server tests 189.737 s |
| Editor/Agent browser tests | 3 passed; Chromium against isolated Go/SQLite |
| Reload/job recovery/cancellation/long-stream stress | 20 repetitions passed; forwarded 16 MiB text does not accumulate retained payload |
| Five fixed-duration fuzz targets | 20 s each, 2,752,020 total executions, passed |
| Published standalone cache protocol | Compilation/offline verification passed; independent `promptPolicy` and `meter` paths |
| C18 complete backend/help/snapshot checks | Passed; server 189.669 s, published examples, CLI consistency and embedded source reads passed; vet/type/diff checks passed |
| Six-platform builds and host smoke | All built; Windows amd64 isolated database/four presets/schema/UI/JS checks passed; five other targets not run |
| Race | No successful execution: Windows lacks a usable GCC/CGO toolchain; downloads failed |
| Remote Linux CI | Gates configured, not pushed or executed; not a passing result |
| Real providers/models and live cache hits | Not verified; no substitution of mock results |

See [system evidence](protocol-system-validation.md) for coverage, negative cases and reproduction. Local logs are in the repository parent's `.cache/c18-backend-full.json`, `c18-vet.txt`, `c18-build.txt` and `c18-smoke.txt`; logs are not committed. The runner prints its independent evidence directory. The post-commit rebuild and smoke use `c18-build-final.txt` and `c18-smoke-final.txt`.

## Performance

On the same Ryzen 5 7600X, Chat cache request → Anthropic measured 29.2–30.8 µs / 18.0 KB / 322 allocations at C01 and 90.8–96.3 µs / 53.8 KB / 752 allocations at C17. Profiling removed repeated semantic serialization, container decoding and allocating limit scans. Request CPU remains roughly three times the old boundary; this is not zero regression.

The 64 KiB stream decode improved from historical 3.58–4.56 ms / 9.98 MB to 3.04–3.34 ms / 2.41 MB. The new request boundary also checks provenance, native preservation, capabilities and resource limits. Those checks were retained. Deployment throughput/latency still require target-hardware and concurrency measurement.

## Builds and host smoke

```sh
node scripts/build-standalone.mjs
node scripts/smoke-standalone.mjs
```

The build runs WebUI build/typecheck and embeds current Go, frontend, presets, protocol guides, CLI references and examples. Generated directories are confined to the repository. Local configuration, databases, keys, debug reports and test logs are not distribution content.

| Target | Artifact relative to repository root | Execution boundary |
| --- | --- | --- |
| Windows amd64 | `dist/standalone/elysia-api-windows-amd64.exe` | Executable on the current host |
| Windows arm64 | `dist/standalone/elysia-api-windows-arm64.exe` | Cross-build only |
| Linux amd64 | `dist/standalone/elysia-api-linux-amd64` | Cross-build only |
| Linux arm64 | `dist/standalone/elysia-api-linux-arm64` | Cross-build only |
| macOS amd64 | `dist/standalone/elysia-api-darwin-amd64` | Cross-build only |
| macOS arm64 | `dist/standalone/elysia-api-darwin-arm64` | Cross-build only |

The smoke runner creates a fresh temporary directory/database, random admin token and local port, with model catalog network synchronization disabled. It checks `/health` database status and commit, the schema compiler version, all four active presets, and embedded UI/JS assets. It closes only its own child process. The printed directory retains `evidence.json`, logs and the database; evidence contains the binary SHA-256, not the token. Other platforms have no execution evidence.

Rebuild after committing so `/health.commit` identifies C18; artifacts are not checked in. This report does not embed its own commit hash and trigger a self-referential update cycle. Inspect the local history with:

```sh
git log --oneline --reverse 8a0b23f..HEAD
git log -1 --format='%h %s'
```

On Windows, `Get-FileHash dist/standalone/* -Algorithm SHA256` produces a complete artifact checksum list.

## Local commit index

| Stage | Commit | Change |
| --- | --- | --- |
| C01 | `d77aac7` | Cache semantics, upgrade and audit |
| C02 | `287568a` | Tool definitions and lifecycle |
| C03 | `3c312e3` | Ordered semantics, presence and provenance |
| C04 | `68955d5` | Native preservation, edits and diagnostics |
| C05 | `f745143` | Shared built-in/custom adapter contract |
| C06 | `a0b44e4` | Shared events and accounting |
| C07 | `acdc236` | Bidirectional declarative compiler |
| C08 | `24c4257` | Capability-driven verification |
| C09 | `f8b4bf1` | Immutable revisions and atomic activation |
| C10 | `572f82e` | Custom ingress and capability routing |
| C11 | `ce5d550` | Declarative WebSocket sessions |
| C12 | `5a7d686` | Durable async jobs |
| C13 | `5f43e87` | Protocol editor |
| C14 | `1f95bc0` | Agent authoring and shared verification |
| C15 | `0fc3613` | Graph migration, backup and cutover |
| C16 | `8303ea7` | Legacy removal, canonical cache TTL and quality review |
| C17 | `9dde72e` | System/fuzz/stress verification and performance work |
| C18 | Commit containing this report | Bilingual docs, CLI, source snapshots, builds and evidence |

See the [implementation ledger](protocol-v2-implementation.md). Pre-existing unrelated `.tmp-c10-tests.log` and `docs/debug-report-2026-09-18.md` remain outside commits and distribution.

## Upgrade and rollback

1. Retain the old executable and a matching configuration/database/master-key set. Copy after stopping the old process, or use a consistent SQLite backup; copying a live main database without its WAL is insufficient.
2. Preview migration on a copy and repair edited presets, custom definitions and binding diagnostics. After verification, apply the same intent transactionally. The server revalidates; failure leaves management available for repair.
3. Only complete known fingerprints authorize preset replacement. User edits remain. Record backup locations and old/new executable identities.
4. Protocol rollback activates a prior revision verified by the current engine. Executable/database rollback requires stopping the new process and restoring the matching old executable/database/configuration/key set, then checking health and bindings. Restoring an old database loses subsequent records; first retain the current database for audit.

`git revert` alone does not undo database migration. See [migration](protocol-migration-v2.md) and [cutover](protocol-cutover-v2.md).

## Outstanding release conditions

Obtain an actual passing race run in a supported environment and real request/stream/tool-lifecycle evidence for intended provider/model/account combinations. Live caching needs a fixed account/model, a sufficiently long stable prefix repeated within TTL, and inspection of read/creation/total input counters.

Remaining request cost and cross-platform execution boundaries are disclosed. Local evidence supports the listed engineering behavior; it does not mean every release gate has passed.
