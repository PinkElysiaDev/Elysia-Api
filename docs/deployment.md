# Elysia-API Deployment Guide

## Standalone Backend

Create a minimal `config.json` next to the backend binary:

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

Run:

```bash
elysia-api-backend --config config.json
```

Open the WebUI against `http://127.0.0.1:8765/ui/` and authenticate with `panelAccessToken`.

The WebUI is embedded in the backend binary (`//go:embed`), so `/ui/` works out of the box with **no `webuiDir` configuration required**. To override it with a custom build, set `webuiDir` in `config.json` to a directory containing the built assets.

## Optional Koishi Entry Plugin

The Koishi side should be reduced to an entry plugin only. It should:

- edit bootstrap `config.json` values such as host, port, database path, and panel access token;
- start, stop, or restart the backend process;
- open or display the WebUI URL;
- call `/api/admin/reload` where a hot reload is enough.

It must not aggregate models, write model groups, proxy requests, or maintain heartbeat logic.

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

For backup, copy the database through SQLite backup tooling or stop the backend before copying all three files.

## Access Token Reset

If the panel token is lost, stop the backend, edit `panelAccessToken` in bootstrap `config.json`, then restart the backend.

Relay API tokens are stored in SQLite and can be changed through `/api/admin/api-tokens` after panel access is restored.

## Migration Notes

Legacy configs containing `tokens` and `modelGroups` are imported into SQLite on startup as compatibility data. New installations should only keep bootstrap fields in `config.json`.

The backend no longer exits when Koishi stops because heartbeat self-shutdown has been removed. Process supervision should be handled by the OS, service manager, Docker, or the optional Koishi entry plugin.

## Memory Diagnostics

Use `GET /api/admin/health` to inspect basic Go memory metrics. The migration reduces memory growth by storing usage records in SQLite instead of keeping all records in an in-memory slice.
