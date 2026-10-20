# Custom protocol definition reference (v2)

[简体中文](protocol-definition-reference.md) · [User guide](protocol-guide.en.md) · [Detailed implementation](protocol-definition-v2.md)

The configuration format is `schemaVersion: 2`; semantic documents use version 1. Author version, content hash and compiler version are independent. Legacy request/response/shape templates are import-only. Read the live contract with `GET /api/admin/protocols/schema` or `elysia protocol schema`: [Definition](../backend/protocol/definition.go), [semantic model](../backend/protocol/model.go), [compiler](../backend/protocol/compiler.go).

## Runtime value-schema validation

The compiler translates the restricted `ValueSchema` language into draft 2020-12 schemas and precompiles them with **santhosh-tekuri/jsonschema/v6 v6.0.3**. Mapping, operation-input and model-page validation reuse revision-owned validators. `nullable` accepts null even when enum is present; exact numbers and last-decoded duplicate keys retain their semantics. Application-owned decoding uses Sonic with `UseNumber`.

**Migration compatibility:** compiler version `2.0.0-dev.73` requires renewed verification evidence. Schema-validated values (including unknown descendants and enum entries) now enforce the numeric conversion bound: at most 4096 bytes per number spelling and exponent in -4096…4096. Complete-value byte/node/depth budgets run before decoding. Diagnostics use library error trees and escaped instance paths; message wording and error ordering can change. See [the migration report](backend-runtime-schema-migration.md) for scope, benchmarks and validation.

## Generated JSON Schema

The live catalog uses **`github.com/invopop/jsonschema` v0.14.0** to reflect the Go models into JSON Schema draft 2020-12. It provides definition, binding, conversion-policy, and semantic schemas. Structs reject unknown properties; JSON `omitempty` and `omitzero` tags control required fields. The protocol's `Value` type is explicitly mapped to an unrestricted JSON schema, retaining arbitrary JSON values, including null, false, zero, and large numbers. Cache-intent descriptions use the library's `jsonschema_description` tag.

### Schema representation migration

Clients inspecting raw schema JSON must handle these standard, validation-equivalent representations:

- An unrestricted schema can be the boolean `true`, rather than `{}`.
- Named maps such as `Object` and `CapabilitySet` use local `$defs` / `$ref` entries rather than always appearing inline.
- Empty `required` arrays are omitted. Optional fields remain optional, including fields using `omitzero`.
- Binding and conversion-policy roots now also declare `$schema`. Automatic package-derived `$id` values are disabled, preserving local-reference resolution.

The catalog envelope and its semantic roots (`request`, `response`, `event`, `task`, `models`, `agent`) remain available. `semanticSchema` groups those roots under a shared `$defs`; to validate one root, combine its `$ref` with that shared `$defs` and dialect. CLI `--section` / `--type` queries continue to expose named definitions, including the newly named maps.

All existing catalog constraints matched the pre-migration output after normalizing the equivalent forms above. Each catalog call returns fresh objects. Application-owned serialization uses Sonic; the dependency owns its custom schema serialization. Runtime protocol checks, resource budgets, and action-specific constraints continue to be enforced by the compiler.

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
| `GET /migration`, `POST /migration/preview`, `/migration/apply` | Upgrade preview and transactional cutover |

Preview modes: mapping/session/task/models/agent. Exact payloads are defined by the [handlers](../backend/server/protocol_revision_admin.go) and [PreviewInput](../backend/protocol/authoring.go). Missing coverage or stale hash/compiler evidence blocks activation; uploading `passed:true` cannot authorize it.


## Default request hint projection

The shared conversion action registry includes `request_projection`, a request action at default order 130. Its value is `{sourceCodec?, targetCodec}`, using `openai-chat`, `responses`, `anthropic` or `gemini`; an omitted source codec leaves defaults unknown. It retains mapped cache hints and request tracking metadata; recognized hints without a target representation are omitted in compatible mode with info-level `conversion_degraded` and `lossy_compatible` fidelity. Strict mode rejects actual loss before upstream invocation. Empty hints and known inactive non-streaming output options use `conversion_normalized` with preserved fidelity. Function tool `strict` is preserved across Chat, Responses and Anthropic. Chat/Anthropic non-strict defaults become explicit `false` in Responses. Responses automatic strictness cannot be reproduced across APIs: compatible mode warns and uses the target default, while strict mode rejects that candidate. Gemini omission of explicit strictness is also lossy. Tool schemas are never rewritten.

The existing `stream_options` action also handles Anthropic custom-tool `eager_input_streaming` preferences (boolean or null). The default rule supplies the actual target codec in `value`, using the same codec enum. Anthropic streaming requests retain the preference. Other targets omit unrepresentable per-tool streaming timing in compatible mode with info-level `conversion_degraded` and `lossy_compatible`; strict mode rejects either explicit boolean value. Non-streaming preferences and a cross-protocol default null normalize without an effect. Tool schemas, input payloads, associations and signatures remain intact, and unknown tool properties still require explicit mapping. Rules without a target codec do not guess a tool-streaming mapping.

Tool-result status uses the existing `tool_result_object` and `tool_result_text` actions. The object action preserves a true error flag in `response.error` and normalizes false to an ordinary result; caller data named `error` is protected from being misclassified. Text-to-object wrapping and joining plain text result blocks remain diagnosed shape changes rejected in strict mode. Media, scoped state and unknown block extensions require explicit mapping. The text action accepts an optional actual target codec in `value`, supplied by the default policy: Anthropic retains the boolean flag, while Chat and Responses normalize false and represent true with a `[Tool error] ` text marker. The latter emits a warning with `lossy_compatible` fidelity and is rejected in strict mode. Without a target codec, the text action retains status. Invalid boolean flags are rejected before authored removal, tool inputs and associations remain unchanged, and native snapshots cannot restore a projected flag.

Chat and Responses retain `metadata` and `user`; the recognized `metadata.user_id` mapping retains the tracking identity. Unknown extensions require explicit cross-protocol mappings. Tool `strict` omissions remain warnings in compatible mode and fail in strict mode. Cache resources, prewarm, cache mode, minimum lifetime, generation constraints, safety, storage and session references keep their dedicated validation. Projection runs on a candidate copy and native replay cannot resurrect omitted fields. Runtime forwarding, management preview, combination verification and the built-in Agent use the same pipeline. Compiler changes use startup revalidation and preset upgrades without database migrations or version-specific fallback branches.
