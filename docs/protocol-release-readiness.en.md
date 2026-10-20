# Protocol validation and release checks

[Documentation index](README.en.md) · [简体中文](protocol-release-readiness.md) · [Protocol guide](protocol-guide.en.md)

This page explains how to validate the checked-out code and prepare a release. Results belong to a specific commit, configuration and environment. Keep run logs, test counts and temporary work notes in CI, PRs or local working directories rather than maintained feature documentation.

## Validation layers

| Layer | Establishes | Does not replace |
| --- | --- | --- |
| Protocol fixtures and combinations | Declared capabilities, conversion rules, field presence and event associations obey the contract | Real provider acceptance |
| Local HTTP, WebSocket, task and SQLite integration | Routing, permissions, cancellation, persistence, restart and settlement behavior | Provider availability or production capacity |
| SDK and client tests | A fixed client consumes complete output and finishes tool rounds | Other versions, plugins or history compression |
| Real upstream probes | Observed behavior for the selected account, model, revision and configuration | Compatibility with all models and channels |
| Cache and timing experiments | Observed nonzero counters, resource lifecycle and the measured time window | Inferring cache hits from accepted fields or HTTP 200 |
| Race, fuzz, benchmarks and load tests | Concurrency, input boundaries and resource cost on tested paths | Capacity or latency promises in untested environments |

Record skips, expected rejections, rate limits and unfinished experiments separately from passes. Distinguish provider usage from gateway estimates; client placeholder counters are not observations.

## Local checks

Run relevant checks from the repository root:

```bash
go -C backend test ./... -count=1 -timeout=15m
go -C backend vet ./...
go -C backend test -race ./protocol/... ./storage ./relay ./server -count=1 -timeout=20m
node scripts/test-protocol-e2e.mjs
```

Race checks need a supported Go platform and C toolchain. Browser integration uses an isolated backend and database; see [development](development.en.md). Slow tests or unavailable environments must remain explicitly unverified.

| Change | Focused regression coverage |
| --- | --- |
| Parameters, model capabilities and conversion rules | Four-protocol JSON/SSE, strict/compatible policies, candidate isolation, native preservation and output limits |
| Tools, reasoning and signatures | Multiple call identities, complete second-turn history, encrypted reasoning, resource scope and tamper rejection |
| Streaming | Start/end order, usage tails, late errors, slow reads, cancellation and no replay |
| Bindings, revisions and upgrades | Evidence invalidation, concurrent edits, failure isolation, retained drafts and repeated restarts |
| Sessions and jobs | Bidirectional events, backpressure, uncertain submission, restart recovery and once-only settlement |
| Editor and Agent | Save/verify/activate, conflicting edits, exact JSON and permissions |

Negative tests must continue rejecting invalid types, unknown mechanisms, out-of-scope resources, mismatched tools and stale evidence. Do not remove assertions or narrow capability claims to hide failures.

## Real channels and caching

See the [protocol audit tool](../scripts/protocol-audit/README.md) for commands, configuration templates and result formats. Select accounts and models explicitly, set request budgets and timeouts, and use synthetic content with isolated instances.

Establish direct-call baselines before comparing gateway JSON/SSE and tool rounds. Check semantic content, completion, hidden error events and usage even after HTTP success. Separate provider restrictions, client failures and conversion defects.

For caching, fix the account, model and a sufficiently long stable prefix. Independently check nonzero reads/creation, streaming usage, and agreement between client output and SQLite. TTL, read refresh and explicit resource expiration need real elapsed time; not observing expiration does not prove unlimited retention. See [cache contracts](cache-usage-contracts.en.md).

Unknown native extensions, cross-protocol resource references, signature recovery after client compression and provider-specific fields each need their own fixtures and endpoint checks. Success on one channel does not broaden a protocol's support claim.

## Performance and builds

Compare identical inputs and concurrency on the same host. Record CPU, memory, allocations, latency and versions. For example:

```bash
go -C backend test ./protocol/builtin -run '^$' -bench 'BenchmarkCacheConversion|BenchmarkStreamTextDecode' -benchmem -count=3
```

Measure capacity targets on intended hardware and workloads. A historical benchmark is not a result for the current version.

See [development](development.en.md#release-builds) for builds. Cross-compilation establishes build success only; startup, database upgrades, UI and networking need separate checks on each target system. See [macOS validation](macos-testing.en.md) for the native App.

## Upgrade and rollback

1. Retain the old executable and matching configuration, database and master key. Stop writes before copying or use a consistent SQLite backup; include committed WAL data.
2. This release provides no old-schema/configuration compatibility or migration. Initialize a current-format environment, recreate required records through current management APIs, and resolve definition/binding diagnostics.
3. Shipped presets follow the program version. Keep customization under separate protocol IDs. Check custom revisions, drafts, explicit unbound states and repeated restarts.
4. Check `runtimeReady`, effective bindings and actual requests. A successful `/health` response alone does not establish protocol readiness.
5. Revision rollback requires current-engine verification. Program/database rollback restores a matching executable, database, configuration and key; retain the current database first to preserve later records.

`git revert` does not restore persisted data. See [deployment](deployment.en.md#upgrade-backup-and-restore) for upgrade requirements and [preset recovery (Chinese)](protocol-preset-recovery.md) for current protocol recovery.
