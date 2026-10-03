# Custom protocol definition reference (v2)

[简体中文](protocol-definition-reference.md) · [User guide](protocol-guide.en.md) · [Detailed implementation](protocol-definition-v2.md)

The configuration format is `schemaVersion: 2`; semantic documents use version 1. Author version, content hash and compiler version are independent. Legacy request/response/shape templates are import-only. Read the live contract with `GET /api/admin/protocols/schema` or `elysia protocol schema`: [Definition](../backend/protocol/definition.go), [semantic model](../backend/protocol/model.go), [compiler](../backend/protocol/compiler.go).

## Complete examples

Import [text-alpha](../backend/protocol/testdata/text-alpha.json) or [text-beta](../backend/protocol/testdata/text-beta.json) into the editor. Both are complete standalone definitions. Tool/multi-turn examples are exercised in [compiler tests](../backend/protocol/compiler_test.go). Adjacent session and job fixtures demonstrate WebSocket and asynchronous operations. A mapping fragment alone is not an activatable definition.

## Definition fields

| Field | Contract |
| --- | --- |
| `id`, `name`, `version` | Identity, display name and author version; ID is not semantic provenance |
| `family`, `wireVersion` | Native compatibility identity, further constrained by direction and resource scope |
| `requires` | Required installed engine features; declaring a feature cannot install it |
| `capabilities` | Verifiable capabilities, optionally narrowed per direction |
| `directions` | Independently authored encoders/decoders; no mechanical inversion |
| `operations` | Relative endpoints, methods, transport, credential placement, input schema and workflow configuration |
| `expressions` | Compile-time reusable expressions; recursive references rejected |
| `native` | Preservation policy and stable array identities when required |
| `limits` | May narrow engine depth/node/state/buffer limits |
| `samples` | Mapping fixtures, expected output/errors, event sequences and capability evidence |
| `sessionSamples`, `taskSamples`, `modelSamples` | Workflow evidence |
| `agent` | Parameter policy, tool result input type, thinking levels and examples |
| `extensions` | Explicit inert metadata area; unknown configuration keys elsewhere fail |

Directions: `decode_request`, `encode_request`, `decode_response`, `encode_response`, `decode_event`, `encode_event`, `decode_client_event`, `encode_upstream_event`. Partial implementations are permitted only for matching uses.

## Expressions

Each mapping selects a `module`, `transform` or event `rules`. Modules are `openai-chat`, `responses`, `anthropic`, `gemini`. An explicit `after` uses module output as `input` and original input as `root`. `initial` emits one declared prefix before the first nonempty decoded event. Mapping input/output schemas and operation input schemas are independently enforced.

Encoding fragment using JSON Pointers:

```json
{"op":"object","fields":{"deployment":{"op":"read","path":"/model"},"history":{"op":"read","path":"/content"}}}
```

Operations: `read`, `literal`, `omit`, `object`, `array`, `map`, `flatmap`, `filter`, `choose`, `if`, `enum`, `cast`, `merge`, `concat`, `join`, `sort`, `associate`, `exists`, `equal`, `all`, `any`, `not`, `parse_json`, `stringify_json`, `strip_prefix`, `ref`. Sources are `input`, `item`, `root`, `context`; obtain exact allowed parameters from the schema.

Missing/null/false/zero/empty values remain distinct. `exists` includes null and zero. `join(source)` requires strings; casts cannot truncate numbers. Unknown branches fail. Invalid function arguments are not replaced by `{}`. Iteration only traverses input data; there is no executable script or recursive expression facility.

## Cache, errors and events

A breakpoint is `{"kind":"breakpoint","location":"block","value":{"type":"ephemeral"},"ttl":"1h"}`. Do not also put TTL inside `value`. Request, tool and block scopes are mapped independently. Unknown fields and undeclared caching policy are not automatically added. Resource references retain provider/account/model/session scope.

Usage input/output/total/cacheRead/cacheCreation are optional counters with `count` and `origin` (`observed` or `inferred`). Explicit zero is `{"count":0,"origin":"observed"}`; an absent counter is different. Declare alias priority with `exists` and conditional expressions.

Built-in error semantics use message/category/code/param/details; unrepresentable provider details fail conversion. Failure mappings can read `context.httpStatus`. Usage tails may follow a stream terminal. Unknown events are rejected; deliberately ignored frames require an explicit empty-array rule. HTTP `framing.idleMillis` defaults to 120000, accepts 1–120000, and limits idle reads rather than total stream duration.

## Management API

All paths below use the `/api/admin/protocols` namespace and existing management authentication:

| Method/path | Purpose |
| --- | --- |
| `GET /schema`, `/enabled` | Runtime catalog, active revisions |
| `POST /validate`, `/preview`, `/test` | Compilation, shared preview, separate real-target evidence |
| `GET` / `PUT /:id/draft` | Exact-JSON draft reads/writes |
| `POST /:id/verify` | Immutable revision and offline evidence |
| `GET /:id/revisions`, `/:id/revisions/:hash` | Revision history/content |
| `POST /:id/revisions/:hash/verify` | Reverify against the current engine |
| `POST /:id/activate`, `/:id/rollback` | `{ "revisionHash":"...", "expectedActive":"..." }`; valid evidence required |
| `GET /:id/diff` | Compare two revisions |
| `POST /combinations`, `GET` / `PUT /bindings` | Composition evidence and model/source/group contracts |
| `POST /reload` | Atomic replacement after successful compilation |
| `GET /migration`, `POST /migration/preview`, `/migration/apply` | Upgrade preview and transactional cutover |

Preview modes: mapping/session/task/models/agent. Exact payloads are defined by the [handlers](../backend/server/protocol_revision_admin.go) and [PreviewInput](../backend/protocol/authoring.go). Missing coverage or stale hash/compiler evidence blocks activation; uploading `passed:true` cannot authorize it.
