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

`SnapshotProtocolRequest` provides the temporary legacy boundary. It restores
Anthropic block order from parser positions and treats Responses InputItems as
authoritative when the old object contains both representations. Its input has
already passed through the old parser, so it cannot promise to recover lost
number precision. `DecodeProtocolSnapshot` also retains the original bytes;
the new adapters will parse those bytes directly. Both transitional helpers are
scheduled for removal when production callers move to the new engine.

The versioned decoder rejects unknown contract keys and extra JSON documents.
Semantic capability and association validation is a separate compilation and
verification responsibility, not implied by successful JSON decoding.
