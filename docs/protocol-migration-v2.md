# Protocol v2 migration

The v2 runtime activates only definitions with current offline evidence. A
failed migration leaves management available and generation closed with
`protocol_migration_required`; it does not select the historical converter.
Offline fixtures are not evidence of a real provider connection or cache hit.

## Before upgrading

Stop the previous executable and retain it together with `config.json`, the
database and its encryption key. Do not replace the encryption key when
restoring a database. The executable opens the existing schema and snapshots
the database **before** historical configuration imports, preset renames or
preset base-path adjustments. The receipt identifies that snapshot. Database
schema initialization precedes this snapshot; a full pre-launch copy remains
the rollback source for changes made by an older executable's schema loader.

Backups use SQLite `VACUUM INTO`, include committed WAL content and are checked
against a configuration fingerprint. An interrupted/blocked migration retains
the first valid startup backup. A missing or changed backup blocks the switch.
The backup is stored beside the database as
`<database>.pre-protocol-v2-<baseline-hash>`. It contains secrets and follows the
same storage/access policy as the original database.

## Preview and repair

Authenticated management endpoints:

| Method | Path | Result |
| --- | --- | --- |
| GET | `/api/admin/protocols/migration` | Committed receipt, or `null` |
| POST | `/api/admin/protocols/migration/preview` | Definitions, reports, bindings, diagnostics, baseline |
| POST | `/api/admin/protocols/migration/apply` | Atomically committed receipt |
| POST | `/api/admin/protocols/reload` | Reload current-engine verified active revisions |

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

After repair, apply the reviewed preview and reload. A reload before the
migration receipt exists cannot enable generation. Restart checks the receipt
and reloads current-engine evidence; it does not repeat the migration or
overwrite later edits. Evidence from an older compiler must be regenerated.

For executable rollback, stop the new server, restore the retained executable,
configuration, database and matching key as a consistent set. Keep the current
database separately if post-upgrade changes are needed. A Git revert is not a
database downgrade and does not restore usage written after migration.

## Evidence and staged boundary

`TestProtocolUpgradeStartupAndRestart` uses the production constructor and
checks that the backup predates preset seeding. Service/API tests cover edited
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

C16 still removes the historical public-handler branches, Agent request
boundary, legacy editor/discovery and template execution implementation.
Migration currently stores full binding-combination reports: an incompatible
full capability contract blocks that ingress/target pair even if a narrower
request would be expressible. Conditional route contracts and final migration
surface checks remain part of the subsequent cutover; they must not be
implemented by ignoring a failed report or deleting input capabilities.
