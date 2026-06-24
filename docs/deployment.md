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
| macOS Intel | `elysia-api-darwin-amd64` |
| macOS Apple Silicon | `elysia-api-darwin-arm64` |

`config.json.example` stays at the repository root and is not copied into `dist/standalone/`.

## Configuration

Create `config.json` from the root `config.json.example`, place it next to the backend binary, and change at least `panelAccessToken`:

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

## Run

### Windows

Put `elysia-api-windows-amd64.exe` and `config.json` in the same directory, then run:

```powershell
.\elysia-api-windows-amd64.exe --config .\config.json
```

If `config.json` is in the same directory as the exe, double-clicking the exe also works because the default config path is `config.json` in the current working directory.

### Linux

Put `elysia-api-linux-amd64` and `config.json` in the same directory, then run:

```bash
chmod +x ./elysia-api-linux-amd64
./elysia-api-linux-amd64 --config ./config.json
```

### macOS

Apple Silicon uses `elysia-api-darwin-arm64`; Intel Macs use `elysia-api-darwin-amd64`:

```bash
chmod +x ./elysia-api-darwin-arm64
./elysia-api-darwin-arm64 --config ./config.json
```

Open the WebUI at `http://127.0.0.1:8765/ui/` and authenticate with `panelAccessToken`.

The WebUI is embedded in the backend binary, so `/ui/` works without a separate frontend deployment. To override it with a custom build, set `webuiDir` in `config.json` to a directory containing the built assets.

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
