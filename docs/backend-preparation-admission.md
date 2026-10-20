# Gateway request-preparation admission

The HTTP inference ingress has an instance-wide preparation limiter, separate from model-group concurrency and daily quotas. It starts after inference authentication/protocol selection, before request-body reading, and covers decoding, candidate conversion/preflight, final wire/operation validation and transport-body materialization. It releases immediately before provider dispatch, not at the end of a potentially long provider response. Retries reacquire; failure paths release idempotently. Group quotas still run unchanged.

## Configuration

Optional `config.json` section (also shown in `config.json.example`):

```json
{
  "preparation": {
    "enabled": true,
    "maxConcurrent": 8,
    "maxQueued": 32,
    "maxReservedBodyBytes": 67108864,
    "queueTimeoutMillis": 5000
  }
}
```

The policy is frozen on the server instance's first admission/diagnostic access. **Restart the server after changing it.** It is not exposed as a hot-editable WebUI setting. Config Load/Reload/transactional Update validate and preserve the section; updating stored settings does not mutate in-flight reservations.

| Setting | Omitted/zero behavior | Range / meaning |
| --- | --- | --- |
| `enabled` | true | Explicit false restores unbounded admission for troubleshooting, not a safe memory cap |
| `maxConcurrent` | 8 | 1–1024 active preparation leases |
| `maxQueued` | 32 when omitted | 0–65536; explicit zero rejects immediately instead of queueing |
| `maxReservedBodyBytes` | max(64 MiB, configured global `maxBodyBytes`) | Aggregate admitted **input body reservations**, not exact heap bytes |
| `queueTimeoutMillis` | 5000 | 1–600000 ms maximum queue wait; not a provider or active-body-read timeout |

If you raise `maxBodyBytes` after a limiter has been initialized, restart and coordinate its reservation policy as well. An explicit reservation capacity lower than an unknown-length request's pinned limit can reject that request even if its eventual body would have been small.

## Length, queueing and rejection rules

- A nonnegative Content-Length reserves that declared size (at least one byte), capped at the pinned protocol request limit. It is **not trusted**: reading is bounded by both the protocol limit and one byte past the declaration; mismatches are rejected before semantic preparation, without extending a held reservation.
- Unknown/chunked length reserves the complete pinned protocol request limit before reading. This avoids unbounded incremental growth or multiple requests deadlocking while trying to enlarge reservations. It is intentionally conservative.
- Waiting is bounded FIFO. A smaller newcomer cannot jump past a larger queued reservation. Body reading has not started while waiting.
- A queue-full/wait-timeout/closed limiter returns a protocol-shaped HTTP 503 with `Retry-After: 1`; a reservation that can never fit returns HTTP 413. Client cancellation wins with the existing 499 accounting. Local admission failures are classified as `preparation_overloaded`, not observed provider failures.
- Shutdown closes admission and wakes queued waiters before normal request draining. Granted work retains ownership until it stops; releasing capacity early on cancellation would allow overlapping work that has not actually exited.

## What is and is not bounded

This bounds concurrent expensive preparation and admitted input-body reservations. It is **not** an exact process-heap, GC/RSS, generated-output, logging, or all-in-flight-request memory limit. Encoders and semantic graphs can require multiples of input bytes; slow upstream responses can retain request data after preparation release. Use model-group concurrency, transport deadlines and usage payload budgets alongside it. Existing HTTP read/connection policy controls active slow uploads; queue timeout is not a replacement for those controls.

HTTP asynchronous submission releases after initial preparation; task workers, Agent invocations and WebSocket session lanes retain their own lifecycles and are not claimed to be covered by this HTTP generation preparation policy. No final-wire/schema/resource/scope checks, unknown-field protection, usage durability or model quotas are disabled.

For performance evidence, see the item-4 report in the [#81 structural follow-up](https://github.com/PinkElysiaDev/Elysia-Api/issues/81). The controlled memory experiment uses the same binary with admission off vs a stricter one-slot policy; it must not be confused with default-policy throughput or a universal RSS improvement.
