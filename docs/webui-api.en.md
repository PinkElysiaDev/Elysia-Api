# Management API

[Documentation](README.en.md) · [简体中文](webui-api.md) · **English**

The WebUI manages the gateway through `/api/admin`. Routes are defined in [admin.go](../backend/server/admin.go); field semantics are in the [data model](webui-data-model.en.md). Paths below are relative to the running gateway.

## Authentication

Use the panel token:

```http
Authorization: Bearer <PANEL_TOKEN>
```

The panel token comes from `config.json`. Browsers may also authenticate with the `panel_access_token` cookie. Relay tokens and Agent keys do not replace it. Requests normally use `Content-Type: application/json`.

## Responses

Management handlers normally return these envelopes:

```json
{"ok": true, "data": {}}
```

```json
{"ok": false, "error": {"code": "invalid_json", "message": "Invalid request body"}}
```

Lists normally use `data.items`; paginated lists add `data.total`. Authentication middleware returns 401 as `{"error":"..."}`. Reload and SSE endpoints use their own formats; not every response contains `ok`.

Common failures: 400 validation error; 401 invalid panel token; 404 missing object; 409 conflict or running task; 503 `store_unavailable`. Operation-specific codes include `save_source_failed`, `save_group_failed`, `save_token_failed`, and `usage_log_not_found`.

## Bootstrap configuration

See [deployment](deployment.en.md#configuration) for fields, environment variables, and data paths. Sources and tokens use SQLite; current parsing does not import legacy `tokens`, `modelGroups`, or `dashboardToken` fields.

## Runtime config

| Method | Path | Result / behavior |
| --- | --- | --- |
| GET | `/api/admin/runtime-config` | Current settings and effective policies; **includes plaintext `panelAccessToken`**, for administrators only |
| PUT | `/api/admin/runtime-config` | Update provided fields; return `{updated, restartRequired}` |
| POST | `/api/admin/reload` | Read configuration again; return reload results such as `{reloaded, debugMode, verboseLog, serverChangedRequiresRestart, server}` |
| POST | `/api/admin/restart-required/check` | Return restart-prompt state |

Partial update example:

```json
{
  "httpTimeout": 120,
  "usageLog": {"bodyMaxKB": 0, "retentionDays": 30},
  "agentRemote": {"enabled": true, "publicUrl": "https://gw.example.com"}
}
```

Writable fields: `host`, `port`, `logLevel`, `httpTimeout`, `panelAccessToken`, `databasePath`, `enablePprof`, `outbound.deniedIpRanges`, `usageLog`, `systemLog`, `modelCatalog.syncIntervalMinutes`, and `agentRemote`.

Omitted fields remain unchanged. `usageLog` merges individual fields and accepts explicit `0`. `outbound` replaces the whole deny list; an empty list permits all ranges. `agentRemote.publicUrl: ""` clears the public address. `webuiDir`, `secretKeyPath`, and `maxBodyBytes` are not writable through this endpoint.

Listener, port, database path, and pprof changes require restart. HTTP timeout and body-capture policies can affect subsequent requests; cleanup settings are read on the next background pass. `bodyMaxKB` defaults to 0 and needs a positive value to capture bodies. Do not log or expose the token in configuration responses.

## Model sources

| Method | Path | Behavior |
| --- | --- | --- |
| GET | `/api/admin/model-sources` | Return `items`, including `refreshState` |
| POST | `/api/admin/model-sources` | Save a source; derive an omitted ID from its name |
| PUT | `/api/admin/model-sources/:id` | Save using the path ID |
| PATCH | `/api/admin/model-sources/:id/enabled` | `{enabled: boolean}`; toggle without fetching again |
| DELETE | `/api/admin/model-sources/:id` | Delete the source and related data |
| POST | `/api/admin/model-sources/:id/fetch` | Start background discovery; return `{started, alreadyRunning?}` |
| POST | `/api/admin/models/refresh` | Start discovery for enabled sources; return `{started, total}` |
| GET | `/api/admin/model-catalog/status` | Model capability catalog status |
| POST | `/api/admin/model-catalog/refresh` | Refresh now; return `{refreshed, status}` |

Manual source example; replace the address, key, and model ID before saving:

```json
{
  "id": "upstream-main",
  "name": "Main upstream",
  "baseUrl": "https://api.example.com/v1",
  "apiKey": "<UPSTREAM_API_KEY>",
  "platform": "chat_completions",
  "enabled": true,
  "autoFetchModels": false,
  "manualModels": [{"id": "provider-model", "name": "Provider model", "type": "llm", "available": true}]
}
```

Use `chat_completions`, `responses`, `anthropic`, `gemini`, or `custom:<id>` for the protocol. Legacy `openai`, `openai-compatible`, and `claude` values have compatibility handling. `fetchBaseUrl` can override model discovery. Set `autoFetchModels: true` for discovery-based sources.

`apiKeys` supports multiple keys and per-key model permissions. `keyStrategy` accepts `single`, `round-robin`, `random`, or `priority`. A fetch response means accepted, not completed; poll `refreshState`. Some writes conflict while fetching. Refresh preserves manual models and user-edited capability fields.

## Models

| Method | Path | Behavior |
| --- | --- | --- |
| GET | `/api/admin/models?sourceId=&search=` | Filter by source and text; return `items` |
| PATCH | `/api/admin/models/:sourceId?modelId=` | Update name, type, capabilities, `maxTokens`, `thinkingMode`, or `enabled` |
| DELETE | `/api/admin/models/:sourceId?modelId=` | Delete a model and clean up group references |

Model IDs may contain `/`, so pass them as URL-encoded `modelId` query values. Identify a model by `(sourceId, id)` across sources.

## Model groups

Clients use the group name as their requested `model`. Prefer `sourceId:modelId` member references to avoid ambiguity:

```json
{
  "id": "default",
  "name": "default",
  "enabled": true,
  "models": ["upstream-main:provider-model"],
  "strategy": "round-robin",
  "maxRetries": 3,
  "retryInterval": 1000,
  "maxConcurrency": 10,
  "dailyLimitMaxRequests": 0,
  "dailyLimitMaxTokens": 0,
  "type": "llm"
}
```

| Method | Path | Behavior |
| --- | --- | --- |
| GET / POST | `/api/admin/model-groups` | List / save |
| PUT / DELETE | `/api/admin/model-groups/:id` | Save / delete |
| POST / DELETE | `/api/admin/model-groups/:id/models` | `{models: ["source:model"]}`; add / remove members |

Strategies: `round-robin`, `sequential`, `random`. `retryInterval` is in milliseconds. Zero concurrency or daily limits mean unlimited. Deleting a group may disable tokens authorized only for that group; `disabledTokens` lists them, preventing an empty allow list from unintentionally widening access.

## API tokens

```json
{"name":"client-main","token":"<RANDOM_RELAY_TOKEN>","enabled":true,"allowedGroups":["default"],"scopes":[]}
```

| Method | Path | Behavior |
| --- | --- | --- |
| GET / POST | `/api/admin/api-tokens` | Masked list / save |
| PUT / DELETE | `/api/admin/api-tokens/:name` | Update / delete |
| GET | `/api/admin/api-tokens/:name/reveal` | Read plaintext `{name, token}` on demand |

Creation requires a nonempty token. On update, an empty token preserves the secret; omitted `allowedGroups` / `scopes` preserve values, while explicit empty arrays clear them. Use `newName` to rename; rename failure does not roll back previously saved attributes. Empty `allowedGroups` means unrestricted model-group access for ordinary relay tokens. Keys carrying `agent` are for remote operations and are rejected by inference endpoints.

## Usage

Use RFC3339 timestamps and the range `[from, to)`. Repeat `keyName`, `groupName`, `modelName`, or `sourceId` for multi-select; do not join them with commas. Other filters include `keyHash`, `statusCode`, `status=success|failed`, and the legacy group-name alias `modelGroup`.

| GET path (prefix `/api/admin/usage`) | Contents |
| --- | --- |
| `/stats` | Requests, success/failure, tokens, cache hit rate, and average durations |
| `/trend` | Local-day aggregation; `utcOffsetMinutes` is local time minus UTC in minutes |
| `/by-model` | List of `{model, requests, failed, tokens}` |
| `/by-model-daily` | `top` is 1–20, default 8; list of `{date, model, requests, isOther}` |
| `/pulse` | Required `from`, at most 48 hours; `bucketMinutes` is 1, 5, or 15; `{points, window}` |
| `/logs` | `limit` / `offset` pagination; `{items, total}` |
| `/logs/:id` | Stored request detail; body availability depends on capture policy |
| `/seq` | Usage sequence for refresh detection |
| `/assets/:requestId/:file` | Admin-authenticated media; filename `<16-hex>.<ext>` |
| `/storage` | `db`, `recordCount`, `assets`, effective `config`, `lastCleanup` |

`pulse.points[].t` is Unix milliseconds. P95 is exact up to 16384 samples, then reservoir-sampled; window P95 is not a mean of bucket P95s. The other-model row in `by-model-daily` uses `isOther: true` and an empty `model`; clients choose its display label.

`POST /api/admin/usage/cleanup` queues retention and physical space reclamation. `GET /api/admin/usage/maintenance` reports progress, blocking readers and failures. `POST /api/admin/usage/reset` synchronously clears request logs, aggregates and attachment references, returning `{reset, reclaimQueued}`; attachment files and database space are reclaimed in the background. See [deployment](deployment.en.md#request-logs) for capture and retention policies.

## Protocols and assistant

| Method | Path | Behavior |
| --- | --- | --- |
| GET | `/api/admin/custom-protocols` | List protocols |
| GET | `/api/admin/custom-protocols/schema` | Field catalog and constraints |
| PUT / DELETE | `/api/admin/custom-protocols/:id` | Save / delete a definition |
| POST | `/api/admin/custom-protocols/preview` | Offline mapping preview |
| POST | `/api/admin/custom-protocols/test` | Live upstream test; may incur charges |
| POST | `/api/admin/custom-protocols/test-models` | Live model discovery test |

See [protocol definitions](protocol-definition-reference.en.md) and frontend types `CustomProtocolPreviewResult` / `CustomProtocolTestResult` for request and result contracts.

`/api/admin/agent/*` shares session handlers with [remote REST](remote-agent-api.en.md#rest), but uses the panel token. See that document for session creation/editing, messages, approvals, stopping, truncation, and draft restoration. The assistant operates through `elysia_cli`, with `ask_user` and `update_plan` as supporting tools. Model calls count toward usage with `relayMode=agent-assist`. Session views mask credentials; test-credential reuse is limited to protocol-testing commands.

## System logs and health

- `GET /api/admin/logs?level=info&limit=100&offset=0`: paginated system logs.
- `GET /api/admin/health`: database status, allocated/system memory, and GC count.
- `GET /health`: public liveness check, separate from detailed admin diagnostics.

## Minimal check

```bash
curl http://127.0.0.1:8765/api/admin/health \
  -H "Authorization: Bearer <PANEL_TOKEN>"
```

On success, inspect `ok` and `data`. On 401, verify the token type and configuration path first.
