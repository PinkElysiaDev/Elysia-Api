# Custom protocol package reference (v4)

[简体中文](protocol-definition-reference.md) · [User guide](protocol-guide.en.md) · [Detailed implementation](protocol-definition-v2.md)

The package format is `{ "schemaVersion":4,"spec":...,"mapping":... }`; Mapping uses DSL2 and the central Maheshvara core2 contract. Author version, revision hash and compiler version are independent. Retired formats are neither imported nor executed. Read the live contract with `GET /api/admin/protocols/schema` or `elysia protocol schema`. See [ProtocolPackage](../backend/protocol/protocol_package.go), the [core model](../backend/protocol/model.go) and the [three-part contract](protocol-three-part-contract.md).

Each Mapping connects only its own external protocol to and from Maheshvara. Provider pairs compose through the core; they do not have dedicated converters.

## Runtime value-schema validation

The compiler translates the restricted `ValueSchema` language into draft 2020-12 schemas and precompiles them with **santhosh-tekuri/jsonschema/v6 v6.0.3**. Mapping, operation-input and model-page validation reuse revision-owned validators. `nullable` accepts null even when enum is present; exact numbers and last-decoded duplicate keys retain their semantics. Application-owned decoding uses Sonic with `UseNumber`.

**Migration compatibility:** compiler version `2.0.0-dev.73` requires renewed verification evidence. Schema-validated values (including unknown descendants and enum entries) now enforce the numeric conversion bound: at most 4096 bytes per number spelling and exponent in -4096…4096. Complete-value byte/node/depth budgets run before decoding. Diagnostics use library error trees and escaped instance paths; message wording and error ordering can change. See [the migration report](backend-runtime-schema-migration.md) for scope, benchmarks and validation.

**Request-budget correction:** compiler version `2.0.0-dev.74` makes operation input validation use the pinned request-body budget (32 MiB by default), matching request decoding and encoding. Explicit protocol limits still impose a tighter cap; response and event budgets remain independent. Existing verification evidence must be regenerated for the corrected validation behavior.

## Generated JSON Schema

The live catalog uses **`github.com/invopop/jsonschema` v0.14.0** to reflect the Go models into JSON Schema draft 2020-12. It provides package, Spec, Mapping, binding, conversion-policy and semantic schemas; retired definitionSchema and modules entries are absent. Structs reject unknown properties; JSON `omitempty` and `omitzero` tags control required fields. The protocol's `Value` type is explicitly mapped to an unrestricted JSON schema, retaining arbitrary JSON values, including null, false, zero, and large numbers. Cache-intent descriptions use the library's `jsonschema_description` tag.

### Schema representation migration

Clients inspecting raw schema JSON must handle these standard, validation-equivalent representations:

- An unrestricted schema can be the boolean `true`, rather than `{}`.
- Named maps such as `Object` and `CapabilitySet` use local `$defs` / `$ref` entries rather than always appearing inline.
- Empty `required` arrays are omitted. Optional fields remain optional, including fields using `omitzero`.
- Binding and conversion-policy roots now also declare `$schema`. Automatic package-derived `$id` values are disabled, preserving local-reference resolution.

The catalog envelope and its semantic roots (`request`, `response`, `event`, `task`, `models`, `agent`) remain available. `semanticSchema` groups those roots under a shared `$defs`; to validate one root, combine its `$ref` with that shared `$defs` and dialect. CLI `--section` / `--type` queries continue to expose named definitions, including the newly named maps.

All existing catalog constraints matched the pre-migration output after normalizing the equivalent forms above. Each catalog call returns fresh objects. Application-owned serialization uses Sonic; the dependency owns its custom schema serialization. Runtime protocol checks, resource budgets, and action-specific constraints continue to be enforced by the compiler.

## Complete examples

[text-alpha](../backend/protocol/testdata/packages/text-alpha) and [text-beta](../backend/server/testdata/protocols/text-beta) each contain a Spec/Mapping pair. Combine the pair into a complete v4 package before saving. Incomplete fragments cannot be activated.

## Package fields

| Field | Contract |
| --- | --- |
| `spec.id/name/version` | Protocol identity, display name and author version |
| `spec.family/wireVersion` | External contract identity; scope and provenance still apply |
| `spec.requires` | Required implemented engine features |
| `spec.documents/constraints` | External documents, reusable assertions and stream lifecycle |
| `spec.operations` | Paths, methods, transports, credentials and workflow contracts |
| `mapping.coreVersion/dslVersion` | Core and DSL versions, both 2 |
| `mapping.capabilities` | Verifiable capabilities, optionally narrowed by direction |
| `mapping.directions` | Independently authored wire/core directions |
| `mapping.operations` | Direction selections and task/model/token-count programs |
| `spec.expressions`, `mapping.expressions` | Independent compile-time symbol tables |
| `mapping.native` | Preservation policy and stable array identities |
| `mapping.conversionDefaults` | Named target dispositions of core intents |
| `mapping.limits` | Resource limits no greater than the engine limits |
| `mapping.samples` and workflow samples | Exact results, rejection cases and capability evidence |
| `mapping.agent` | Agent parameters, client output intents and examples |

Directions: `decode_request`, `encode_request`, `decode_response`, `encode_response`, `decode_event`, `encode_event`, `decode_client_event`, `encode_upstream_event`. Partial implementations are permitted only for matching uses.

## Expressions

Each direction selects its Spec `document`, a DSL `program`, and semantic `effects`. Streams can additionally declare bounded state, incremental programs and finalizers. There are no module, after, or legacy transform/rules execution modes. External, core and final wire checks validate different boundaries.

Encoding fragment using JSON Pointers:

```json
{"op":"object","fields":{"deployment":{"op":"read","path":"/model"},"history":{"op":"read","path":"/content"}}}
```

Operations: `read`, `literal`, `omit`, `object`, `array`, `map`, `flatmap`, `filter`, `choose`, `if`, `enum`, `cast`, `merge`, `concat`, `join`, `sort`, `associate`, `exists`, `equal`, `all`, `any`, `not`, `parse_json`, `stringify_json`, `strip_prefix`, `ref`. Sources are `input`, `item`, `root`, `context`; obtain exact allowed parameters from the schema.

Missing/null/false/zero/empty values remain distinct. `exists` includes null and zero. `join(source)` requires strings; casts cannot truncate numbers. Unknown branches fail. Invalid function arguments are not replaced by `{}`. Bounded walk/visit/recur traverse explicitly selected inputs under the engine budgets. Executable scripts and cyclic symbol references are rejected.


Optional reads also accept an explicit-source JSON string: `"$input/model"`, `"$root/path"`, `"$item/path"`, `"$context/path"`, `"$runtime/path"`, `"$state/slot/path"`, or `"$binding/name/path"`. Omit the pointer to read the source itself; a trailing slash selects an empty key. Pointers and binding-name segments use RFC 6901 escaping. Required reads use object form. Only Expression positions interpret compact strings; literal, enum and sample values remain data. Existing scope permissions, missing/null behavior, compiled depth and execution budgets remain in force. The catalog exposes an object/string `oneOf`. Valid shorthand survives package retrieval, encoding and recompilation; existing object declarations stay objects. A changed authored form has its own revision fingerprint and requires fresh verification.

## Cache, errors and events

A breakpoint is `{"kind":"breakpoint","location":"block","value":{"type":"ephemeral"},"ttl":"1h"}`. Do not also put TTL inside `value`. Request, tool and block scopes are mapped independently. Unknown fields and undeclared caching policy are not automatically added. Resource references retain provider/account/model/session scope.

Usage input/output/total/cacheRead/cacheCreation are optional counters with `count` and `origin` (`observed` or `inferred`). Explicit zero is `{"count":0,"origin":"observed"}`; an absent counter is different. Declare alias priority with `exists` and conditional expressions.

Built-in error semantics use message/category/code/param/details; unrepresentable provider details fail conversion. Failure mappings can read `context.httpStatus`. Usage tails may follow a stream terminal. Unknown events are rejected; deliberately ignored frames require an explicit empty-array rule. HTTP `framing.idleMillis` uses 600000 (10 minutes) when omitted or zero and accepts explicit values from 1 through 3600000 (1 hour). It bounds waiting for response headers and each upstream read, independently of total stream duration and the non-streaming `httpTimeout`. Existing explicit values remain unchanged; custom protocols can adjust this field, then verify and activate the revision.

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

Preview modes: mapping/session/task/models/count_tokens/agent. Exact payloads are defined by the [handlers](../backend/server/protocol_revision_admin.go) and [PreviewInput](../backend/protocol/authoring.go). Missing coverage or stale hash/compiler evidence blocks activation; uploading `passed:true` cannot authorize it.


## Default core dispositions

Target Mapping declarations generate default conversion rules with stable IDs. Requests and responses operate on core intents; no sourceCodec/targetCodec switch selects a Go implementation. The editor reads the current generic action catalog, and custom rule values remain exact JSON.

Compatible mode permits only declared, diagnosed losses; strict mode rejects them. Disabling a default by ID does not make the encoder silently perform its work. Unknown semantics and required provider/authorization/resource constraints remain protected. See the [three-part contract](protocol-three-part-contract.md) for field ownership and rule semantics.
