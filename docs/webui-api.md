# WebUI Backend API Reference

Elysia-API now exposes a backend-first WebUI API under `/api/admin`. The WebUI can be developed independently and only needs to call these REST endpoints.

## Authentication

All `/api/admin/*` endpoints require:

```http
Authorization: Bearer <panelAccessToken>
```

`panelAccessToken` is read from bootstrap `config.json`. API relay tokens for `/v1/*` are managed separately through `/api/admin/api-tokens`.

## Response Envelope

Successful responses use:

```json
{ "ok": true, "data": {} }
```

Errors use:

```json
{ "ok": false, "error": { "code": "invalid_json", "message": "..." } }
```

Common error codes: `store_unavailable`, `invalid_json`, `list_sources_failed`, `save_source_failed`, `fetch_source_failed`, `list_models_failed`, `save_group_failed`, `save_token_failed`, `usage_logs_failed`, `usage_log_not_found`.

## Bootstrap `config.json`

```json
{
  "host": "127.0.0.1",
  "port": 8765,
  "panelAccessToken": "change-me",
  "databasePath": "elysia-api.sqlite3",
  "logLevel": "info",
  "httpTimeout": 120,
  "secretKeyPath": ".master-key",
  "webuiDir": "",
  "enablePprof": false,
  "maxBodyBytes": 33554432
}
```

`webuiDir` 为可选项：**留空时后端使用内嵌的 WebUI**（`//go:embed`，开箱即用，启动后访问 `/ui/` 即可），仅在需要用外部目录覆盖内嵌版本时才填写。

Legacy `server`, `dashboardToken`, `tokens`, and `modelGroups` fields are still imported for compatibility, but new WebUI data should live in SQLite.

## Runtime Config

### `GET /api/admin/runtime-config`

Returns current bootstrap runtime values. Tokens are not returned in plaintext. `webuiDir`, `enablePprof`, and `maxBodyBytes` are bootstrap-only fields and normally changed by restarting the backend.

### `PUT /api/admin/runtime-config`

```json
{ "host": "127.0.0.1", "port": 8765, "logLevel": "debug", "httpTimeout": 120 }
```

Accepts an optional `usageLog` block (all fields partial; numeric `0` is an explicit value):

```json
{
  "usageLog": {
    "persistEnabled": true,
    "retentionDays": 30,
    "maxStorageMB": 1024,
    "maxRecords": 0,
    "bodyMaxKB": 1024,
    "bodyOnErrorOnly": false,
    "externalizeMedia": true,
    "cleanupIntervalMinutes": 60
  }
}
```

`usageLog` changes apply immediately: body cap / switches take effect for subsequent requests, retention parameters are re-read by the background cleanup loop on its next tick. Returns `restartRequired: true` when host or port changes. Persisting bootstrap config to disk is handled by the backend `config.Save()` path; process restarts should be handled by the operator or service manager.

## Model Sources

A source describes an upstream provider and either auto-fetches models or stores manual models.

```json
{
  "id": "openai-main",
  "name": "OpenAI Main",
  "baseUrl": "https://api.openai.com/v1",
  "apiKey": "sk-...",
  "platform": "openai",
  "enabled": true,
  "autoFetchModels": true,
  "manualModels": []
}
```

Supported `platform` values: `openai`, `openai-compatible`, `claude`, `gemini`.

- `GET /api/admin/model-sources`
- `POST /api/admin/model-sources`
- `PUT /api/admin/model-sources/:id`
- `DELETE /api/admin/model-sources/:id`
- `POST /api/admin/model-sources/:id/fetch`
- `POST /api/admin/models/refresh`

Manual source example:

```json
{
  "id": "local",
  "name": "Local Provider",
  "baseUrl": "http://127.0.0.1:8000/v1",
  "apiKey": "local-key",
  "platform": "openai-compatible",
  "enabled": true,
  "autoFetchModels": false,
  "manualModels": [
    { "id": "local-model", "name": "Local Model", "type": "llm", "available": true }
  ]
}
```

## Models

### `GET /api/admin/models`

Returns cached models aggregated from sources. Each model includes `id`, `name`, `sourceId`, `sourceName`, `baseUrl`, `platform`, `type`, `maxTokens`, capability booleans, `thinkingMode`, `available`, and `lastCheckedAt`.

## Model Groups

Model groups are the public model IDs shown to relay clients through `/v1/models`.

```json
{
  "id": "default-chat",
  "name": "gpt-default",
  "enabled": true,
  "models": ["gpt-4.1-mini", "local-model"],
  "strategy": "round-robin",
  "maxRetries": 3,
  "retryInterval": 1000,
  "maxConcurrency": 10,
  "dailyLimitMaxRequests": 0,
  "dailyLimitMaxTokens": 0,
  "type": "llm",
  "maxTokens": 0,
  "visionCapable": true,
  "toolsCapable": true
}
```

- `GET /api/admin/model-groups`
- `POST /api/admin/model-groups`
- `PUT /api/admin/model-groups/:id`
- `DELETE /api/admin/model-groups/:id`

Supported strategy values: `round-robin`, `sequential`, `random`.

## API Tokens

Relay clients use these tokens for `/v1/*` and `/v1beta/*`.

```json
{ "name": "default", "token": "client-token", "enabled": true }
```

- `GET /api/admin/api-tokens`
- `POST /api/admin/api-tokens`
- `PUT /api/admin/api-tokens/:name`
- `DELETE /api/admin/api-tokens/:name`

List responses mask tokens; create/update accepts plaintext.

## Usage

### `GET /api/admin/usage/stats`

Query params: `from`, `to` as RFC3339 timestamps; optional `keyName`, `keyHash`, `groupName`, `modelGroup`, `modelName`, `sourceId`, `statusCode`. Repeated params (`keyName`, `groupName`, `modelName`, `sourceId`) are treated as multi-select.

### `GET /api/admin/usage/pulse`

Query params: same time filters as stats, plus `utcOffsetMinutes` and `bucketMinutes` (`1`, `5`, or `15`).
`from` is required; `[from, to)` must be at most 48 hours (`to` omitted uses now).
Returns `{ points, window }` where `points` is `{ t, requests, avgDurationMs, p95DurationMs }[]` (`t` is the bucket start in Unix milliseconds) and `window` is `{ requests, avgDurationMs, p95DurationMs, totalTokens }`. Window `requests` / `avgDurationMs` are exact. Window and bucket `p95DurationMs` are exact up to 16384 samples, then a reservoir-sampling estimate. They are not a mean of bucket P95s.

### `GET /api/admin/usage/by-model-daily`

Query params: same time filters as stats, plus `utcOffsetMinutes` and optional `top` (1–20, default 8).
Returns `{ date, model, requests, isOther }[]`. Models outside the top-N by request count are merged into one row with `isOther: true` and empty `model`; the client chooses the display label.

### `GET /api/admin/usage/logs`

Adds pagination params `limit` and `offset`.

### `GET /api/admin/usage/logs/:id`

Returns the full stored usage record JSON.

### `POST /api/admin/usage/reset`

Deletes all usage records. Externalized media assets under the `usage-assets/` directory are removed as well.

### `GET /api/admin/usage/assets/:requestId/:file`

Serves an externalized media asset for a usage record (images / audio / video / files captured from logged bodies; the body itself stores a `__ELYSIA_ASSET__:<requestId>/<hash>.<ext>` placeholder instead of the base64 payload). `file` must match `<16-hex>.<ext>`; requests are admin-authenticated like all other admin endpoints.

### `GET /api/admin/usage/storage`

Returns log storage status: `db` (`totalBytes`, `logicalBytes`, `pageCount`, `pageSize`, `freePages`), `recordCount`, `assets` (`bytes`, `files`, `dirs`), the effective `config` (usageLog block), and `lastCleanup` (result of the most recent retention pass).

### `POST /api/admin/usage/cleanup`

Triggers one retention pass asynchronously (TTL / record-count / storage-cap cleanup plus orphan asset sweep). Returns `{ accepted }`; `false` means a pass is already running.

## Protocol Agent (AI Assistant)

The AI assistant (`/agent` page) is a general-purpose, server-side tool-calling agent for the gateway: protocol engineering (read API docs, draft custom protocol configs, offline preview, user-approved upstream tests, save), model source & model group management (create/update, user-approved), usage statistics with inline charts (```chart fenced specs rendered by the WebUI), and error/log analysis (failed-request drill-down with captured bodies). Test credentials are supplied in the
conversation: the model passes them as `test_upstream` / `test_model_list` arguments (`baseUrl` / `apiKey`), they are
shown masked on the approval card, and remembered (encrypted) for the rest of the session. A `update_plan` tool lets
the agent maintain a step checklist surfaced live in the side panel (plan / draft / progress tabs). Sessions and messages persist in SQLite (`agent_sessions` / `agent_messages`); every model call is recorded into usage stats under key name `AI 协议助手` with `relayMode=agent-assist`.

### `GET /api/admin/agent/sessions`

Lists sessions (newest first). Each item: `id`, `title`, `mode` (`create`|`edit`), `protocolId`, `seedConfig`, `draftConfig`, `settings`, `status` (`idle`|`running`|`waiting_approval`), `pendingAction`, `createdAt`, `updatedAt`. The API key is never returned (only `settings.testApiKeySet`).

### `POST /api/admin/agent/sessions`

Creates a session: `{ title?, mode?: "create"|"edit", protocolId?, settings? }`. Edit mode requires an existing `protocolId` and seeds the draft with its stored config.

### `GET /api/admin/agent/sessions/:id`

Returns `{ session, messages }` — the full message history (`seq`-ordered; roles: `user` / `assistant` / `tool_result` / `approval` / `system`).

### `PATCH /api/admin/agent/sessions/:id`

Updates settings: `{ title?, settings?: { modelSourceId, modelName, thinkingEnabled, thinkingEffort ("low"|"medium"|"high"|"max"|"adaptive"), allowLiveTest, allowSave ("ask"|"always"|"never"), testBaseUrl }, apiKey?, clearApiKey? }`. The API key is encrypted at rest.

### `DELETE /api/admin/agent/sessions/:id` · `DELETE /api/admin/agent/sessions/:id/messages?afterSeq=0`

Deletes a session (stops a running turn first) or clears/truncates its messages while keeping the session, draft, and settings.

### `POST /api/admin/agent/sessions/:id/messages`

Sends a user message and streams the turn over SSE. Body: `{ content?, documents?: [{ name, mime?, text? | dataUrl? }], afterSeq? }` — `afterSeq` truncates messages with `seq > afterSeq` first, covering retry / edit-resend / regenerate. Responds `text/event-stream` with named events:

- `status` — phase text (calling model / executing tool)
- `text_delta` / `reasoning_delta` — streamed body / chain-of-thought increments
- `tool_call` / `tool_result` — tool invocation and result (persisted)
- `draft_updated` — the working config draft changed
- `plan_updated` — the working plan checklist changed (`update_plan` tool)
- `message` — a message was persisted (full message incl. `seq`)
- `approval_required` — the turn paused on a gated action (test / save); payload carries the pending tool calls
- `turn_done` — turn finished (aggregated usage, rounds, duration)
- `error` — turn failed (`retryable` flag)

Turns are detached from the HTTP request: closing the stream does not cancel the turn; results are persisted and visible on reconnect. `409` if a turn is already running.

### `POST /api/admin/agent/sessions/:id/approve`

Resolves a pending approval: `{ approved, baseUrl?, apiKey?, note? }` and streams the resumed turn over SSE (same event format). Denying synthesizes tool-result denials so the agent can adapt.

### `POST /api/admin/agent/sessions/:id/stop`

Cancels the running turn (partial output is persisted). Returns `{ stopped }`.


## Logs and Health

- `GET /api/admin/logs?level=info&limit=100&offset=0`
- `GET /api/admin/health`

Health includes basic runtime memory fields so the WebUI can surface memory diagnostics.
