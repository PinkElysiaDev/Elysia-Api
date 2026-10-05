# Protocol definition v2 — implementation reference

Status: the v2 runtime serves public/custom ingress, upstreams, the editor,
Agent, previews and probes. C16 removes the legacy execution kernel. See
[cutover contract](protocol-cutover-v2.md) and the implementation ledger for
validation status; provider validation is independent of offline tests.

## Definition and execution

`backend/protocol.Definition` is the configuration contract. `schemaVersion: 2`
is independent of the semantic document's `schemaVersion: 1`, the author's
`version`, and `CompilerVersion`. `DescribeSchema()` generates the definition
and semantic schemas together with the operation and capability catalogs.

Each direction is independent: `decode_request`, `encode_request`,
`decode_response`, `encode_response`, `decode_event`, `encode_event`. A mapping
selects one registered `module`, one `transform`, or event `rules`. A module can
have an `after` expression to construct a different target layout. In `after`,
`input` is the module output and `root` is the original semantic input. There
is no inverse-map generation and no script evaluation.

The compiler registers `openai-chat`, `responses`, `anthropic`, and `gemini`
from `backend/protocol/builtin`. Each implements request/response and upstream
stream event decoding/client encoding against the ordered semantic model.
Declarative protocols can implement all directions without using any module.
Client WebSocket events additionally use `decode_client_event` and
`encode_upstream_event`. See the [session](protocol-sessions-v2.md) and
[task](protocol-jobs-v2.md) references for their distinct lifecycle contracts.

Minimal object construction:

```json
{
  "op": "object",
  "fields": {
    "deployment": {"op": "read", "path": "/model"},
    "history": {"op": "read", "path": "/content"}
  }
}
```

Expressions support `read`, `literal`, `omit`, `object`, `array`, `map`,
`flatmap`, `filter`, `choose`, `if`, `enum`, `cast`, `merge`, `concat`, `join`,
`sort`, `associate`, `exists`, `equal`, `all`, `any`, `not`, `parse_json`,
`stringify_json`, and compile-time `ref`. Paths use RFC 6901 JSON pointers.
Array iteration preserves input order. Sort is stable and association rejects
duplicate identities. Merge requires an explicit collision policy.

Missing, null, false, zero, empty strings and empty containers are distinct.
Read of a missing field yields omission unless `required` is true. Array
operations require an array; use an explicit `if`/`exists` to map optional
arrays. Invalid JSON tool input is an error, not an empty object. Native JSON
numbers keep their representation; exact numeric comparisons are bounded to
avoid unbounded allocation from huge exponents.

All configuration objects reject unknown keys. Only `extensions` is inert
author metadata. Named expression recursion, unused references, invalid
schema paths, statically incompatible types, uncovered declared enum branches
and increased engine limits are compilation errors. Dynamic inputs are also
checked against their schemas. Limits bound depth, nodes, state, edits and
buffered bytes. The runtime checks cancellation during traversal.

Event rules require `unknownEvent: "reject"`. Ignorable wire frames need an
explicit rule yielding an empty array. An HTTP status alone is never a
semantic terminal event. `EventReplay` and `SessionReplay` handle association and lifecycle;
the immutable compiler stores no per-request mutable state.

## Cache intents and capabilities

A request's `cache` list holds cache intents; `Kind` selects the policy and is one of:

- `breakpoint` — an Anthropic-style `cache_control` marker on a request, block or
  tool node. Only an Anthropic target emits it.
- `key` — a cache key (`prompt_cache_key`), Chat/Responses.
- `retention` — the maximum-retention setting (`prompt_cache_retention`),
  Chat/Responses. It is decoded for compatibility even though the provider marks
  the field deprecated.
- `resource` — an explicit cache-resource reference (`cachedContent`), Gemini.
- `mode` — the `explicit`/`implicit` selector carried inside the
  `prompt_cache_options` object, Chat/Responses.
- `options.ttl` — the minimum-lifetime string carried in the same
  `prompt_cache_options` object, Chat/Responses. It is independent of `retention`
  (a maximum lifetime) and of a breakpoint TTL; keep the three settings separate
  even when they travel together.
- `prewarm` — a request to populate the cache before the first read, Responses
  only.

An unknown `Kind` is rejected explicitly rather than dropped. The capability
catalog adds `cache.options` (covering `mode` and `options.ttl`) and
`cache.prewarm` beside the existing `cache.breakpoints`, `cache.keys`,
`cache.retention` and `cache.resources`. Cache capabilities are declared
**per-direction**: an encoder checks the declaration of the direction it is
encoding, not only the definition's top-level `capabilities`.

## Native preservation

`native.preserve: true` requires the corresponding decoder for each encoder.
Compatible family, wire version, direction and resource scope authorize
native replay; a matching protocol ID or `type` string does not.

The runtime compares original and updated mapped values and applies changes
to the native document. Unchanged extensions, explicit nulls and original
array order survive. Removed fields are deleted. Clearing tools does not replay
the original tools. Foreign protocols receive only explicitly mapped fields.

For arrays with multiple modified items, declare stable item keys:

```json
{"preserve": true, "arrayKeys": {"/tools": "/name", "/history": "/id"}}
```

Keys must exist and be unique. A changed key denotes a replacement item.
Unambiguous single edits and unchanged items can be matched without keys;
Fully mapped subtrees can be replaced directly; ambiguous associations of
unmapped native fields are rejected. Pointer paths are exact, not wildcards.
Native-preserving event mappings currently require one semantic event per
wire frame; compound response/item events can retain the complete frame.
Multiple semantic outputs with native replay enabled fail explicitly.

Provider/account/model/session resources are stamped from gateway context,
including nested content, tools and cache references. Ordinary expressions
cannot replace their declared scope. Same-wire opaque resources still require
compatible scope when replayed.

## Legacy import and staged boundaries

`relay.ImportLegacyProtocol` returns a draft plus located blocking issues.
It imports a proven subset of annotated request bodies and response modules,
preserving target field paths, credentials and literal JSON. Unproven string
templates, conditional defaults, aliases, model discovery, response overrides
and stream rules remain explicit migration issues. The original configuration
is retained in inert `extensions.legacyImport` for repair.

Import is not activation. It neither executes an old template inside v2 nor
substitutes a simpler protocol to make migration appear successful. C15
implements graph preview, repair, backup and transactional cutover. The old
execution runtime has been removed; import types only preserve old definitions.

WebSocket and asynchronous task operations are installed in the gateway
compiler. Declaring an engine feature or capability is not verification.
Activation requires hash-bound offline evidence; real-provider evidence is
recorded independently.

## C07 evidence

- Two independent definitions, without built-in shapes, convert ordered
  messages, JSON tool calls/results, responses and events in both directions.
- Four built-in HTTP modules pass 16 request and 16 response combinations,
  including tools, exact long integers and nonzero cache reads.
- Native preservation tests cover unknown extensions, renames, deletions,
  explicit zero/null, scoped resources, event edits and keyed array reorder.
- Module request tests reproduce the Responses custom-tool history case using
  an arbitrary definition ID and retain ordinary messages around tool items.
- Negative tests cover unknown keys, impossible branches, invalid types,
  recursion, unsupported transports/modules, invalid events, cancellation and
  unsafe import. Diagnostics locate the innermost failed expression.
- Backend full tests and `go vet` pass. No frontend files changed in C07.

New matrix failures identified a legacy floating-point conversion in
Anthropic/Gemini request tool arguments and Gemini response arguments:
`9007199254740993` became `9007199254740992`. The shared wire JSON decoder now
retains `json.Number` at those boundaries. The corresponding matrix assertions
failed before these changes and pass afterward. This finding has not been
independently reproduced against v1.4.0; no historical attribution is claimed.

These are local offline tests. They do not establish actual provider support,
online cache hit rates, completed editor/Agent integration, or final migration.
