# Elysia-API Deployment Guide

## Build

Install dependencies first when building from a fresh checkout or after dependency changes:

```bash
yarn install
```

Build the embedded WebUI and standalone backend binaries:

```bash
yarn build
```

The standalone binaries are emitted to `dist/standalone/`:

| Platform | File |
| --- | --- |
| Windows amd64 | `elysia-api-windows-amd64.exe` |
| Linux amd64 | `elysia-api-linux-amd64` |
| macOS Intel (local builds only) | `elysia-api-darwin-amd64` |
| macOS Apple Silicon (local builds only) | `elysia-api-darwin-arm64` |
| macOS app image (universal) | `elysia-api-macos.dmg` |

`config.json.example` stays at the repository root and is not copied into `dist/standalone/`.

On a macOS host with Xcode Command Line Tools installed, `npm run build:macos-app` additionally assembles `ElysiaApi.app` (universal arm64 + amd64) into `dist/standalone/` from the binaries produced by `npm run build`, and packs it into `elysia-api-macos.dmg`.

Releases only ship the DMG for macOS; the standalone darwin binaries above are intended for local builds and command-line use.

## Configuration

If `config.json` does not exist next to the binary on first launch, the backend automatically writes a default one with a **randomly generated `panelAccessToken`** (not the `change-me` placeholder) to the default config path and continues starting up. The generated token and the config path are printed once to the startup log — copy that token to log in, then rotate it from the panel. You do not need to create the file by hand; the manual template below is only for reference.

To create `config.json` manually instead, copy it from the root `config.json.example`, place it next to the backend binary, and change at least `panelAccessToken`:

```json
{
  "host": "127.0.0.1",
  "port": 8765,
  "panelAccessToken": "change-me",
  "databasePath": "elysia-api.sqlite3",
  "logLevel": "info",
  "httpTimeout": 120,
  "secretKeyPath": ".master-key"
}
```

Relative `databasePath`, `secretKeyPath`, and `webuiDir` values are resolved from the directory containing `config.json`.

## Request Log Management

The `usageLog` block controls how request logs (usage records and their four captured bodies) are retained. **Automatic cleanup is disabled by default** — logs accumulate exactly like older versions until you opt in. All values can also be changed at runtime from the WebUI settings page (`运行配置` → `日志管理`).

```json
{
  "usageLog": {
    "persistEnabled": true,
    "retentionDays": 0,
    "maxStorageMB": 0,
    "maxRecords": 0,
    "bodyMaxKB": 1024,
    "bodyOnErrorOnly": false,
    "externalizeMedia": true,
    "cleanupIntervalMinutes": 60
  }
}
```

| Field | Default | Meaning |
| --- | --- | --- |
| `persistEnabled` | `true` | Master switch for persisting request logs. `false` stops recording entirely. |
| `retentionDays` | `0` (off) | Auto-delete records older than N days. |
| `maxStorageMB` | `0` (off) | Cap on SQLite logical size; oldest records are deleted when exceeded (a rate-limited `VACUUM` reclaims disk space afterwards). |
| `maxRecords` | `0` (off) | Keep at most N records, deleting the oldest beyond the cap. |
| `bodyMaxKB` | `1024` | Per-body capture cap for each of the four logged bodies. `0` saves no bodies at all (metadata only). |
| `bodyOnErrorOnly` | `false` | When enabled, only failed requests keep their bodies; successful requests store metadata only. |
| `externalizeMedia` | `true` | Base64 media (images / audio / video / files) inside logged bodies are written as separate files under `<db dir>/usage-assets/<requestId>/`, and the body keeps a `__ELYSIA_ASSET__:<requestId>/<hash>.<ext>` placeholder instead. |
| `cleanupIntervalMinutes` | `60` | How often the background cleanup pass runs (minimum 5). |

Notes:

- Cleanup only deletes raw `usage_records` rows; hourly rollups (aggregate statistics) are untouched, so historical usage reports survive log cleanup.
- Externalized assets are served through the admin-authenticated endpoint `GET /api/admin/usage/assets/:requestId/:file` and are removed together with their records (on cleanup or `POST /api/admin/usage/reset`).
- The legacy flat keys `usagePersistEnabled` / `usagePersistMaxRecords` still work: they are honored when the `usageLog` block does not configure the corresponding field.

## Run

### Windows

Put `elysia-api-windows-amd64.exe` in a directory and run it:

```powershell
.\elysia-api-windows-amd64.exe --config .\config.json
```

If `config.json` is in the same directory as the exe, double-clicking the exe also works because the default config path is `config.json` in the current working directory. When `config.json` is absent on first launch, a default with a random `panelAccessToken` is created automatically — check the startup log for the generated token.

### Linux

Put `elysia-api-linux-amd64` in a directory and run it:

```bash
chmod +x ./elysia-api-linux-amd64
./elysia-api-linux-amd64 --config ./config.json
```

### macOS

For command-line use, build from source (`npm run build`) and run the matching binary — `elysia-api-darwin-arm64` on Apple Silicon, `elysia-api-darwin-amd64` on Intel:

```bash
chmod +x ./elysia-api-darwin-arm64
./elysia-api-darwin-arm64 --config ./config.json
```

Open the WebUI at `http://127.0.0.1:8765/ui/` and authenticate with `panelAccessToken`.

The WebUI is embedded in the backend binary, so `/ui/` works without a separate frontend deployment. To override it with a custom build, set `webuiDir` in `config.json` to a directory containing the built assets.

### macOS app bundle

`elysia-api-macos.dmg` (attached to each release) contains `ElysiaApi.app`, a native wrapper around the same universal backend binary (requires macOS 12 or newer, matching the Go-built backend). Open the DMG and drag ElysiaApi onto the Applications shortcut, then launch it from the Applications folder or Launchpad:

- The app runs the backend as a child process and shows the WebUI in its own window; the panel token from the generated config is injected automatically, so no manual login is needed.
- First launch writes a config with a randomly generated `panelAccessToken`. All runtime data lives in `~/Library/Application Support/ElysiaApi/` (`config.json`, SQLite database, `.master-key`, `elysia-api.log`) and survives app updates.
- A menu bar item shows the running state, port and version, with shortcuts to copy the API base URL / panel token and to start/stop the backend.
- The default port is `8765`; if it is occupied the app automatically picks a free port starting from `8799`. The actual port is shown in the menu bar status line.
- Closing the window keeps the backend running in the background (Dock icon hidden, menu bar item only); reopen the window from the menu bar. Quitting from the menu bar stops the backend.
- Updates: an update banner appears in the bottom-left corner of the window when a newer release is published. Clicking **Update now** downloads the new DMG, verifies its SHA-256 digest (refused if the release publishes no digest), swaps in the entire new app bundle exactly as signed by CI, and relaunches — config and database are untouched. Also available from the menu bar item (**Check for Updates**).

The bundle is ad-hoc signed by CI. If Gatekeeper blocks a freshly downloaded copy, run `xattr -d com.apple.quarantine /Applications/ElysiaApi.app` and open it again.

## Process Supervision

The backend is a normal long-running process. In production, run it under a platform service manager such as systemd, Windows Service wrappers, supervisord, Docker, or another supervisor.

Minimal systemd example:

```ini
[Unit]
Description=Elysia-API Backend
After=network.target

[Service]
WorkingDirectory=/opt/elysia-api
ExecStart=/opt/elysia-api/elysia-api-linux-amd64 --config /opt/elysia-api/config.json
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
```

## SQLite and WAL

The backend stores runtime data in SQLite at `databasePath`. On startup it applies:

- `PRAGMA journal_mode=WAL`
- `PRAGMA busy_timeout=5000`
- `PRAGMA foreign_keys=ON`
- `PRAGMA synchronous=NORMAL`

Expect these files beside the configured database:

- `elysia-api.sqlite3`
- `elysia-api.sqlite3-wal`
- `elysia-api.sqlite3-shm`

For backup, use SQLite backup tooling or stop the backend before copying all three files. If `secretKeyPath` points to a file, back it up separately and protect it.

## Access Token Reset

If the panel token is lost, stop the backend, edit `panelAccessToken` in bootstrap `config.json`, then restart the backend.

Relay API tokens are stored in SQLite and can be changed through `/api/admin/api-tokens` after panel access is restored.

## Runtime Changes

`POST /api/admin/reload` reloads hot-reloadable bootstrap fields. Changes to `host`, `port`, `databasePath`, or `enablePprof` normally require a process restart.

Local-only management endpoints are also available:

- `POST /__reload`
- `POST /__shutdown`

Both endpoints are restricted to loopback callers.

## Migration Notes

Legacy configs containing `tokens` and `modelGroups` are imported into SQLite on startup as compatibility data. New installations should only keep bootstrap fields in `config.json`.

The optional `customProtocols` bootstrap field registers Maheshvara-based
provider protocols. Set a model source's `platform` to `custom:<protocol-id>`
to use one. Custom request bodies are constrained JSON templates; they can map
`maheshvara.model`, `maheshvara.messages`, `maheshvara.tools`, generation
parameters, metadata, and `raw_extra`, while response paths map text, tool
calls, usage, finish reason, and errors back into the core response model.

See `docs/maheshvara-protocol.md` for the complete Maheshvara v1 field model,
four-protocol mapping matrix, streaming contract, security rules, and a full
custom protocol example.
