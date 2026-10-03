# Semantic contract, version 1

The Go contract lives in `backend/protocol`. Its `schemaVersion` is independent
of a protocol definition's schema or user-selected revision. C03 introduces
this contract; it does not yet switch production forwarding to the new engine.

`Request.content` is the sole ordered history. A message contains ordered
children, including tool calls and results where they originally appeared.
`Response.content` and event snapshots use the same node types. There is no
second messages/input-items projection that can drift independently.

JSON-valued fields use immutable `Value`. Omitted fields are absent; present
`null`, `false`, `0`, `""`, `[]` and `{}` remain distinct. Raw JSON numbers are
retained exactly. Missing values cannot be serialized as array entries or map
values, where they would otherwise be confused with null. Interface decoding
uses `json.Number`.

| Contract | Purpose |
| --- | --- |
| Identity | Wire family/version, definition ID, pinned revision |
| Provenance / Native | Source direction, JSON pointer, resource scope and original JSON |
| Node | Ordered messages, media, reasoning, calls, results or opaque native content |
| Tool / ToolInput | Function JSON, free text, server execution or opaque definitions |
| CacheIntent / Resource | Cache policy separated from provider/account/model/session resource identity |
| Counter / Usage | Missing versus observed zero versus inferred count; cache read/create separated |
| Event / Media | Lifecycle, deltas, snapshots, correlation and encoded media references |
| Task | Pinned upstream identity, status, cancellation and settlement identity |

Native is an original snapshot, not an alternative editable semantic history.
Unknown extensions belong there until an explicit mapping gives them semantic
meaning. Named Parameters/Attributes are not authorization to copy unknown
fields to a different vendor. Preservation and modification rules are C04.

Production adapters now parse original wire JSON directly. The transitional
snapshot/projection helpers and old conversion runtime have been removed.
Cache breakpoint `ttl` has one owner beside `value`; `value` contains the
remaining policy object. Missing TTL deletes the wire field, explicit null
remains null. See [cutover](protocol-cutover-v2.md).

The versioned decoder rejects unknown contract keys and extra JSON documents.
Semantic capability and association validation is separate from JSON decoding.

## Preservation and diagnostics (C04)

`PreserveNative` requires matching nonempty family/wire version and compatible
decode/encode directions. Definition IDs and revisions may differ when the wire
contract matches. Scoped native content additionally requires matching resource
restrictions. Cross-wire adapters construct declared mappings; they cannot use
this API to forward opaque payloads.

`ApplyMutations` is transactional: `set` may create a final object key, `replace`
and `delete` require an existing target, and parents must already exist. Paths
are RFC 6901 JSON pointers. Array deletions retain remaining element order; set
and replace require valid existing array indices. No-op replay returns the
original JSON. Failed edits return no partial result. Edits never modify the
original snapshot. Depth, node count, patch count and bytes are bounded by the
engine limits.

`CheckRequest` checks capabilities, opaque provenance, cache/resource scopes,
input kinds and call/result association. A text-only target rejects tool data;
it does not strip it. Account resources require provider/account provenance.
These checks now have structured `ConversionIssue` results with stable code,
direction, stage, capability and semantic field path. The C02 native tool path
already consumes this shared preservation implementation; other relay paths
move to it in C05.

Verification reports carry separate offline/upstream kind and bind definition,
compiler and sample hashes. This is the report contract, not the C08 verifier or
C09 activation gate; those remain tracked as pending in the implementation log.
