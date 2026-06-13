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

Returns current bootstrap runtime values. Tokens are not returned in plaintext. `webuiDir`, `enablePprof`, and `maxBodyBytes` are bootstrap-only fields and normally changed by restarting the backend or through the standalone Koishi entry plugin.

### `PUT /api/admin/runtime-config`

```json
{ "host": "127.0.0.1", "port": 8765, "logLevel": "debug", "httpTimeout": 120 }
```

Returns `restartRequired: true` when host or port changes. Persisting bootstrap config to disk can be handled by the Koishi entry plugin or an installer tool.

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

Query params: `from`, `to` as RFC3339 timestamps; optional `keyName`, `keyHash`, `groupName`, `modelGroup`, `modelName`, `statusCode`.

### `GET /api/admin/usage/logs`

Adds pagination params `limit` and `offset`.

### `GET /api/admin/usage/logs/:id`

Returns the full stored usage record JSON.

### `POST /api/admin/usage/reset`

Deletes all usage records.

## Logs and Health

- `GET /api/admin/logs?level=info&limit=100&offset=0`
- `GET /api/admin/health`

Health includes basic runtime memory fields so the WebUI can surface memory diagnostics.
