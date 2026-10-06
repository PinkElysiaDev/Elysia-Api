# Protocol v2 WebSocket sessions

The gateway installs the pure Go `github.com/coder/websocket` v1.8.14 transport.
The fixed `/gateway/:protocolId/*path` namespace uses the same compiled mappings,
capability checks and session replay as offline verification. Local network
tests below are not real-provider verification.

## Independent directions

| Role | Handshake | Client-to-upstream events | Upstream-to-client events |
| --- | --- | --- | --- |
| Client ingress | `decode_request` | `decode_client_event` | `encode_event` |
| Upstream | `encode_request` | `encode_upstream_event` | `decode_event` |

A protocol can implement one role without implementing the inverse role or an
HTTP response adapter. Offline verification requires its actual directions.
Native preservation on an encoder still requires the corresponding decoder.

New semantic events are `session.configure`, `input.append`, `input.commit`,
`response.create`, `response.cancel`, `tool.result` and `session.close`. Existing
response/item/usage/error/media events retain their meanings. An event's lane
comes from the connection, never from an author-controlled `type` value.

`session.configure` carries a semantic `request`. It can configure tools and
content but cannot change the authorized model group. The pinned adapter maps
the authorized group to the selected upstream model. Model bindings also check
every real client command and upstream event; an event cannot enable a tool or
media capability excluded by that binding.

## Session operation

A session is a GET WebSocket operation with explicit limits. Example transport
metadata (not a complete protocol definition):

```json
{
  "kind": "session",
  "method": "GET",
  "path": "/session",
  "transport": "websocket",
  "auth": { "location": "header", "name": "Authorization", "prefix": "Bearer " },
  "session": {
    "frameBytes": 1048576,
    "queueItems": 32,
    "queueBytes": 8388608,
    "idleMillis": 120000,
    "pingMillis": 30000,
    "closeMillis": 5000,
    "automaticResponses": false
  }
}
```

`DefaultSessionConfig` supplies authoring defaults. Compilation rejects missing
or invalid positive limits instead of repairing them at execution time.
Frame/queue sizes cannot exceed the engine limits. Timing must satisfy
`0 < closeMillis < pingMillis < idleMillis <= 120000`.

Binary input/output requires `inputMedia`/`outputMedia`, each containing `type`
and `format`, plus the `media.realtime` capability. Both endpoints must declare
matching formats. Binary payloads are forwarded byte-for-byte without JSON
encoding or transcoding. Undeclared binary input and format mismatches fail.
Reconnect, media transcoding and session-resume configuration are unsupported;
unknown configuration keys fail compilation.

## Lifecycle and verification

`sessionSamples` contains ordered mixed-lane traces:

- Each sample references its session operation and supplies a model, scope and
  optional expression context.
- Each step specifies its actual direction, wire/semantic input and exact
  expected output, following the ordinary fixture conventions.
- Each mapped step is compared with its expectation and, when available, its
  independent inverse adapter. The complete trace is then validated by the same
  `SessionReplay` used by live translation.
- Positive traces must cover every implemented direction and declared semantic
  capability. Declared function/free-text tools need a definition, completed
  associated call and client-submitted result in one trace. Negative examples
  cannot satisfy positive coverage.
- A session operation without a passing interleaved trace cannot activate.
  Single-direction fixtures cannot prove a session lifecycle.

Each response keeps an independent `EventReplay`, allowing interleaved responses
and usage tails after response completion. Tool results require completed,
unanswered calls and cannot be submitted twice. Calls cannot reuse another
response's identity. The gateway never executes these client tools.

Explicit sequence numbers are deduplicated across each entire lane. Their
digests include native extensions; changing an unknown field under the same
sequence number is a conflict, not an identical replay. Missing numbers disable
deduplication. Explicit `null` does not become sequence zero.

A connection error without a response ID is a session failure. EOF with an
unfinished response fails validation. Explicit client session-close is
cancellation; it does not create successful response terminals. Automatic
server-created responses require the operation's explicit declaration.

Protocol combinations replay traces from both endpoints through the other
endpoint's implemented encoders. A binding limited to HTTP text does not inherit
session/tool obligations merely because its protocol also implements them.
Combination evidence records observed semantic capabilities, not unproven
fixture labels. Reports now use compiler version `2.0.0-dev.4`, and the sample
hash includes both ordinary samples and session traces; older evidence must be
reverified.

## Bounded execution and accounting

`RunSession` owns bounded incoming and outgoing queues, independent readers and
writers, heartbeat deadlines and cancellation. Queue budgets count writes in
progress. Each reader may additionally retain at most one bounded frame while
waiting for capacity. The coordinator alone mutates semantic replay state.
Response identities, pending requests, tool buffers and sequence windows have
aggregate state/byte limits; exceeding them produces a diagnostic.

Read EOF is ordered after previously received messages. Valid terminal/usage
frames are translated and the outgoing queues drain before sockets close.
Explicit session close follows the same flush rule. Cancellation closes both
connections and joins all workers; messages are never automatically replayed.

Usage snapshots remain separate per response, preserving absent, observed zero
and nonzero counters. At connection end, each response is persisted once with
its response ID, terminal status and pinned ingress/upstream revision hashes.
A later connection failure does not turn completed responses into failures.
Shutdown waits for session accounting before closing the usage writer/store.

## Handshake and network behavior

Only declared `queryFields` and `headerFields` enter handshake mappings. Client
credentials, cookies and transport headers cannot become author-controlled
upstream fields. The gateway injects the selected model source's credential.
Upstream dialing reuses HTTP proxy, TLS and dial-time address checks. Redirects
are refused. Browser connections retain the library's same-origin check.

Authorization, model binding, protocol compatibility and request shaping run
before accepting the client connection. Each session pins both definitions,
model and account. Session configuration cannot change the authorized model.
There is no automatic reconnect, resume or application-message replay.

Graceful close drains pending frames, sends normal/error close codes and has a
configured deadline. The deadline closes sockets even if a reader already
ended. Cancellation immediately closes both sockets and joins workers. JSON
mapping failures send a declared error event when the encoder supports it;
an unencodable failure still closes with an error, never a success terminal.

## Validation evidence

Core regression coverage is in `session_test.go`,
`verification_session_test.go` and `session_transport_test.go`. It covers
interleaved JSON/free-text tools, orphan/duplicate results, usage tails,
model changes, native sequence conflicts, incomplete sessions, independent
roles, capability-limited bindings, configuration errors, slow consumers,
cancellation, binary byte preservation, queue budgets and explicit close.

`relay/protocol_websocket_test.go` exercises actual local sockets: binary bytes
in both directions, correlated ping/pong, cancellation, a non-reading client,
unacknowledged close deadlines, worker cleanup and rejected credential
redirects. `server/gateway_session_test.go` exercises independent alpha/beta
wire definitions through the HTTP upgrade route, multi-turn tools, cached usage
tails, per-response SQLite records, authentication/group/origin rejection before
upstream access, and a late unsupported event with no generation replay.
The standalone definitions are in `backend/protocol/testdata/session-*.json`.

Full backend tests, vet and diff checks are required for this commit. No real
provider or successful local race run is claimed; the Windows C toolchain still
prevents race builds. Media format conversion and session resumption remain
explicitly unsupported.
