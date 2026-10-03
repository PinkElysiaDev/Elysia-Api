# C16 runtime cutover contract

The gateway, native Agent, health checks, model discovery, CLI, MCP and editor
now consume pinned v2 definitions. `backend/relay` retains HTTP/WebSocket
transport and the legacy definition importer; it no longer executes the old
Maheshvara converters or string-template interpreter. Historical types are
read-only import/hash inputs. A failed migration leaves management available
for repair and prevents inference using an unverified graph.

## Changes protocol authors must account for

- `Mapping.initial` emits a declared prefix once, before the first nonempty
  decoded event frame. It does not invent a prefix when no frame arrives.
- `join` accepts either `items` or `source` (an array of strings). It never
  coerces objects or free-text tool input into JSON arguments.
- `Operation.input` checks the final wire body. Definition and binding
  verification also exercise operation requirements. Combination evidence is
  conservative across a definition's generate/submit operations; use separate
  definitions when operations require incompatible request contracts.
- `models` operations declare their own decoder, pagination and `modelSamples`.
  Agent calls declare their parameter policy in `agent`; they cannot change
  messages, tool permissions, credentials or gateway authorization.
- `framing.idleMillis` limits each upstream idle read, including response
  headers. Zero selects 120000 ms. The compiler accepts explicit values from
  1 through 120000 ms. This is independent of total stream duration.
- Response mappings can read `context.httpStatus` for HTTP failures. A non-2xx
  body mapped to a successful response violates the upstream contract.

## Cache and native edits

A breakpoint has exactly one TTL field in the semantic document:

```json
{"kind":"breakpoint","location":"block","value":{"type":"ephemeral"},"ttl":"1h"}
```

Decoders extract `cache_control.ttl`; encoders join it back at the declared
request, block or tool location. Remove `ttl` to remove the wire field. Explicit
null remains null. Putting `ttl` inside `value` is rejected to avoid two sources
of truth. Move it to the sibling field in edited semantic samples and mappings
before reverifying an older custom definition. No intent means no added policy.
Chat copies must explicitly declare and demonstrate `cache.breakpoints` when
they carry Anthropic-style block extensions. Resource references and cache keys
remain different capabilities with different provider/account constraints.

Native reconciliation preserves original values for unchanged subtrees and
uses edited values for fully mapped subtrees. Arrays carrying unmapped fields
need stable declared identities when several items change. Ambiguous edits
fail instead of attaching one message's extensions to another.

## Errors, retries and accounting

Built-in failures use semantic `message`, `category`, `code`, `param` and
`details`. Provider-only fields remain source-scoped extensions. Unrepresentable
details fail conversion. A successful HTTP status containing a failed generation
is a gateway failure and is never retried as if the provider returned HTTP 502.
Only observed retryable HTTP rejections before stream output allow retries.
Late stream errors emit the client's supported failure event and set the
`X-Elysia-Stream-Error` trailer; they never emit a fabricated successful terminal.

Observed zero counters remain distinct from absent counters. Input totals
include cache reads/creation where required by the provider's usage semantics.
Estimates count Unicode characters and declared file/input budgets; estimates
are only recorded when enabled. Model health uses the same current credentials
and model permissions as routing. Missing probe evidence and HTTP 429 do not
change health; empty HTTP 200 and unsupported paths do not imply success.

Known Responses hosted calls are counted by source identity and output index,
with a bounded deduplication map. Declared usage details override observation,
including explicit zero: `tools.web_search_calls`, `tools.file_search_calls`,
`tools.image_generation_calls`, `tools.code_interpreter_calls`, and
`tools.computer_use_calls`. Session and task protocols can map the same details
in their per-response/task usage; arbitrary native event names are never guessed.

Historical vendor usage aliases must be explicitly mapped. The retired
guess-by-field fallback is not a protocol contract. Map the documented source
counter to `usage.cacheRead` with `origin: "observed"`; use `exists` to choose an
alias, so zero is not replaced by a different counter. Missing counters should
remain absent. See the definition expression catalog for typed construction.

## Upgrade and rollback

Compiler `2.0.0-dev.8` and preset author version `2.1.0` require fresh evidence.
Known unedited prior presets are identified by complete content fingerprints,
not IDs. Edited active definitions are reverified as authored; edited drafts
and intentionally disabled definitions remain untouched. All reports and model
combinations must pass before a backup and atomic graph update. Concurrent
configuration changes invalidate the prepared baseline. The new receipt is
stored as `protocol_runtime_refresh`; the original migration receipt remains.

Back up the database, encryption key and configuration and retain the old
executable. Restore that matching set to roll back a runtime/database upgrade;
`git revert` alone does not reverse persisted revisions or migrations.

## Evidence

C16 regression suites cover the 96-case cache HTTP matrix, arbitrary-ID and
standalone custom protocols, tool histories, usage tails, exact JSON presence,
runtime refresh, TTL edits at five scopes, failure mapping/retry boundaries,
model discovery, Agent calls and health probes. Chromium tests exercise the
real isolated Go/SQLite service for authoring, verification, activation, rollback
and exact-JSON Agent/editor handoff. Final commands and performance/stress results
are recorded in the implementation ledger and C17/C18 acceptance report.

These are local protocol tests. They do not establish provider-side cache hits,
model equivalence, or compatibility with mechanisms outside the declared engine.
