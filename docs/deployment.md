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

The optional `customProtocols` bootstrap field registers Maheshvara-based
provider protocols. Set a model source's `platform` to `custom:<protocol-id>`
to use one. Custom request bodies are constrained JSON templates; they can map
`maheshvara.model`, `maheshvara.messages`, `maheshvara.tools`, generation
parameters, metadata, and `raw_extra`, while response paths map text, tool
calls, usage, finish reason, and errors back into the core response model.

See `docs/maheshvara-protocol.md` for the complete Maheshvara v1 field model,
four-protocol mapping matrix, streaming contract, security rules, and a full
custom protocol example.
