# Backend SSE library migration

Tracking: [#50](https://github.com/PinkElysiaDev/Elysia-Api/issues/50).

## Selection

The gateway uses **`github.com/tmaxmax/go-sse` v0.11.0** for SSE field parsing and encoding.

| Candidate | Findings in the evaluated version |
| --- | --- |
| `go-sse` v0.11.0 | Incremental event iteration, configurable scanner/event limit, BOM removal, LF/CRLF/CR support, multiline data, persistent/reset event IDs, comment encoding, and writer-error propagation. Its reader accepts a final data line ending in one newline at EOF, so finite model streams need an EOF completeness guard. |
| Existing `gin-contrib/sse` v1.1.0 | `Decode` calls `io.ReadAll`, buffers all events, and splits on LF; unsuitable for long-lived upstream streams. Several `Encode` paths discard writer errors, including byte-slice data, and cannot reliably signal disconnects to this gateway. |

The selection is based on framing and lifecycle requirements, not a performance claim. Gin's transitive SSE dependency remains available to Gin itself.

## Ownership

- `backend/internal/eventstream/sse.go` adapts the library to finite gateway streams. The dependency parses fields and writes events/comments. The local adapter checks context, filters payload-free control events, validates NUL-free output event names, and enforces a complete final frame using only four trailing bytes.
- `protocol.ReadFrames` owns JSON decoding, declared transport end markers, and resource-error classification. It drains late usage after semantic completion until transport completion. It does not reconnect or resubmit generation.
- `protocol.WriteFrame` resolves declared event names and compacts raw JSON using Sonic without decoding numbers or introducing HTML escaping.
- Gateway completion markers and Agent REST/admin events and heartbeat comments use the shared writer. HTTP owners still control headers, flushing, cancellation, and detached Agent turn lifetimes.
- MCP and A2A transports are owned by their official SDKs.
- Stored-body media extraction is a separate, best-effort, line-preserving log transformation. Its narrow `data:` prefix handling preserves surrounding raw comments, unknown fields, retry values, and whitespace; it is not used for upstream event decoding. Re-encoding those logs through an event-only reader would discard that information.

## Compatibility and boundary behavior

1. **Whole-event budget:** the configured protocol buffer limit now bounds the library's complete wire-event scanner, including metadata, comments, data, and delimiters. Previously the local loop bounded individual lines and accumulated data separately. Providers near the old payload-only bound must allow room for framing. Oversized input still reports a `response_frame` resource failure.
2. **Complete EOF:** an empty stream is valid framing input; semantic completion is checked separately. Nonempty streams must end on a blank-line event boundary. A truncated data/completion/usage event is never delivered. The EOF check also rejects incomplete trailing metadata or comments, which the old implementation could ignore. A declared complete end-marker frame stops reading immediately, so bytes after that marker are not consumed as events.
3. **Empty/control frames:** comments and payload-free events are not passed to JSON decoding. This includes explicit empty `data` events, which previously failed JSON decoding. ID-only events still update the library's ID state. `retry` does not enable reconnects.
4. **IDs and names:** IDs persist across events, empty IDs reset them, and IDs containing NUL are ignored. The event name resets for each event and remains empty for unnamed events. Output event names containing CR, LF, or NUL fail before any frame bytes are emitted.
5. **Input syntax:** UTF-8 BOM at stream start and LF, CRLF, or CR line endings are supported, including fragmented reads. Multiple `data` fields are joined with LF; one optional space after the colon is removed by the library. Unknown fields are ignored.
6. **Output:** normal compact JSON events retain `event: name\ndata: JSON\n\n`; Agent heartbeats remain `: keepalive\n\n`. Encoding errors reach the caller. Application-owned JSON compaction/Agent-event encoding uses Sonic; large integer lexemes, explicit null/false, negative zero, and literal HTML characters survive raw JSON compaction.

NDJSON framing, handler response structures, source accounting, downstream error trailers, and HTTP flush policies retain their existing roles.

## Focused validation

From `backend/`:

```sh
go test -tags=sonic -race ./internal/eventstream ./protocol ./relay ./server \
  -run '^Test(SSE|HTTP|StreamIdle|OpenAIStreamNotCut|GatewaySSEAndNDJSON|GatewayBrokenStream|GatewayResponsesNativeFailure|GatewayFailureEncoding|ProviderFailureRetains|CustomProtocolStream|TransportFailure|FailureEvidence|AgentCaller|AgentFullTurn|AgentGatedToolApproval|AgentStopTurn|AgentKeyCreateSecret|OpenAIChatPassthroughStream|ResponsesPassthroughStream|StreamCancellationUsage|ToolOnlyStreamWithoutUsage|CanceledConnection)' \
  -count=1 -timeout=4m
```

Results:

| Package | Result |
| --- | --- |
| `internal/eventstream` | PASS, 1.016s |
| `protocol` | PASS, 1.030s |
| `relay` | PASS, 1.538s |
| `server` | PASS, 120.440s |

Migration regressions cover mixed/fragmented newline boundaries, BOM, metadata-only frames, NUL/empty IDs, complete versus truncated EOF, whole-event limits, per-write disconnect errors, cancellation/consumer errors, and raw JSON fidelity. The real HTTP bridge test uses an independent CR-only upstream fixture with BOM/control frames and verifies late usage persistence. Existing focused tests cover Agent events/approvals, SSE/NDJSON bridging, provider failures, transport attribution, idle timeouts, cancellation, and no retries after partial delivery.

The full backend suite was not run. No throughput or allocation improvement is claimed.
