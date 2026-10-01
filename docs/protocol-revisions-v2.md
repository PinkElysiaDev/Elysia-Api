# Protocol revisions and atomic activation

C09 separates editable drafts, immutable definitions, verification evidence and
active pointers. SQLite stores these in `protocol_drafts`, `protocol_revisions`,
`protocol_verification_reports` and `protocol_activations`. It retains previous
definitions and reports instead of rewriting them during rollback.

`protocol.Service` is the shared authoring/registry boundary. Save uses the
draft's current hash; activation uses the current active revision hash. An
empty expected hash means creation, so a stale editor cannot silently replace
an existing draft or activation. Verification pins the selected draft snapshot
and produces a content-addressed immutable revision. A later edit does not
alter that revision's evidence or automatically activate the new content.

Activation compiles the stored revision and checks current-engine offline
evidence before committing its pointer. It then publishes a new immutable
registry snapshot. A request calls `Pin` once and keeps that compiled pointer;
subsequent activation/reload cannot mutate its adapters. Request lookup uses
no database access or compilation. Activation/reload serialize administrative
writes, while readers use an atomic snapshot.

Reload compiles the whole persisted active set before publishing anything.
Failure retains the previous in-process snapshot; on a fresh start the
registry remains empty. Management endpoints stay available for repair.
Rollback uses the same compile/evidence checks as activation. An old report
from another compiler version is insufficient; `VerifyRevision` can produce
fresh evidence without overwriting the user's draft.

## Management API

These routes inherit the existing admin authentication and authorization.

| Route under `/api/admin/protocols` | Behavior |
| --- | --- |
| `GET /` | Drafts, persisted active pointers and actually loaded hashes |
| `GET /schema` | Current compiler schemas, modules, capabilities and diagnostics |
| `POST /validate` | Strictly compile the supplied definition |
| `POST /preview` | Execute typed request/response/event mapping and show provenance |
| `POST /combinations` | Verify two supplied protocol definitions together |
| `GET /:id/draft` | Read draft and its `ETag` |
| `PUT /:id/draft` | Save JSON with `If-Match`; missing header creates only |
| `POST /:id/verify` | Verify the supplied `draftHash` |
| `GET /:id/revisions` | List immutable history |
| `GET /:id/revisions/:hash` | Read a revision and current offline report |
| `POST /:id/revisions/:hash/verify` | Reverify an immutable revision |
| `GET /:id/diff?from=...&to=...` | Review field changes; arrays are replacements |
| `POST /:id/activate` | Activate `revisionHash` using `expectedActive` |
| `POST /:id/rollback` | Apply the same guarded activation to an older revision |
| `POST /reload` | Atomically reload persisted active definitions |

The API generates verification reports itself; a client-supplied `passed`
report cannot authorize activation. Body size and unknown request keys are
checked. Revision conflicts return HTTP 409. Invalid definitions and failed
verification return structured diagnostics; missing revisions return 404.

The old custom-protocol PUT endpoint recognizes versioned definitions and
saves them through this service as drafts. It cannot install a v2 definition
in the legacy registry or overwrite its draft with a v1 payload. The legacy
v1 execution path remains temporarily available until C15/C16; C10 connects
the new pinned registry to custom gateway ingress and upstream routing.

## Evidence

SQLite/service tests cover save-versus-activation separation, optimistic edit
conflicts, in-flight revision stability, failed activation, reload failure,
corrupt stored content, restart restoration, rollback, stale compiler reports
and re-verification. Concurrent readers decode/encode while activations and
reloads replace snapshots. Management tests exercise save → verify → activate,
preview provenance, listing/history, legacy endpoint gating and forged-report
rejection. Backend full tests and vet pass; local race execution remains
pending the C17 Linux environment.

Two complete HTTP JSON examples, with independent field layouts and no built-in
modules, are available in `backend/protocol/testdata/text-alpha.json` and
`text-beta.json`. Their declared scope is text only. They are offline fixtures,
not evidence of compatibility with a real hosted service.
