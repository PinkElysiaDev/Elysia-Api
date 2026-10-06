# Maheshvara protocol

> Historical architecture reference. The Maheshvara runtime was removed in C16.
> For the active engine, use [Protocol v2](protocol-guide.en.md). Old paths below describe historical versions only.

[Documentation](README.en.md) · [简体中文](maheshvara-protocol.md) · **English**

Maheshvara is the gateway's internal model for requests, responses, and stream events. It supports cross-protocol conversion and is not a standalone HTTP API. `MaheshvaraProtocolVersion` is currently `"2"`, corresponding to the reasoning envelope; v1 envelopes remain readable.

## Conversion path

Cross-protocol and custom-protocol requests use the common model:

```text
client wire → Maheshvara → routing / filtering → upstream wire
upstream wire → Maheshvara → client wire
```

Supported wires are Chat Completions API, Responses API, Anthropic API, and Gemini API. Matching protocols can use automatic passthrough, so the diagram does not imply every request is rewritten.

Conversion preserves what the target can represent. Unsupported semantics produce explicit errors or defined filtering. Unknown blocks, signatures, and ciphertext are not fabricated into ordinary prompts. Custom protocols use restricted data mapping.

## Request model

This illustrates internal fields, not a complete request for any particular provider:

```json
{
  "model": "provider-model",
  "instructions": "system and developer instructions",
  "messages": [],
  "input_items": [],
  "max_output_tokens": 4096,
  "temperature": 0.7,
  "top_p": 0.9,
  "stream": true,
  "stream_options": {"include_usage": true},
  "tools": [],
  "tool_choice": "auto",
  "parallel_tool_calls": true,
  "response_format": {},
  "reasoning": {},
  "thinking": {},
  "metadata": {},
  "raw_extra": {}
}
```

### Generation parameters

| Category | Fields |
| --- | --- |
| Limits | `max_output_tokens`, `min_output_tokens`, `max_tool_calls` |
| Sampling | `temperature`, `top_p`, `top_k`, `typical_p`, `min_p`, `top_a` |
| Penalties | `presence_penalty`, `frequency_penalty`, `repetition_penalty` |
| Candidates and probabilities | `n`, `seed`, `logprobs`, `top_logprobs` |
| Output | `stop`, `response_format`, `modalities`, `audio`, `prediction`, `verbosity` |
| Policies | `service_tier`, `safety_identifier`, `safety_settings`, `cache_control` |
| Responses state | `previous_response_id`, `store`, `include`, `truncation`, `background`, `conversation`, `prompt` |
| Cache and tracing | `prompt_cache_key`, `prompt_cache_retention`, `request_id`, `session_id`, `timeout_ms` |

Extensions outside stable fields enter `RawExtra` for compatibility and templates. Not every target re-emits them.

### Messages and content

Messages contain `role`, `content`, `tool_calls`, `tool_call_id`, `name`, `cache_control`, and `metadata`. Roles are `user`, `assistant`, `system`, `developer`, and `tool`. System instructions map to messages, `system`, `systemInstruction`, or `instructions` according to the target.

| Part type | Main fields |
| --- | --- |
| `text` | `text` |
| `reasoning` | `reasoning_text`, `text`, `signature`, `signature_provider`, `encrypted_content`, `reasoning_summary` |
| `refusal` | `text`, `annotations` |
| `image` | `image_url`, `image_base64`, `media_type`, `detail` |
| `audio` | `audio_url`, `audio_base64`, `media_type`, optional transcription `text` |
| `video` | `video_url`, `video_base64`, `media_type` |
| `file` / `document` | `file_id`, `file_name`, `file_data`, `uri`, `media_type` |
| `tool_output` | `tool_call_id`, `tool_output` |
| `tool_call` | Tool-call fields |

Multimodal values may also retain general `uri` / `data`; renderers choose a representation the target supports.

### Tools

Function definitions include `type: "function"`, `name`, `description`, `parameters` / `input_schema`, and `strict`. Calls use `id`, `type`, `name`, `arguments` / `arguments_text`; `thought_signature` is paired with its issuer.

Results link to calls through `tool_call_id`. Gemini `functionResponse` requires a name; if history cannot supply it, conversion fails with the message index and call ID.

Responses built-in tools such as search, files, code execution, and image generation carry execution semantics. Unsupported targets produce explicit errors rather than pretending these are ordinary functions.

## Response model

```json
{
  "id": "resp-example",
  "model": "provider-model",
  "created_at": 0,
  "status": "completed",
  "output": [],
  "stop_reason": "stop",
  "incomplete_details": {},
  "metadata": {},
  "service_tier": "default",
  "system_fingerprint": "example",
  "usage": {},
  "error": null
}
```

`output` may contain messages, function calls, reasoning, and representable provider tool results. Functions use `call_id`, `name`, and `arguments`; reasoning can include text, summaries, and encrypted content.

Beyond input/output/total tokens, usage can record cache hits/creation, reasoning, text/image/audio, tool use, accepted/rejected predictions, built-in tool counts, estimation flags, sources, and raw provider usage. An unreported field does not imply precisely zero consumption.

## Reasoning and signatures

Distinguish visible reasoning text, provider signatures, and opaque encrypted/redacted data. Signature restoration checks its issuer; an Anthropic signature is not a Gemini `thoughtSignature`.

The current prefix is `maheshvara-reasoning-v2:`. It carries text, ciphertext, summaries, provider, and model information and can travel through Claude signature slots on a client round trip. v1 remains readable. Known foreign ciphertext is not replayed as another provider's native signature. The Responses path retains compatibility for v1 ciphertext without an issuer; this is not strict model-level cryptographic verification.

The Chat extension `tool_calls[*].extra_content.google.thought_signature` is marked as Gemini-issued. Cross-provider history lacking a valid Gemini signature uses `skip_thought_signature_validator` on the necessary first function call. Unknown blocks, empty thinking, and uninterpretable external redacted data follow conversion rules rather than becoming ordinary text.

See [reasoning.go](../backend/relay/maheshvara_reasoning.go) and [v2 tests](../backend/relay/maheshvara_reasoning_v2_test.go).

## Gemini Part constraints

Each Part must contain one valid representation: nonempty `text`, `inlineData`, `fileData`, `functionCall`, or `functionResponse`. `thought` is a text-Part attribute, not independent content.

Messages with no Parts after filtering are discarded; adjacent same-role messages may be merged in order. Renderers do not insert empty text placeholders. No representable content produces a local error.

## Request mappings

| Internal field | Chat Completions | Anthropic | Gemini | Responses |
| --- | --- | --- | --- | --- |
| Model | `model` | `model` | URL model | `model` |
| Instructions | system/developer messages | `system` | `systemInstruction` | `instructions` |
| Messages | `messages` | content blocks | `contents.parts` | `input` items |
| Multimodal | Extended content parts | blocks | `inlineData` / `fileData` | input content |
| Reasoning | `reasoning_content` and extensions | `thinking` | thought Part / config | reasoning item / config |
| Function definitions | `tools[].function` | `input_schema` | `functionDeclarations` | function tool |
| Calls/results | `tool_calls` / tool message | `tool_use` / `tool_result` | `functionCall` / `functionResponse` | call / output item |
| Output shape | `response_format` | Representable output config | MIME / schema | `text.format` |
| Generation | Standard fields and extensions | Representable subset | `generationConfig` | Representable subset |

Recognized Chat extensions include `reasoning_content`, `reasoning_effort`, `repetition_penalty`, `min_p`, `top_a`, media, and usage details. Known fields enter the internal model; others enter `RawExtra`.

## Response mappings

| Internal output | Chat Completions | Anthropic | Gemini | Responses |
| --- | --- | --- | --- | --- |
| Text | choice message | text block | text Part | `output_text` |
| Refusal | refusal / finish reason | text / stop reason | text / safety finish | refusal content |
| Reasoning | `reasoning_content` | thinking | thought Part | reasoning / summary / encrypted content |
| Call | `tool_calls` | `tool_use` | `functionCall` | function call item |
| Usage | usage details | usage / cache | `usageMetadata` | usage details |

These are mapping categories, not a guarantee that arbitrary private fields survive conversion.

## Streaming

SSE / NDJSON → stateful decoder → `MaheshvaraStreamEvent` → client renderer. The reader supports multiline `data`, `event` / `id` / `retry`, a final event without a trailing blank line, cancellation, idle timeout, and single-line NDJSON.

Renderers maintain tool state, Anthropic block boundaries, Responses ordering, and valid Gemini Parts. Only targets requiring `[DONE]` emit it. Custom streams need recognizable completion. An empty completion with a finish reason can succeed; a bare terminator with no representable output fails. See [stream definitions](protocol-definition-reference.en.md#streaming) for field-family modes, heterogeneous frames, and tool assembly.

## Custom protocols

SQLite and the designer manage definitions. Saving validates and updates the registry; invalid new definitions cannot replace the current valid registry. Definitions with `models` support discovery; others require manual lists. The old `/custom-protocols/assist` is not a current route; AI onboarding uses the Agent and CLI.

Request/response trees, templates, auth, transforms, aliases, and schema fields are maintained in the [definition reference](protocol-definition-reference.en.md) to avoid conflicting parameter tables.

## Same-protocol passthrough

Chat forwarding automatically uses passthrough when client and selected upstream wires match and capability filtering has not rewritten the request. Responses has a corresponding passthrough path. There is no `relay.passthrough` configuration switch to enable.

Passthrough rebuilds the original JSON, changing the routed model name and adding streaming flags or OpenAI usage options when needed. It preserves unknown fields but is not byte-identical or zero-cost. Cross-protocol, custom mappings, and filtered requests use conversion.

## Errors and validation

Missing tool names, unlinked results, unrepresentable built-in tools, empty output, invalid mappings, or missing stream completion can cause local conversion errors. Do not bypass validation with spaces or fabricated objects.

Run from `backend/`:

```bash
go test ./relay ./server
```

Existing regressions cover request/response 4×4 matrices, signatures and envelopes, multimodal input and tools, usage, SSE, passthrough, cumulative snapshots, heterogeneous frames, and registry safety. Local fixtures do not prove compatibility with every live provider.

Implementations: [types](../backend/relay/maheshvara_types.go), [conversion](../backend/relay/maheshvara_request_in.go), [custom mappings](../backend/relay/custom_mapping.go), [stream mappings](../backend/relay/custom_stream_mapping.go). Changes to field semantics or completion contracts require version and legacy-data review, not merely a different version number in documentation.
