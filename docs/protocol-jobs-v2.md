# Persistent generation jobs

The gateway supports HTTP JSON asynchronous generation through declared
`submit`, `status`, `result` and optional `cancel` operations. Jobs fix the
ingress/upstream revision hashes, model, source and credential account identity
before submission. The gateway does not execute client business tools.

## Definition

The submit operation owns `task.status`, `task.result` and `task.cancel`
references. Control endpoints use the `{taskId}` placeholder. Requests and
finished results use the ordinary independent request/response adapters.
`task.decode`, `task.encode` and `task.control` use the same bounded declarative
mapping compiler as those adapters; no expressions are parsed on each poll.

- `decode`: provider receipt to `{id,status,usage?,error?}`.
- `encode`: that semantic receipt to the client wire shape. Client IDs are
  gateway job IDs, never upstream job IDs.
- `control`: `{id}` to `{body?,query?}` for a fixed linked operation. Dynamic
  query fields cannot override static fields or authentication.
- `context.operation`: gateway-owned submit/status/result/cancel discriminator.
- `idempotencyHeader`: optional provider header, populated with the gateway job
  ID. It does not enable automatic retry of uncertain submissions.

The supported provider statuses are queued, running, completed, failed and
cancelled. Gateway receipts additionally support submitting and uncertain.
Missing counters stay missing; observed zero and cache read/create counters
retain their meaning. Result responses use the shared capability checks.

Complete standalone examples with different receipt field names are
`backend/protocol/testdata/job-alpha.json` and `job-beta.json`. The current task
flow contract requires request and result mappings in both directions.
`taskSamples` asserts decode/encode/control outputs and receipt roundtrips.
Missing state, operation or usage evidence prevents activation. Reports use
compiler `2.0.0-dev.5`; earlier revisions must be reverified for this compiler.

## Client workflow

POST the declared submit path in `/gateway/:protocolId/*path`. The gateway
returns HTTP 202 and a receipt in that protocol's declared shape. `Location`
contains `/gateway/:protocolId/_jobs/:jobId`, which survives active revision
changes. GET that location reads status, GET its `/result` child retrieves the
finished result, and DELETE requests cancellation. Native declared control
paths are also accepted when they match the job's pinned ingress operation.

Every operation requires the original authenticated owner and current model
group authorization. Cancellation acknowledges intent until the provider's
state confirms cancellation; completion can win the race. Repeated result
queries read the durable result and do not repeat generation or settlement.

An optional client `Idempotency-Key` is scoped to the owner and ingress. Reusing
it for a different semantic request returns HTTP 409. With no key, each request
reserves a new job identity. Lost submissions must be inspected using the
returned identity; blind resubmission can create another provider generation.

Synchronous bridging requires an explicit model binding `operation` selecting
the submit flow and `wait: {timeoutMillis,onTimeout}`. `onTimeout` must be
`cancel` or `continue`; the cancel option requires provider cancellation.
Timeout and disconnected waiters use that policy. A wait timeout returns 504
with Location; an uncertain submit returns an error and Location. Native async
submissions always return a receipt rather than waiting implicitly.

## Durability and boundaries

Reservation precedes network submission. Submission timeout, invalid provider
receipt or an abandoned submitting lease becomes uncertain and is never
automatically resubmitted. Restart only resumes known polling, result fetch,
cancellation and settlement. Missing pinned model accounts/revisions stop
progress with diagnostics; jobs never switch accounts or adopt newly activated
mappings. Already compiled inactive revisions have a bounded shared cache.

Leases and compare-and-swap revisions prevent stale writes. A concurrent cancel
intent can be merged onto the exact leased poll result. Terminal updates and a
settlement outbox share a SQLite transaction. Usage delivery uses an idempotent
settlement ID; acknowledgement happens only after the usage write succeeds.
Failure in the write/acknowledgement gap therefore does not double count usage.

The store applies its encryption policy to operational job state and results.
Submitted prompts and credentials are not stored in job records. Raw provider
HTTP failures are not copied into diagnostics. Provider errors explicitly
selected by the task mapping are preserved for the owning client. Jobs are
operational data, separate from optional request-body logging.

There is no provider-side job discovery, automatic retry of uncertain submits,
or implicit synchronous/asynchronous bridging. Provider validation remains
separate from the local evidence below.

## Local evidence

Protocol tests verify two independent shapes, paired receipt conversion,
unknown/invalid states, missing coverage and changed identities. Real SQLite
tests cover restart, owner boundaries, deduplication conflicts, uncertain and
abandoned reservations, cancellation/poll races, immutable identities, cache
counters and crash-after-write-before-acknowledgement. Local HTTP gateway tests
cover model/ID mapping, immutable revisions after activation, repeat result
reads, one-time persisted usage, uncertain responses without replay, and both
explicit waiting policies. Full backend tests and vet pass. These fixtures do
not establish compatibility with any live provider.
