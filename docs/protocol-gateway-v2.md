# Protocol v2 gateway integration (C10)

The versioned registry now serves `ANY /gateway/:protocolId/*path`. Authentication,
model-group authorization, request limits, affinity, cancellation, configured HTTP
clients, outbound address checks and usage storage remain gateway-owned. Definitions
cannot replace authorization or supply provider/account provenance.

## Bind a model to an enabled revision

Activate ingress and upstream definitions using the revision API first, then use
`PUT /api/admin/protocols/bindings` (admin authentication required):

```json
{
  "kind": "source",
  "sourceId": "my-source",
  "modelId": "",
  "groupId": "",
  "binding": {
    "protocolId": "text-beta",
    "revisionHash": "<active revision hash>",
    "capabilities": {"text": true},
    "transports": ["http_json"]
  }
}
```

`kind: model` requires sourceId and modelId and overrides a source binding.
`kind: group` requires groupId and constrains a specific ingress revision and the
group's allowed capabilities/transports. The pre-existing model/group tools and
media switches continue to constrain actual routing. A binding never enables a
feature disabled on the selected model. Optional `binding.operation` selects one
named upstream operation where otherwise ambiguous.

`GET /api/admin/protocols/bindings` returns the saved contracts. Source/model saves
recompute paired offline reports against enabled ingress definitions; uploaded
reports are replaced with engine-generated evidence. At least one compatible
ingress is required. Each report binds both definition hashes, the compiler
version, and the exact capability restriction. Samples beyond that restriction
are visibly skipped, while missing fixtures for promised capabilities block the
pair. This permits a text-only model without claiming tool support.

Requests require a passing, current paired report, as well as runtime checks on
actual input and upstream output. Activating another revision requires rechecking
the affected bindings. Requests already holding a registry view keep their
original adapters and binding data; hot reload cannot change them midway.

## Standalone example

The definitions in `backend/protocol/testdata/text-alpha.json` and `text-beta.json`
use independently declared mappings, with no built-in adapter/shape reference.
After activation and a source binding, a client can send:

```http
POST /gateway/text-alpha/generate
Authorization: Bearer <gateway token>
Content-Type: application/json

{"deployment":"my-group","turns":[{"actor":"user","segments":[{"text":"hello"}]}]}
```

For a beta upstream the gateway emits `engine`, `history[].speaker` and
`chunks[].value` with the selected upstream model name. Response conversion uses
the independently defined reverse response direction. Model-source credentials
are injected only at the declared header/query location.

Operation paths remain relative to the configured base URL. `{model}` is an
escaped component; custom ingress mappings can read matched path parameters from
the evaluation context. Several generation operations require an unambiguous
path/method or an explicit binding operation. SSE and NDJSON can bridge each
other when both revisions declare the necessary event directions.

## Fidelity and failure behavior

- Model candidates lacking the actual requested capabilities are excluded before
  sending. An incapable group rejects media; the former image/audio/video stripping
  path and its filtered-response header have been removed.
- File/cache/session/signature references and opaque native content need a unique
  source/account/model binding. Ambiguous multi-account provenance is an explicit
  error. Mapping-supplied scope cannot replace the gateway-owned scope.
- SSE frames and NDJSON lines are bounded, including fragmented multiline SSE.
  Framing end markers never substitute for a semantic terminal. Usage after the
  semantic terminal is drained and merged. A declared framing end marker ends
  reading without waiting for another network close.
- No generation is replayed after downstream output starts. A late failure emits
  the declared `operation.failed` mapping when supported and sets the HTTP trailer
  `X-Elysia-Stream-Error: protocol_stream_error`. No success terminal is fabricated
  for a truncated stream. Network errors with uncertain submission are not
  automatically replayed; configured candidate retries apply to explicit HTTP
  429/503 rejections before downstream commitment.
- Request records include ingress/upstream hashes, conversion diagnostics and
  canonical usage presence/origin. Body capture still follows the existing logging
  policy. Missing usage stays missing through all 16 built-in response combinations;
  observed zero and absent cache counters remain distinct. This fixes an additional
  typed-response integration defect; its historical-version attribution has not
  been independently tested.

## Validation and staged boundaries

C10 tests exercise alpha/beta HTTP in both directions, SSE/NDJSON in both
directions, usage tails and persisted creation/read/zero counters, authentication,
group authorization, cancellation, stale bindings, in-flight activation, four
public ingress dispatches, report generation, frame limits, path/credential
placement, nested provenance and model capability checks. HTTP tests use local
simulated upstreams, not real provider validation.

The four public paths and custom namespace now use active verified v2
revisions. C15/C16 removed the old runtime. Built-in adapters implement stateful
events; WebSocket and durable task transport are installed. See the
[guide](protocol-guide.en.md) and [system evidence](protocol-system-validation.md)
for current compiler, coverage, performance and unverified release gates.
