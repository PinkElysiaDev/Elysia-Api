# Data model

[Documentation](README.en.md) · [简体中文](webui-data-model.md) · **English**

Core fields and semantics of management APIs. Use [frontend types.ts](../packages/webui/src/lib/types.ts), [storage types](../backend/storage/types.go), and [Agent types](../packages/webui/src/lib/agent/types.ts) to inspect complete definitions; handlers determine request/response behavior. The WebUI manages data over HTTP rather than reading configuration files directly.

## Common types

```ts
type ApiResult<T> =
  | { ok: true; data: T }
  | { ok: false; error: { code: string; message: string } }

type Platform = 'chat_completions' | 'responses' | 'anthropic' | 'gemini'
  | 'openai' | 'openai-compatible' | 'claude' | `custom:${string}`
type ModelType = 'llm' | 'embedding' | 'reranker'
type GroupStrategy = 'round-robin' | 'sequential' | 'random'
type SourceKeyStrategy = 'single' | 'round-robin' | 'random' | 'priority'
type ThinkingMode = 'both' | 'non-thinking-only' | 'thinking-only'
type LogLevel = 'debug' | 'info' | 'warn' | 'error'
```

Legacy values `openai`, `openai-compatible`, and `claude` remain readable; use canonical wire identifiers for new configuration. Type fields such as `embedding` and `reranker` do not imply corresponding inference routes exist. See [API](webui-api.en.md) and [protocols](maheshvara-protocol.en.md).

## Runtime config

| Field | Semantics |
| --- | --- |
| `host`, `port` | Configured listener; port 1–65535; restart to change the listener |
| `panelAccessToken` | Plaintext panel token behind admin authentication; not the obsolete `panelAccessTokenConfigured` field |
| `databasePath`, `defaultDatabasePath` | Current and default database paths |
| `logLevel`, `httpTimeout`, `enablePprof` | Logging, timeout in seconds, diagnostics switch |
| `outbound` | `deniedIpRanges` and read-only `defaultDeniedIpRanges` |
| `usageLog` | Body capture, persistence, and retention |
| `modelCatalog` | `enabled`, `url`, `proxy`, `syncIntervalMinutes`; explicit interval 0 disables periodic sync |
| `agentRemote` | `enabled`, `publicUrl` |

`RuntimeConfigUpdate` contains only writable management fields, not every returned field. Updates return `{updated, restartRequired}`. See [deployment](deployment.en.md#configuration) for defaults and restart requirements.

## Model source

| Field | Semantics |
| --- | --- |
| `id`, `name` | Stable identifier and display name |
| `baseUrl`, `platform` | Forwarding address and upstream wire protocol |
| `apiKey`, `apiKeys`, `keyStrategy` | Single-key fallback, multiple keys, scheduling; list reads mask secrets |
| `enabled`, `autoFetchModels` | Source availability and discovery mode |
| `manualModels` | Manual IDs, names, types, capabilities, context limits, and availability |
| `fetchBaseUrl` | Separate discovery address; empty uses the source address |
| `refreshState` | Runtime task state, not persisted configuration |
| `createdAt`, `updatedAt` | Timestamps |

`refreshState` contains `refreshing`, `lastCount`, `lastAdded`, `lastRemoved`, `lastError`, `lastFinishedAt`, and per-key `lastKeys` results. Poll after starting a fetch.

Multi-key entry:

```ts
interface SourceAPIKey {
  value: string
  note?: string
  disabled?: boolean
  fetchedModels?: string[]
  allowedModels?: string[]
}
```

If `allowedModels` is absent, use the fetched set. If both are absent, access is unrestricted. An explicit empty array disables all models for that key. Do not collapse absent and empty values.

## Model

| Fields | Meaning |
| --- | --- |
| `id`, `sourceId` | Composite identity; model IDs may contain `/` |
| `name`, `sourceName`, `baseUrl`, `platform`, `type` | Display and origin information |
| `maxTokens`, `visionCapable`, `toolsCapable`, `structuredOutput`, `thinkingMode` | Capabilities and context limits |
| `enabled` / `available` | Manual switch / health state; both must be true, along with source and routing constraints |
| `origin` | `fetched` or `manual`; refresh does not overwrite manual rows |
| `capabilitySource` | Empty, `catalog`, or `manual`; refresh retains user-edited capabilities |
| `lastCheckedAt` | Most recent check |

A catalog miss retains available manual capability values. Model capabilities can guide candidate selection; group capabilities constrain requests. They are not interchangeable.

## Model group

| Field | Semantics |
| --- | --- |
| `id`, `name`, `enabled` | Management ID, client-visible model name, enabled state |
| `models` | Member references; prefer `sourceId:modelId` |
| `strategy` | `round-robin` / `sequential` / `random` |
| `maxRetries`, `retryInterval` | Retry settings; interval in milliseconds |
| `maxConcurrency`, `dailyLimitMaxRequests`, `dailyLimitMaxTokens` | Concurrency and daily limits; 0 is unlimited |
| `type`, `maxTokens`, `visionCapable`, `toolsCapable` | Group type and capabilities |

Deleting a group disables tokens that lose all explicitly authorized groups; an emptied allow list must not silently grant every group.

## API token

`ApiToken` contains `name`, `token`, `enabled`, `allowedGroups`, `scopes`, `createdAt`, and `updatedAt`. `newName` is used only in rename requests.

- List and save responses mask `token`; `reveal` returns plaintext on demand.
- An empty `allowedGroups` is unrestricted for ordinary tokens; otherwise authorization uses group names.
- A scope containing `agent` is for remote operations and cannot call inference endpoints. Group authorization is independent of this scope.
- Clear plaintext from forms after saving. Do not submit masked output as a replacement secret.

## Usage

| Type | Contents |
| --- | --- |
| `UsageStats` | `requests`, `success`, `failed`, `inputTokens`, `outputTokens`, `totalTokens`, `cacheHitTokens`, `cacheHitRate`, `avgDurationMs`, `avgFirstByteMs` |
| `UsageTrendPoint` | Local `date`, success/failure counts, total and optional detailed tokens, `modelTokens` |
| `UsageModelStat` | `model`, `requests`, `failed`, `tokens` |
| `UsagePulseResult` | `points` and `window`; bucket `t` is Unix milliseconds; window includes count, mean duration, P95, tokens |
| `UsageModelDailyPoint` | `date`, `model`, `requests`, `isOther` |
| `UsageLogsResult` | `{items, total}` |

`cacheHitRate` is in `[0, 1]`. Request times use RFC3339 and filtering uses `[from, to)`. Daily aggregation uses a fixed UTC offset rather than automatic daylight-saving rules.

`UsageLogItem` includes request ID, start time, caller name/hash, original `requestedModelGroup`, selected `groupName` / `modelName`, source, protocols, conversion mode, status, error, streaming flag, timings, tokens, and truncation flags. Pre-routing failures may have no selected model but retain the requested group name.

`UsageLogDetail` adds end time, group/model IDs, endpoints, conversion chain, usage source, warnings, `usageDetail`, `builtinToolUsage`, `retryCount`, `retryEvents`, and four bodies: `incomingBody`, `outgoingBody`, `providerResponse`, `downstreamResponse`. `errorKind` may be `client_canceled` or `upstream`.

Body shape:

```ts
interface UsageBody {
  content: string
  truncated: boolean
}
```

`content` may contain JSON text but is not guaranteed to parse. Disabled capture leaves bodies empty; externalized media uses `__ELYSIA_ASSET__` placeholders. Truncation describes the logging copy, not necessarily the response delivered to the client.

Frontend multi-select fields `keyNames`, `groupNames`, `modelNames`, and `sourceIds` in `UsageQueryParams` serialize as repeated singular query parameters. See the [API](webui-api.en.md#usage) for other filters.

## System logs and health

`SystemLog` contains `id`, `createdAt`, `level`, `message`, and optional `fields`; pagination returns `{items, total}`. `Health` contains `status`, `database`, and `memory.{alloc,sys,numGC}`; memory values are bytes.

## Protocol and Agent types

Custom protocol types are the `CustomProtocol*` definitions in the same `types.ts`. The schema endpoint supplies the configurable field catalog. Do not use a complete internal model as an HTTP request body. See [custom protocols](protocol-definition-reference.en.md).

For Agent sessions, messages, settings, pending actions, charts, and SSE shapes, see [remote access](remote-agent-api.en.md) and the Agent type file. Distinguish persisted state from current runtime state.
