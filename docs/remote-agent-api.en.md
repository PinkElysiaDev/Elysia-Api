# Remote Agent APIs

[Documentation](README.en.md) · [简体中文](remote-agent-api.md) · **English**

REST / A2A drive the built-in assistant through persistent sessions, model calls, and approvals. MCP executes `elysia_cli` operations directly without calling the built-in model or creating an assistant session.

| Interface | Endpoint | Purpose |
| --- | --- | --- |
| REST | `/api/agent/*` | Scripts, panels, session management |
| MCP | `POST /mcp` | Direct operations by an external agent |
| A2A | `POST /a2a` | Tasks and agent collaboration |
| Agent Card | `GET /.well-known/agent-card.json` | Public discovery, gated by the remote-access switch |

## Authentication

Create a key with the `agent` scope in the runtime configuration's remote-access section:

```http
Authorization: Bearer <AGENT_KEY>
```

| Credential | Scope |
| --- | --- |
| Panel token `panelAccessToken` | `/api/admin/*` management |
| Relay token without `agent` | `/v1/*`, `/v1beta/*`, subject to group authorization |
| Key with `agent` | `/api/agent/*`, `/mcp`, `/a2a`; inference endpoints reject it |

Missing or invalid remote keys return 401; missing `agent` scope returns 403, both with `WWW-Authenticate`. The Agent Card itself does not require a key. Group authorization (`allowedGroups`) and endpoint scopes (`scopes`) are separate dimensions.

`agentRemote.enabled` defaults to `true`; setting it to `false` returns 404 for remote endpoints and the card. `agentRemote.publicUrl` sets the base address behind a reverse proxy; an empty value derives it from the request.

## REST

Handlers are shared with `/api/admin/agent/*`, with different authentication. Ordinary responses use `{ok,data}` / `{ok,error:{code,message}}`; messages and approvals return SSE.

Paths below use the prefix `/api/agent`:

| Method | Path | Request / behavior |
| --- | --- | --- |
| GET | `/sessions` | Optional `status`, `limit`, `offset`; `{items,total}`; no pagination parameters returns all |
| POST | `/sessions` | `{title?,mode?,protocolId?,settings?}`; `mode` is `create` or `edit`, with an existing protocol ID required for editing |
| GET | `/sessions/:id` | `{session,messages}`, messages ordered by `seq` |
| PATCH | `/sessions/:id` | Partial `{title?,settings?,apiKey?,clearApiKey?}` |
| DELETE | `/sessions/:id` | Stop the turn, then delete the session |
| POST | `/sessions/:id/messages` | `{content?,documents?,afterSeq?}`; `afterSeq` truncates subsequent messages first |
| POST | `/sessions/:id/approve` | `{approved,answer?,note?,apiKey?,baseUrl?}`; resume via SSE after a decision or answer |
| POST | `/sessions/:id/stop` | Stop the turn; `{stopped}` |
| DELETE | `/sessions/:id/messages?afterSeq=0` | Clear messages; positive values truncate by sequence, retaining session and settings |
| POST | `/sessions/:id/restore-draft` | Restore a draft snapshot |

Statuses are `idle`, `running`, and `waiting_approval`. `limit` must be positive and is capped at 200; `offset` is nonnegative. Sending another message during an active turn returns 409.

`settings` includes `modelSourceId`, `modelName`, `thinkingEnabled`, `thinkingEffort`, `planMode`, `allowSave`, `allowLiveTest`, `allowDelete`, and `testBaseUrl`. Permissions are `ask` / `always` / `never`; plan mode blocks gated operations. Test keys are encrypted at rest; session views expose a configured flag rather than plaintext.

Create a session:

```bash
curl http://127.0.0.1:8765/api/agent/sessions \
  -H "Authorization: Bearer <AGENT_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"title":"Gateway inspection","settings":{"modelSourceId":"<SOURCE_ID>","modelName":"<MODEL_ID>"}}'
```

Use the returned session ID to send a message. This calls a model and incurs upstream usage:

```bash
curl -N "http://127.0.0.1:8765/api/agent/sessions/<SESSION_ID>/messages" \
  -H "Authorization: Bearer <AGENT_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"content":"List model sources without changing configuration."}'
```

Each `documents` entry is `{name,mime?,text?,dataUrl?}`. Supply text or a data URL; a server-local path is not an uploaded document.

### SSE and lifecycle

| Event | Contents |
| --- | --- |
| `status` | Model-call or tool-execution phase |
| `text_delta` / `reasoning_delta` | Body / reasoning text returned by the model |
| `tool_call` / `tool_progress` / `tool_result` | Tool call, progress, and result |
| `draft_updated` / `plan_updated` | Draft / working-plan changes |
| `approval_required` | Approval, answer, or plan confirmation required |
| `message` | Persisted message including `seq` |
| `context_updated` / `context_compacted` | Context usage / compaction result |
| `turn_done` / `error` | Completion summary / failure, optionally `retryable` |

Streams send a keepalive every 15 seconds. Closing REST HTTP does not stop the turn; results continue to persist. Read the session detail or list to obtain the final state. Use `/stop` to cancel explicitly. Denied approvals produce tool-denial results so the assistant can continue.

## MCP

This implementation accepts two interaction modes:

| Mode | Contract |
| --- | --- |
| Legacy | Versions `2024-11-05`, `2025-03-26`, `2025-06-18`, `2025-11-25`; `initialize` → `notifications/initialized` → tool calls; no session ID issued |
| Modern | Version `2026-07-28`; include `io.modelcontextprotocol/protocolVersion` in `params._meta` on each request; supports `server/discover` and `resultType` |

POST `Accept` must contain both `application/json` and `text/event-stream`, otherwise 406. GET / DELETE return 405; JSON-RPC batches are unsupported. Browser `Origin` must be same-origin or satisfy loopback rules. A version-header mismatch with a Modern body returns `-32020`; supplied `Mcp-Method` / `Mcp-Name` headers must match the request. Modern `tools/list` also returns `ttlMs` / `cacheScope`.

### Client configuration

Use “Copy MCP JSON” beside an enabled remote key in runtime configuration. It prefers `publicUrl`, otherwise the current address, and retrieves the plaintext key only when copying. A common HTTP client configuration:

```json
{
  "mcpServers": {
    "elysia": {
      "type": "http",
      "url": "https://gw.example.com/mcp",
      "headers": {"Authorization": "Bearer <AGENT_KEY>"}
    }
  }
}
```

For clients with a different format, transfer the `url` and `headers`. MCP publishes only `elysia_cli`, with a required string `command`. Minimal call after Legacy initialization:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {"name": "elysia_cli", "arguments": {"command": "elysia source ls"}}
}
```

### Execution boundaries

- Each business command starts with `elysia`; inspect `elysia help` for syntax. This is neither a system shell nor a standalone terminal executable.
- An Agent key lets MCP execute reads, writes, live tests, and deletes **without the built-in assistant's approval flow**. Command validation, outbound policies, and business constraints still apply.
- Each call is independent. Drafts, test targets, and credentials can be reused only within one `command` and are discarded afterward. Explicit `sessionId` is rejected.
- To update a protocol, prepare a complete draft in the same batch and call `elysia protocol save --update <id>`. The ID must match an existing protocol; omitting `--update` does not overwrite it.
- `&&` skips the remaining chain after failure; `;` continues. There is no batch-wide transaction. Disconnect or timeout cancels remaining work without rolling back completed operations.
- Tools return SSE; `params._meta.progressToken` enables per-command progress. Final results contain JSON text `content` and `structuredContent` shaped as `{ok,summary,output,exitCode}`.
- Business failures set `isError: true`; protocol failures use JSON-RPC errors. Validation failures may contain only error text. Prefer `--limit` for queries; `head` truncates text lines, and output can still be capped.

See the [CLI reference](agent-cli.en.md) for complete semantics.

## A2A

`A2A-Version` defaults to `0.3.0`; `1.0.0` is also supported. Unknown versions return 400. The Agent Card follows the requested version.

| v0.3 method | v1.0 method | Behavior |
| --- | --- | --- |
| `message/send` | `SendMessage` | Create a turn nonblockingly and return a snapshot |
| `message/stream` | `SendStreamingMessage` | Follow over SSE until final state or input required |
| `tasks/get` | `GetTask` | Read a task |
| `tasks/cancel` | `CancelTask` | Stop a running turn; deny a pending input action |
| `tasks/resubscribe` | `SubscribeToTask` | Replay and follow |
| — | `ListTasks` | Cursor pagination |

`contextId` identifies a session; missing or unknown IDs create a session. Each task is one turn, with `taskId` shaped as `sessionID:userMessageSeq`. Running, waiting, completed, failed, and canceled map to `working`, `input-required`, `completed`, `failed`, and `canceled`; v1.0 uses `TASK_STATE_*` enums. Completed artifacts include final text and `{rounds,model,durationMs}`.

To resume an input-required task, send the same `taskId`. Approval or plan confirmation requires an `approved` data part; questions accept text, with `data.answer` taking precedence. Rejected-plan text becomes `note`. v0.3 decision fragment:

```json
{"parts":[{"kind":"data","data":{"approved":true}}]}
```

v1.0 parts omit `kind`. A missing decision data part returns `-32602`. Omitting `taskId` creates another turn in the session. `messageId` deduplication uses a single-instance in-memory LRU and is not durable across restarts. Push notifications and extended cards are not implemented.

## Troubleshooting and permissions

| Symptom | Check |
| --- | --- |
| 401 / 403 | Enabled key with the `agent` scope |
| 404 | `agentRemote.enabled` and proxy paths |
| MCP 406 | Both required `Accept` types |
| Draft missing after a call | MCP is stateless; combine dependent operations in one call |
| REST continues after disconnect | Turns outlive connections; call `/stop` |
| Approval remains pending | Permission settings, plan mode, and an explicit resume decision |

Treat Agent keys as management credentials. Live tests and assistant calls may incur charges. Session and tool output masks known secret fields, but avoid supplying unnecessary credentials or publishing complete conversations for troubleshooting.
