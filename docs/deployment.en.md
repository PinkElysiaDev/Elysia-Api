# Deployment and operations

[Documentation](README.en.md) · [简体中文](deployment.md) · **English**

Installation, upgrades, backups, and troubleshooting. See [development](development.en.md) for source builds and [quick start](../README.en.md#quick-start) for the first model request.

## Installation and startup

Download the matching artifact and `SHA256SUMS` from [Releases](https://github.com/PinkElysiaDev/Elysia-Api/releases/latest). Windows / Linux provide amd64 and arm64 binaries; macOS provides a universal DMG. Verify the downloaded file's SHA256 before launching it.

### Windows / Linux

Start from the download directory. This example uses Linux amd64:

```bash
chmod +x ./elysia-api-linux-amd64
./elysia-api-linux-amd64 --config ./config.json
```

On Windows use `.\elysia-api-windows-amd64.exe --config .\config.json`; on ARM64 use the matching arm64 filename.

- Without `--config`, the path is `config.json` in the **current working directory**, which is not necessarily the executable directory.
- A missing file is created automatically and a random panel token is printed to the startup log. A malformed file causes startup to fail without overwriting it.
- The default listener is `127.0.0.1:8765`. Standalone binaries report a port conflict instead of selecting another port.
- Open `/ui/` and sign in with the panel token. Rotate it after the first sign-in and protect the configuration and startup logs.
- Startup attempts to open a browser. Set `openBrowserOnStart: false` on headless hosts.

### macOS app

Drag ElysiaApi from the DMG into Applications. Requires macOS 12+ and supports Intel and Apple Silicon. Running and updating do not require Xcode.

The main shell acts as a menu-bar supervisor for the backend, health checks and updates. Opening the panel starts a separate WebUI child using the bundled `ElysiaApi` executable with `--webui-process`; that child owns the window and WebKit. Closing the window exits the WebUI and its WebKit services while retaining the supervisor and original backend. Releasing Swift references to `WKWebView` alone does not end WebKit services, so resource cleanup uses the owning process's exit as its boundary.

| Action | Behavior |
| --- | --- |
| First sign-in | Copy the panel token from the menu bar; credentials are not injected automatically |
| Data directory | `~/Library/Application Support/ElysiaApi/`, containing configuration, database, master key, and runtime log |
| Port conflict | Try the configured port, then an available port in `8799–8899`; use the address shown in the menu |
| Close window | Exit the WebUI child and its WebKit services; retain the menu bar and original backend, authentication, position and theme |
| Start / restart service | Start when not running; otherwise wait for graceful shutdown before starting a new backend process |
| Quit | Request graceful backend shutdown, then terminate the child if it times out |
| Recovery | Check every 30 seconds while healthy in the background, every 3 seconds with the panel open or during startup/recovery. Process-exit callbacks handle backend crashes promptly; restart after sustained health failures, stop after 3 consecutive attempts and offer manual retry |
| Launch at login | Disabled by default; enabling it starts only the menu bar item at login |
| Notifications | Important notifications default to enabled; request system authorization on first use or explicit enable |
| Update | Validate and stage the DMG app; stop the backend and old shell before helper replacement; retain the old bundle until the new service and panel are ready, roll back failures, and preserve configuration and database |

The menu provides service start and restart, API address and token copying, logs, preferences, browser access, and update checks. The managed backend uses `ELYSIA_API_OPEN_BROWSER=false`. The WebUI uses WebKit's default persistent data store for cookies and authentication after manual sign-in; application preferences retain theme, window size and position. Recreating the child does not clear this data. Signing out requires authentication again.

CI artifacts use ad-hoc signing, which is not Developer ID notarization. After verifying the file came from the project Release, a Gatekeeper block can be cleared with:

```bash
xattr -d com.apple.quarantine /Applications/ElysiaApi.app
```

See [macOS validation](macos-testing.en.md) for detailed behavior and checks.

### Docker

Build the image from the repository root. Listen on `0.0.0.0` inside the container while binding the host port to loopback:

```bash
docker build -t elysia-api:local .
docker run -d --name elysia-api \
  --restart unless-stopped --init \
  --read-only --tmpfs /tmp:size=64m,mode=1777 \
  --security-opt no-new-privileges:true --cap-drop ALL \
  -p 127.0.0.1:8765:8765 \
  -e ELYSIA_API_HOST=0.0.0.0 \
  -v elysia-data:/data \
  elysia-api:local
docker logs elysia-api
```

The image runs as UID/GID `65532` with `/data/config.json`. The named volume retains configuration, database, and keys. Bind-mounted directories must be writable by that user.

Alternatively, save this as `compose.yaml` in the repository root:

```yaml
services:
  elysia-api:
    build: .
    image: elysia-api:local
    container_name: elysia-api
    restart: unless-stopped
    init: true
    read_only: true
    tmpfs:
      - /tmp:size=64m,mode=1777
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    ports:
      - "127.0.0.1:8765:8765"
    environment:
      ELYSIA_API_HOST: 0.0.0.0
      ELYSIA_API_OPEN_BROWSER: "false"
    volumes:
      - elysia-data:/data
volumes:
  elysia-data:
```

```bash
docker compose up -d --build
docker compose logs elysia-api
```

Use a reverse proxy for TLS and external access; expose ports only to intended networks. If changing the internal port, also update the mapping and the image's health and shutdown probes, which use `8765`.

## Configuration

These are the main fields written on first launch. Replace the example token; do not deploy with `change-me`:

```json
{
  "host": "127.0.0.1",
  "port": 8765,
  "panelAccessToken": "change-me",
  "databasePath": "elysia-api.sqlite3",
  "secretKeyPath": ".master-key",
  "logLevel": "info",
  "httpTimeout": 120,
  "openBrowserOnStart": true
}
```

| Field | Default and purpose |
| --- | --- |
| `host` / `port` | `127.0.0.1` / `8765`; restart to change the listener |
| `panelAccessToken` | Generated on first creation; protects management APIs, not inference |
| `databasePath` | `elysia-api.sqlite3`; business data including models, tokens, and sessions |
| `logDatabasePath` | Defaults to the business filename with `-logs` before its extension; dedicated system/usage logs, aggregates and media references; changing it requires restart |
| `secretKeyPath` | `.master-key`; encryption key for sensitive SQLite fields |
| `logLevel` | `info`; accepts `debug`, `info`, `warn`, `error` |
| `logPersistLevel` | `info`; independent database diagnostic threshold; operations and errors always attempt persistence |
| `logFormat` | `auto`: readable terminal output, JSON when redirected; also accepts `json` / `console` |
| `httpTimeout` | Total timeout for non-streaming HTTP requests; 120 seconds in an automatically created config, while explicit `0` or an omitted field means unlimited |
| `openBrowserOnStart` | Attempts to open a browser when omitted |
| `webuiDir` | Empty uses embedded assets; nonempty overrides the WebUI directory |
| `enablePprof` | `false`; enables admin-authenticated `/debug/pprof` routes; restart after changes |
| `maxBodyBytes` | `33554432` (32 MiB); request body limit |
| `debugMode` / `verboseLog` | Disabled by default; verbose logging requires both |

Relative `databasePath`, `logDatabasePath`, `secretKeyPath`, and `webuiDir` values resolve against the configuration directory. The databases must be different files, including symlink and hard-link aliases. Model sources, groups, and API tokens are stored in SQLite through management APIs.

SSE/NDJSON streams use the protocol’s `framing.idleMillis`: omitted or `0` means 10 minutes; explicit values accept 1–3600000 milliseconds. This bounds waiting for headers and idle reads between data, not total stream duration; `httpTimeout: 0` does not disable it. Adjust the protocol field for upstreams with long reasoning pauses. Caller cancellation still takes effect immediately. Longer waits do not resolve upstream 429 rate limits, and generation is never replayed after streaming starts.

### Environment variables and precedence

| Variable | Effect |
| --- | --- |
| `ELYSIA_API_HOST` | Overrides the configured listener address on load and reload |
| `ELYSIA_API_OPEN_BROWSER` | Overrides browser startup without writing the file; an invalid boolean logs a warning and falls back |
| `ELYSIA_API_MASTER_KEY` | Takes precedence over key files; inject through a protected runtime environment |

Key lookup order: environment variable → `secretKeyPath` → generate a random key. Legacy `.db-key` files are not read, and encrypted storage rejects plaintext secret fields. Key creation failures produce warnings; a running service alone does not prove encryption was established.

### Runtime policies

| Block | Fields and defaults |
| --- | --- |
| `responses` | `enabled: true`; current protocol bindings and conversion policies control translation |
| `usage` | Estimate missing upstream usage by default; `charsPerToken: 4`, `defaultOutputTokenEstimate: 1024`, `imageInputTokenEstimate: 300`, `fileInputTokenEstimatePerKB: 128`; `estimateWhenMissing: false` disables estimation |
| `healthCheck` | Disabled by default; `intervalSeconds: 300`, `timeoutSeconds: 10`, `failureThreshold: 3`; probes may incur upstream charges |
| `modelCatalog` | models.dev enabled by default; bundled snapshot and local cache, mirror fallback on online failure; optional `url` and `proxy`; `syncIntervalMinutes` defaults to 1440, explicit `0` disables periodic sync |
| `agentRemote` | `enabled` defaults to `true`; `publicUrl` supplies the Agent Card address behind a reverse proxy; see [remote access](remote-agent-api.en.md) |
| `outbound` | Optional HTTP proxy `proxyUrl`; omitted `deniedIpRanges` uses private, loopback, and reserved ranges; explicit `[]` permits all ranges |

Set `outbound.proxyUrl` under Runtime configuration → Security, for example `http://127.0.0.1:7890` or `http://user:password@127.0.0.1:7890`. URL-encode special characters in credentials. The field is hidden by default with a visibility toggle. The configuration file and authenticated admin API retain the original value; error logs redact proxy credentials.

The proxy covers model calls (including streams and WebSocket), model discovery, upstream health probes, the model catalog, and macOS release checks and DMG downloads. New requests use saved changes; active requests retain their connections. macOS rereads the setting for each check or download. Local panel and health/API traffic bypasses this proxy. With an empty value the backend connects directly and macOS updates retain system network defaults. Explicit proxy failures never fall back to direct connections. An existing `modelCatalog.proxy` takes precedence over the global proxy.

The explicitly configured proxy endpoint may be local or private without removing loopback deny rules. Upstream targets and redirects are still checked against `deniedIpRanges`. Proxied requests require local DNS validation; the proxy performs final DNS resolution and dialing, so use a trusted proxy with its own private-target restrictions. Local/private model servers still require an appropriate target CIDR adjustment. Remove only necessary ranges; see [config.json.example](../config.json.example) for defaults.

## Request logs

By default, logs contain metadata and usage, without request/response bodies, including for failed requests. Set `usageLog.bodyMaxKB` to a positive value to capture bodies; the WebUI initially enables it at 1024 KB. Usage detail defaults to 14 days and a 1024 MiB content budget; explicit zero disables each limit.

```json
{
  "usageLog": {
    "persistEnabled": true,
    "retentionDays": 14,
    "maxContentMB": 1024,
    "maxRecords": 0,
    "bodyMaxKB": 0,
    "bodyOnErrorOnly": false,
    "externalizeMedia": true,
    "cleanupIntervalMinutes": 60
  }
}
```

| Field | Meaning |
| --- | --- |
| `persistEnabled` | Defaults to `true`; `false` stops request-log persistence |
| `retentionDays` / `maxRecords` | Age or record-count cleanup; `0` means unlimited |
| `maxContentMB` | request-log JSON plus deduplicated media content budget; `0` is unlimited; database pages are reclaimed independently |
| `bodyMaxKB` | KB limit for each of the four request-chain bodies; `0` disables body capture |
| `bodyOnErrorOnly` | Keep bodies only for failed requests; still requires `bodyMaxKB > 0` |
| `externalizeMedia` | Deduplicate base64 media under `<logDatabasePath>-assets/` and retain placeholders in bodies |
| `cleanupIntervalMinutes` | Defaults to 60 minutes; positive values have a minimum of 5 |

Policies affect subsequent requests; old bodies are not retroactively removed. Retention cleanup preserves hourly aggregate statistics and deletes associated media. This version does not migrate or support old database schemas; use new databases.

`systemLog.retentionDays`, `systemLog.maxRecords`, and `systemLog.maxContentMB` independently limit system-log age, count, and content bytes; defaults are 30 days, unlimited count, and 256 MiB. Usage detail defaults are 14 days and 1024 MiB. Explicit zero disables the corresponding limit. A content budget is not a hard disk limit: indexes, free pages, and WAL are reported separately. Retention keeps historical usage aggregates, while Reset Usage clears them. Old databases are not migrated or supported.

Zerolog provides structured output. HTTP events and usage records share a server-generated `request_id` (`X-Elysia-Request-Id` response header). Access events use debug; 5xx responses and panics use error. The system queue is bounded to 2048 records / 16 MiB and batches up to 64 records or 100 ms. Saturated diagnostics are dropped and counted; important events use bounded synchronous fallback. The usage queue is bounded to 1024 records / 128 MiB, batches up to 64 records or 50 ms, and falls back to synchronous persistence with bounded retries. Settings and `GET /api/admin/usage/storage` expose both writers' queue, commit, drop, failure and latency statistics.

SIGTERM, SIGINT, the shutdown endpoint and parent EOF share a shutdown sequence: stop producers, give persistence a separate 15-second drain budget, then checkpoint and close databases. Persistence failures go directly to stderr, increment failure metrics, and cause nonzero exit. Queued records cannot survive SIGKILL; committed records use SQLite FULL synchronous WAL durability, subject to filesystem sync guarantees.

## Process supervision

Standalone binaries are long-running processes. Use systemd, a Windows service wrapper, or a container. This unit assumes a system user named `elysia`, a binary under `/opt/elysia-api/`, and writable configuration and data directories for that user:

```ini
[Unit]
Description=Elysia API
After=network.target

[Service]
User=elysia
WorkingDirectory=/opt/elysia-api
ExecStart=/opt/elysia-api/elysia-api-linux-amd64 --config /opt/elysia-api/config.json
Environment=ELYSIA_API_OPEN_BROWSER=false
Restart=on-failure
RestartSec=3
TimeoutStopSec=45

[Install]
WantedBy=multi-user.target
```

Save it as `/etc/systemd/system/elysia-api.service`, then run `sudo systemctl daemon-reload` and `sudo systemctl enable --now elysia-api`. Inspect startup logs with `journalctl -u elysia-api`.

## Upgrades, backup, and restore

Both databases use WAL, a 5000 ms busy timeout and foreign keys. Business data uses `synchronous=NORMAL`; logs use `synchronous=FULL`. Business schema version is 2 and log schema version is 1. Old business schemas are rejected without migration.

1. Stop the service before upgrading. Back up `config.json`, both databases and any remaining `-wal` / `-shm` files, and `<logDatabasePath>-assets/`.
2. Protect and back up the active encryption key separately. If supplied by an environment variable, preserve its secure recovery procedure. Leaking both database and key defeats encryption at rest.
3. Retain the old binary and replace the binary or image. Use databases with the current schema; upgrading from an older schema requires new database paths and fresh data initialization.
4. Check `/health`, panel sign-in, the model list, and an inference request. Real inference incurs upstream charges.
5. To restore, stop the service, restore matching configuration, database, media, and key from the same backup, restore permissions, then start a compatible version. Do not replace only the main database file or generate a new key.

For online backups, use SQLite backup tooling to obtain a consistent database and coordinate the media snapshot. Do not copy only the main file while it is being written. An unwritable data directory or full disk causes writes to fail.

## Token recovery

If the panel token is lost, stop the service, set `panelAccessToken` to a new random value, restart, and sign in again. Relay tokens reside in SQLite; change them through the panel after restoring access.

## Reloading

`POST /api/admin/reload` requires panel authentication. See the [API reference](webui-api.en.md#runtime-config) for runtime updates. Listener address, port, database path, and pprof route changes require a restart; reloading does not recreate these resources.

`POST /__reload` and `POST /__shutdown` accept only loopback callers. Do not expose them through a reverse proxy.

## Current format requirements

The backend provides no backward compatibility or automatic migration. Use a new database and current configuration. Back up existing data separately and recreate required records through current management APIs; the service never deletes old files for you.

- Listener settings use top-level `host` / `port`, not `server.host` / `server.port`.
- API keys, models and groups live only in SQLite; `tokens`, `modelGroups` and `customProtocols` are not read or imported.
- Protocols use current definitions and explicit bindings with `custom:<protocol ID>` platforms. Model references must be `sourceId:modelId`.
- Legacy protocol migration endpoints, preset rename/path repairs and historical log-layout backfills have been removed.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Connection refused | Service log, actual port; Docker must set `ELYSIA_API_HOST=0.0.0.0` |
| Panel returns 401 | Use the panel token and verify the config path; relay and Agent keys are different credentials |
| Upstream connection blocked | Outbound CIDRs, resolved IP addresses, and proxy settings |
| Logs have no bodies | A positive `bodyMaxKB` enables future capture; old requests cannot be captured retroactively |
| Secrets cannot be decrypted | Matching master key or `secretKeyPath`; changed environment variables |
| Empty model list | Upstream discovery endpoint, per-key permissions, and the custom protocol's `models` definition |
