# Protocol definition v2 — implementation reference

[Documentation index](README.en.md) · [Authoring reference](protocol-definition-reference.en.md)

The runtime serves public/custom ingress, upstreams, the editor, Agent, previews and probes. This reference describes executable definitions; see the [runtime contract](protocol-cutover-v2.md) for shared limits.

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

## Legacy import

`relay.ImportLegacyProtocol` returns a draft plus located blocking issues.
It imports a proven subset of annotated request bodies and response modules,
preserving target field paths, credentials and literal JSON. Unproven string
templates, conditional defaults, aliases, model discovery, response overrides
and stream rules remain explicit migration issues. The original configuration
is retained in inert `extensions.legacyImport` for repair.

Import is not activation. It neither executes an old template inside v2 nor
substitutes a simpler protocol to make migration appear successful. The migration service provides graph preview, repair, backup and transactional cutover. The old
execution runtime has been removed; import types only preserve old definitions.

WebSocket and asynchronous task operations are installed in the gateway
compiler. Declaring an engine feature or capability is not verification.
Activation requires hash-bound offline evidence; real-provider evidence is
recorded independently.

## Validation

Fixtures must cover declared capabilities, native extensions, field presence, exact numbers, edits and negative inputs. Two independent mapping examples are `backend/protocol/testdata/text-alpha.json` and `text-beta.json`. Offline success does not establish provider acceptance; see [validation](protocol-release-readiness.en.md).
