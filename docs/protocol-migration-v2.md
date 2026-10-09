# Protocol v2 migration

The v2 runtime activates only definitions with current offline evidence. A
failed migration leaves management available and generation closed with
`protocol_migration_required`; it does not select the historical converter.
Offline fixtures are not evidence of a real provider connection or cache hit.

## Before upgrading

Stop the previous executable and retain it together with `config.json`, the
database and its encryption key. Do not replace the encryption key when
restoring a database. After schema preparation, startup first recovers the four
embedded presets, then completes legacy imports and binding verification.
Missing presets and append-only evidence need no backup. Existing rows are
protected by a current snapshot before replacement; schema initialization
precedes that snapshot, so a full pre-launch copy remains the rollback source
for changes made by an older executable's schema loader.

Historical `protocol_engine_v2_backup` records and files are retained without
being opened or revalidated at startup. Missing, damaged or outdated historical
backups cannot block insert-only recovery. Current snapshots use SQLite
`VACUUM INTO`, include committed WAL content, and check SHA-256, database
integrity and configuration/evidence baselines. They do not require old
protocol definitions to compile. The independent `protocol_current_snapshot_v2`
setting identifies the format, path, checksum, creation time and baselines.
Receipts record `backupMode: not_required` for pure additions or
`backupMode: current_snapshot` and the actual snapshot for overwrites.
Snapshot files sit beside the database and contain the same sensitive data.

## Preview and repair

Authenticated management endpoints:

| Method | Path | Result |
| --- | --- | --- |
| GET | `/api/admin/protocols/migration` | Committed receipt, or `null` |
| POST | `/api/admin/protocols/migration/preview` | Definitions, reports, bindings, diagnostics, baseline |
| POST | `/api/admin/protocols/migration/apply` | Atomically committed receipt |
| POST | `/api/admin/protocols/reload` | Retry preset recovery, pending migration and runtime publication |

Send `{}` for the initial preview. For repairs, supply `definitions`, keyed by
the **existing protocol ID**, and optional binding replacements. Each value is
a complete v2 definition. The server recompiles and verifies all candidates;
client-provided reports are rejected. Copy the returned `baseline` into the
apply request, retaining the same replacement definitions and bindings.

```json
{
  "baseline": "hash-from-preview",
  "definitions": {},
  "bindings": []
}
```

The preview performs no writes. Changed source credentials, models, group
membership, drafts, active revisions or bindings invalidate its baseline.
Usage/log writes do not. Identical repeated apply requests return the original
receipt, including after a reload failure. A changed apply request after a
completed migration conflicts; subsequent edits use revisions and activation.

Unedited presets are recognized by registered historical content hashes or
their current shipped content, not by ID alone. Edited presets and custom
legacy definitions remain intact. The conservative importer supplies a repair
draft and diagnostics; an operator/Agent must provide an explicit v2
replacement where equivalence cannot be established. A work-in-progress draft
does not replace an active revision and is not silently enabled by migration.

Disabled sources and their models are included. Standalone historical models
retain their empty source ID. Model/group tools and media constraints apply to
supplied bindings as well as automatically generated ones. No group ingress is
chosen implicitly. Existing API keys, source IDs, URLs and model references are
retained. Chat/Responses keep their versioned-base convention; Anthropic and
Gemini keep their complete operation paths.

## Atomic switch and recovery

All candidate definitions and the binding graph must pass before the store
writes drafts, revisions, offline reports, activations, bindings and the
completion receipt in one transaction. The store rechecks the baseline inside
that transaction. An injected failure after revision writes rolls everything
back. Registry publication follows commit, so a reload failure can be repaired
without reapplying the migration. New requests pin an immutable registry view.

After repair, apply the reviewed preview and reload. Reload also retries an
unfinished startup, but generation remains disabled until necessary migration,
binding verification and runtime publication succeed. Unchanged restarts reuse
current evidence without duplicate reports, activations or snapshots. Evidence
from an older compiler must be regenerated. The protocol listing exposes
`runtimeReady` and a structured `startupFailure`; four preset names or an HTTP
200 from `/health` do not prove that the protocol runtime is ready.

For executable rollback, stop the new server, restore the retained executable,
configuration, database and matching key as a consistent set. Keep the current
database separately if post-upgrade changes are needed. A Git revert is not a
database downgrade and does not restore usage written after migration.

## Evidence and staged boundary

`TestProtocolUpgradeStartupAndRestart` uses the production constructor and
checks insert-only startup without legacy preset seeding or a backup.
`TestProductionStartupIgnoresHistoricalBackups` reproduces a zero-preset database
with an old fingerprint and a backup missing `protocol_history`, then checks
ten unchanged restarts. Service/API tests cover edited
legacy definitions, explicit repair, forged reports, source-less models,
disabled sources, capability conflicts, repeated application and readiness.
Storage tests use real SQLite backup/reopen, stale fingerprints, failed
verification and a transaction-aborting trigger.

The four shipped definitions use direct ordered-model modules in
`backend/protocol/builtin`, including stateful event modules. Regression tests
independently inspect Responses tool frames and item identities, media/tool
extensions, reasoning summary/encryption, late cache/TTL/reasoning usage and
compound native session frames. No Maheshvara projection is used by these
modules.

C16 removed the historical handlers, Agent boundary, legacy editor and template
executor. Binding verification may publish explicitly restricted capability
profiles, each with independent evidence; actual requests must match a verified
profile. Failed combinations are retained as diagnostics, never ignored.
Compiler upgrades reverify edited active definitions and only replace known
preset fingerprints. See [cutover](protocol-cutover-v2.md).
