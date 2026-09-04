<h1 align="center">Elysia-API</h1>

<p align="center">
  <a href="https://github.com/PinkElysiaDev/Elysia-Api/tags"><img src="https://img.shields.io/github/v/tag/PinkElysiaDev/Elysia-Api?sort=semver&amp;style=flat-square&amp;label=tag" alt="Latest tag"></a>
  <a href="https://github.com/PinkElysiaDev/Elysia-Api/stargazers"><img src="https://img.shields.io/github/stars/PinkElysiaDev/Elysia-Api?style=flat-square&amp;logo=github" alt="GitHub stars"></a>
  <a href="./backend/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/PinkElysiaDev/Elysia-Api?filename=backend%2Fgo.mod&amp;style=flat-square" alt="Go version"></a>
</p>

<p align="center">
  English · <a href="./README.zh-CN.md">简体中文</a>
</p>

Elysia-API is a self-hosted multi-upstream AI model gateway. It centralizes request authentication, model-group routing, load balancing, OpenAI / Claude / Gemini format conversion, streaming forwarding, Usage statistics, and WebUI management in a single Go backend.

The WebUI is embedded into the backend binary through `//go:embed` and served at `/ui/` by default. Runtime configuration uses a bootstrap `config.json`; model sources, model groups, Relay API Tokens, Usage data, and system logs are stored in SQLite.

## Project Structure

```text
elysia-api/
├── backend/                # Go backend (the gateway itself)
│   ├── config/             # Configuration loading / hot reload / encryption key
│   ├── relay/              # Upstream forwarding / format conversion / Maheshvara core protocol
│   ├── server/             # HTTP routes / authentication middleware / admin API / Usage records
│   ├── storage/            # SQLite persistence
│   └── webui/              # Embedded WebUI static assets (//go:embed all:dist)
├── packages/webui/         # React + Vite console source
├── docs/                   # Deployment, WebUI API, data model, and other documentation
├── scripts/                # Standalone backend release build scripts
└── config.json.example     # Minimal bootstrap configuration template
```

## Features

- Model groups and load balancing: supports round-robin, sequential, random, and model-group-level permission strategies.
- Multi-format conversion: uses Maheshvara Request / Response / Usage as the single core representation, converting between OpenAI Chat Completions, OpenAI Responses, Claude Messages, and Gemini GenerateContent.
- Responses API: `/v1/responses` can be forwarded natively or converted to Chat / Claude / Gemini upstreams.
- Streaming responses: all four built-in protocols and custom protocols are converted to SSE through stateful Maheshvara decoders / renderers.
- Same-protocol passthrough: when the client and upstream route use the same protocol among Chat Completions, Responses, Claude Messages, and Gemini GenerateContent, requests automatically pass through with zero conversion; only the model name is rewritten and all other fields are preserved.
- Usage statistics: records cache hits, reasoning tokens, multimodal tokens, built-in tool calls, request / response summaries, and retry events.
- Traffic limits: supports model-group-level concurrency and daily request/token limits.
- Security hardening: encrypts sensitive fields at rest, protects against SSRF, and compares tokens in constant time.
- Operations and diagnostics: includes health checks, system logs, pprof, a WebUI usage dashboard, and hot-reload endpoints.
- Multi-key scheduling: a model source can configure multiple API keys and schedule them by round-robin / random / priority; **per-key permission auto-discovery** independently requests the model list with each key, automatically obtains each key's available model set (which can be enabled or disabled per key in the panel), and ensures scheduling never switches to a key without permission.
- Model capability catalog: includes a models.dev snapshot for zero-configuration use, periodically updates it online, and persists a cache (automatically falling back to a jsDelivr mirror when models.dev is unavailable); model fetching fills in capabilities such as vision / tools / structured output / reasoning mode / context length. Model-group capabilities are derived from members and enforced (groups without vision automatically remove images; groups without tool support reject tool requests).
- Asynchronous background fetching: model fetching runs as a background task, returns immediately without blocking the page, and exposes progress and results through real-time polling; sources being fetched automatically lock related operations to prevent conflicts.
- Custom fetch URL: sites whose model-list endpoint differs from the request-forwarding endpoint by domain / port / protocol can configure a separate fetch URL.
- Model-level management: fetched models support individual editing / enabling / disabling / deletion and search; refresh uses a preserving merge, so manually added models and user edits are never lost.

## Quick Start

Prebuilt binaries are published through [GitHub Releases](https://github.com/PinkElysiaDev/Elysia-Api/releases/latest). Download the program for your platform and `SHA256SUMS`; to rebuild from source, see the [Build](#build) section below.

| Platform | Release file |
| --- | --- |
| Windows amd64 | `elysia-api-windows-amd64.exe` |
| Linux amd64 | `elysia-api-linux-amd64` |
| macOS app (universal Intel / Apple Silicon) | `elysia-api-macos.dmg` |

### General Configuration

On first startup, if there is no `config.json` beside the binary, the backend automatically writes a default configuration. Its `panelAccessToken` is generated randomly with `crypto/rand` (not a `change-me` placeholder), and the generated token and configuration path are printed in the startup log—rotate the token promptly after using it to sign in to the panel. No manual file creation is required; the template below is for reference only.

Create the runtime configuration from `config.json.example` in the repository root and change at least `panelAccessToken`:

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

When `databasePath` and `secretKeyPath` use relative paths, they are resolved relative to the directory containing `config.json`.

### Windows

Place `elysia-api-windows-amd64.exe` and `config.json` in the same directory, then run:

```powershell
.\elysia-api-windows-amd64.exe --config .\config.json
```

If `config.json` is in the same directory as the executable, you can also double-click the executable. Without `--config`, the program reads `config.json` from the current directory. If the file does not exist on first startup, it is created automatically with a random `panelAccessToken` (see the startup log).

### Linux

Place `elysia-api-linux-amd64` and `config.json` in the same directory, then run:

```bash
chmod +x ./elysia-api-linux-amd64
./elysia-api-linux-amd64 --config ./config.json
```

### macOS

Download `elysia-api-macos.dmg` from Releases, double-click it, and drag `ElysiaApi` to the `Applications` shortcut to install. Then launch it from Launchpad:

- The first launch automatically generates the configuration and a random `panelAccessToken`; all data is stored in `~/Library/Application Support/ElysiaApi/` (`config.json`, SQLite, `.master-key`, and `elysia-api.log`).
- In-app updates are supported: when a new version is available, an **Update now** button appears in the lower-left corner and completes the download (DMG), SHA-256 verification, extraction, and replacement in one step. You can also trigger it manually through **Check for Updates…** in the menu bar. Updates replace only the embedded backend; configuration and data are unaffected.
- The default port is `8765`. If it is occupied, the app automatically uses an available port starting at `8799` (the actual port appears in the menu bar status row and panel address).

> CI artifacts use ad-hoc signing. If Gatekeeper blocks the first launch, run
> `xattr -d com.apple.quarantine /Applications/ElysiaApi.app` and open it again.

### Docker

Build the image from the repository root first:

```bash
docker build -t elysia-api:local .
```

Minimal run command:

```bash
docker run -d \
  -p 8765:8765 \
  -v elysia-data:/data \
  -e ELYSIA_API_HOST=0.0.0.0 \
  elysia-api:local
```

On first startup, if the configuration file does not exist, the backend generates the configuration and a random `panelAccessToken` in the data volume. Visit `http://127.0.0.1:8765/ui/` and sign in with the token from the startup log.

For public access, configure `ELYSIA_API_HOST=0.0.0.0`; the default is `127.0.0.1`.

To provide the database master key through an environment variable, add `-e ELYSIA_API_MASTER_KEY=...` to the run command.

Recommended Compose configuration:

```yaml
services:
  elysia-api:
    image: elysia-api:local
    container_name: elysia-api
    restart: unless-stopped # start automatically
    init: true
    read_only: true
    tmpfs:
      - /tmp:size=64m,mode=1777
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    ports:
      - "${ELYSIA_HTTP_PORT:-8765}:8765"
    environment:
      ELYSIA_API_HOST: 0.0.0.0
      # Uncomment when using an external database master key, and inject it through a secure environment-management method:
      # ELYSIA_API_MASTER_KEY: your-master-key
    volumes:
      - elysia-data:/data

volumes:
  elysia-data:
```

### WebUI Initialization

Open the WebUI after starting the backend:

```text
http://127.0.0.1:8765/ui/
```

Sign in with `panelAccessToken`, then add model sources, fetch models, create model groups, and create a Relay API Token in the WebUI. You can then call a model group through the OpenAI-compatible endpoint:

```bash
curl http://127.0.0.1:8765/v1/chat/completions \
  -H "Authorization: Bearer <your-relay-api-token>" \
  -H "Content-Type: application/json" \
  -d '{"model":"default","messages":[{"role":"user","content":"hi"}]}'
```

## Build

Install dependencies before the first build or after dependency changes:

```bash
npm install
```

> If `proxy.golang.org` is inaccessible, set the Go module proxy before building: `export GOPROXY=https://goproxy.cn,direct`

Build the WebUI, synchronize embedded assets, and cross-compile standalone binaries for all platforms independently of the build host:

```bash
npm run build
```

Local build artifacts are placed in `dist/standalone/`. This directory is not committed to Git. Formal releases are distributed through GitHub Releases and contain only these three artifacts:

| Platform | Artifact |
| --- | --- |
| Windows amd64 | `elysia-api-windows-amd64.exe` |
| Linux amd64 | `elysia-api-linux-amd64` |
| macOS (universal Intel / Apple Silicon) | `elysia-api-macos.dmg` |

> DMG assembly is available only on macOS (requires swiftc / lipo / codesign / hdiutil) and is produced by CI during release: pushing a `v*` tag publishes automatically, or you can trigger `workflow_dispatch` manually from the Actions page and then download the artifacts. The two bare darwin binaries are only inputs to DMG assembly; command-line scenarios can still use them directly.

On macOS (with Xcode Command Line Tools), you can assemble `ElysiaApi.app` and the DMG separately from the darwin binary produced by `npm run build`:

```bash
npm run build:macos-app
```

Develop the WebUI:

```bash
cd packages/webui
npm run dev
```

The Vite dev server proxies to `http://127.0.0.1:8765` by default.

## Configuration

`config.json` stores only the bootstrap fields required for startup. Model sources, model groups, Relay API Tokens, Usage data, and system logs are stored in SQLite.

| Field | Description |
| --- | --- |
| `host` | Backend listen address. Use `127.0.0.1` for local-only access. |
| `port` | Backend listen port; the default example is `8765`. |
| `panelAccessToken` | Access token for the WebUI and `/api/admin/*` administrative API. |
| `databasePath` | SQLite database path. Relative paths are resolved relative to the directory containing `config.json`. |
| `logLevel` | Log level, commonly `info` or `debug`. |
| `httpTimeout` | Upstream HTTP timeout in seconds; `0` means unlimited. |
| `secretKeyPath` | Path to the master key file used to encrypt sensitive SQLite fields. Relative paths are resolved relative to the directory containing `config.json`. |
| `webuiDir` | Optional. Leave empty to use the embedded WebUI; set it to override with an external static asset directory. |
| `enablePprof` | Optional. Enables pprof endpoints protected by the panel token. |
| `maxBodyBytes` | Optional. Request body size limit. |

### Maheshvara and Custom Protocols

Cross-protocol conversion uniformly passes through the Maheshvara core request / response model: OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and Gemini GenerateContent are all first parsed into Maheshvara and then rendered for the upstream protocol. A model source's `platform` can be `custom:<protocolID>` (the WebUI lets you select and enter the ID directly), with safe JSON body templates declared in `customProtocols` in the bootstrap `config.json`. Custom protocol sources use a manually maintained model list and do not perform automatic model discovery. For example:

```json
{
  "customProtocols": [
    {
      "id": "vendor-json",
      "request": {
        "method": "POST",
        "path": "/v2/generate/{{maheshvara.model}}",
        "headers": {"X-Model": "{{maheshvara.model}}"},
        "bodyTemplate": "{\"model\":{{maheshvara.model | json}},\"messages\":{{maheshvara.messages}},\"temperature\":{{maheshvara.temperature | default:0.2}}}",
        "omitIfEmpty": ["temperature"]
      },
      "response": {
        "textPath": "answer.text",
        "usagePath": "usage",
        "finishReasonPath": "finish"
      }
    }
  ]
}
```

Templates can read only `maheshvara.*` (and are also compatible with `request.*`), and support string interpolation, native JSON values, `json`, `default:`, and `omitIfEmpty`; they do not execute arbitrary code. Common fields include `model`, `instructions`, `messages`, `tools`, `tool_choice`, generation parameters, `reasoning`, `metadata`, `stream`, and `raw_extra`. Both non-streaming responses and SSE / NDJSON streams can be mapped back to Maheshvara and rendered as any of the four protocols requested by the client.

See [`docs/maheshvara-protocol.md`](docs/maheshvara-protocol.md) for the complete field model, four-protocol mapping matrix, reasoning safety conventions, Gemini Part invariants, and custom protocol configuration details.

You can also provide the master key through the `ELYSIA_API_MASTER_KEY` environment variable. In production, if the database directory will be backed up or packaged as a whole, place `secretKeyPath` in a separately protected location or inject it through the environment variable.

## Operations

- `GET /health`: public health check endpoint.
- `GET /api/admin/health`: administrative health check endpoint; requires the panel token.
- `POST /api/admin/reload`: administrative hot-reload endpoint; requires the panel token.
- `POST /__reload`: local loopback hot-reload endpoint.
- `POST /__shutdown`: local loopback graceful-shutdown endpoint.

Changing `host`, `port`, `databasePath`, or `enablePprof` usually requires a restart. In production, use systemd, Windows Service Manager, supervisord, Docker, or another process manager to supervise the backend.

## Data Backup

The SQLite database uses WAL mode. You will normally see:

- `elysia-api.sqlite3`
- `elysia-api.sqlite3-wal`
- `elysia-api.sqlite3-shm`

For backups, use the SQLite backup tool, or stop the backend before copying all of the database files above together. If the key file is enabled, also back up and protect the `.master-key` referenced by `secretKeyPath`. If the master key is lost, upstream API keys and Relay API Tokens encrypted in SQLite cannot be decrypted.

## HTTP Endpoints

| Endpoint | Description | Authentication |
| --- | --- | --- |
| `POST /v1/chat/completions` | OpenAI Chat Completions entrypoint | Relay API Token |
| `POST /v1/responses` | OpenAI Responses API entrypoint | Relay API Token |
| `POST /v1/messages` | Native Claude Messages entrypoint | Relay API Token |
| `POST /v1/messages/count_tokens` | Claude-compatible token counting | Relay API Token |
| `GET /v1/models` | List available model groups | Relay API Token |
| `GET /v1beta/models` / `POST /v1beta/models/*` | Gemini-compatible entrypoints | Relay API Token |
| `GET /ui/` | WebUI console | Page login |
| `GET /api/admin/*` | Administrative API | Panel Token |
| `GET /debug/pprof/*` | pprof profiling (must be enabled) | Panel Token |
| `GET /health` | Health check | None |

Request authentication supports `Authorization: Bearer <token>`, `x-api-key`, `x-goog-api-key`, and `?key=`. Panel-token scenarios also support the `panel_access_token` cookie.

## Legacy Configuration Migration

`tokens` and `modelGroups` in legacy configuration are imported into SQLite as compatibility data at startup. New installations should keep only bootstrap fields in `config.json`, such as host, port, database path, panel access token, logging, and diagnostic settings.
