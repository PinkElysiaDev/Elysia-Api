<div align="center">
  <p><img src="docs/assets/elysia-banner.webp" width="100%" alt="Elysia API"></p>
  <p><strong>A lightweight, self-hosted AI gateway.</strong><br>
    Model access, protocol conversion, and AI-assisted operations.</p>
  <p>
    <a href="https://github.com/PinkElysiaDev/Elysia-Api/releases/latest"><img src="https://img.shields.io/github/v/release/PinkElysiaDev/Elysia-Api?style=flat-square&logo=git&logoColor=white&label=release&labelColor=20222b&color=e26d85" alt="Latest release"></a>
    <a href="https://github.com/PinkElysiaDev/Elysia-Api/stargazers"><img src="https://img.shields.io/github/stars/PinkElysiaDev/Elysia-Api?style=flat-square&logo=github&logoColor=white&label=stars&labelColor=20222b&color=e26d85" alt="GitHub stars"></a>
    <a href="backend/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/PinkElysiaDev/Elysia-Api?filename=backend%2Fgo.mod&style=flat-square&logo=go&logoColor=white&label=Go&labelColor=20222b&color=e26d85" alt="Go version"></a>
    <a href="#license"><img src="https://img.shields.io/github/license/PinkElysiaDev/Elysia-Api?style=flat-square&labelColor=20222b&color=e26d85" alt="License"></a>
  </p>
  <p><a href="#quick-start"><strong>Quick start</strong></a> · <a href="#ui-preview">UI preview</a> · <a href="docs/README.en.md">Documentation</a> · <a href="CHANGELOG.md">Changelog</a><br>
    <a href="README.md">简体中文</a> · <strong>English</strong></p>
</div>

## Features

- Protocols: Chat Completions API / Responses API, Anthropic API, and Gemini API; streaming conversion and same-protocol passthrough
- Routing: model groups, load balancing, multiple API keys, concurrency and quota limits, per-key model discovery
- Protocol extensions: visual field mappings for upstream APIs; saved definitions take effect without source changes
- AI operations: protocol onboarding, usage analysis, and troubleshooting; writes follow session approval policies
- Management: embedded WebUI and remote REST / MCP / A2A access; CLI commands run through Agent tools
- Security: sensitive fields encrypted when the master key loads successfully, outbound IP policies, usage records, and request logs

See the [documentation index](docs/README.en.md) for capabilities and implementation details.

## UI preview

| Overview | Protocol designer |
| :---: | :---: |
| ![Overview](docs/assets/webui-overview.webp) | ![Protocol mappings](docs/assets/webui-protocol-mapping.webp) |
| AI assistant | Runtime configuration |
| ![AI assistant](docs/assets/webui-agent.webp) | ![Runtime configuration](docs/assets/webui-runtime.webp) |

[Login animation](packages/webui/public/assets/elysia-login.mp4)

## Quick start

### 1. Download

Download a binary for your platform and the SHA256 checksum file from [GitHub Releases](https://github.com/PinkElysiaDev/Elysia-Api/releases/latest).

<details>
<summary>Release files</summary>

| Platform | File |
| --- | --- |
| Windows amd64/arm64 | `elysia-api-windows-{arch}.exe` |
| Linux amd64/arm64 | `elysia-api-linux-{arch}` |
| macOS universal | `elysia-api-macos.dmg` |

</details>

### 2. Start

The first launch creates a configuration file and SQLite database. On Windows / Linux, the startup log prints a random panel token. The default address is `127.0.0.1:8765`. Change the configured port if it is occupied; the macOS app selects an available port automatically.

<details>
<summary><strong>Windows</strong></summary>

Double-click the executable for your architecture, or run:

```powershell
.\elysia-api-windows-amd64.exe
```

On ARM64, use `elysia-api-windows-arm64.exe`. The configuration is created in the current working directory.

</details>

<details>
<summary><strong>Linux</strong></summary>

```bash
chmod +x ./elysia-api-linux-amd64
./elysia-api-linux-amd64
```

On ARM64, replace both filenames with `elysia-api-linux-arm64`.

</details>

<details>
<summary><strong>macOS</strong></summary>

1. Mount the DMG and drag ElysiaApi into Applications. Requires macOS 12+; supports Intel and Apple Silicon.
2. Copy the panel token from the menu bar. Data is stored in `~/Library/Application Support/ElysiaApi/`.
3. If Gatekeeper blocks the downloaded app, run this command and try again:

   ```bash
   xattr -d com.apple.quarantine /Applications/ElysiaApi.app
   ```

</details>

<details>
<summary><strong>Docker</strong></summary>

```bash
# Build from the repository root
docker build -t elysia-api:local .
# Start the service
docker run -d --name elysia-api \
  -p 127.0.0.1:8765:8765 \
  -v elysia-data:/data \
  -e ELYSIA_API_HOST=0.0.0.0 \
  elysia-api:local
```

Run `docker logs elysia-api` to obtain the initial panel token. See [deployment](docs/deployment.en.md) for a Compose example and production settings.

</details>

### 3. Connect a model

1. Open `http://127.0.0.1:8765/ui/` and sign in with the panel token. For the macOS App, use the menu-bar address and its port in the call below.
2. Add a model source, enter its upstream URL and API key, and fetch its model list.
3. Create a model group named `default` and add an available model.
4. Create a Relay API Token with access to that group, then test it:

   ```bash
   curl http://127.0.0.1:8765/v1/chat/completions \
     -H "Authorization: Bearer <RELAY_TOKEN>" \
     -H "Content-Type: application/json" \
     -d '{"model":"default","messages":[{"role":"user","content":"hi"}]}'
   ```

   The reply is in `choices[0].message.content`. For 401, check the relay token; if no model is available, check the source, group members and enabled states.

   The panel token is for management, relay tokens are for `/v1` and `/v1beta`, and keys with the `agent` scope are for remote operations. See [authentication](docs/remote-agent-api.en.md#authentication).

## Configuration

`config.json` stores listener settings, database paths, and runtime policies. Model sources, model groups, API tokens, and usage records are stored in SQLite.

The file is normally created automatically. This minimal manual configuration omits `httpTimeout`, which means unlimited; automatically generated configuration sets 120 seconds. Replace `change-me` with a random panel token:

```json
{
  "host": "127.0.0.1",
  "port": 8765,
  "panelAccessToken": "change-me",
  "databasePath": "elysia-api.sqlite3"
}
```

See [deployment](docs/deployment.en.md) for configuration fields, environment variables, migration, backup, restore, and security settings.

## Further reading

- [Custom protocols](docs/protocol-definition-reference.en.md)
- [Remote AI agent access](docs/remote-agent-api.en.md)
- [Management API](docs/webui-api.en.md)
- [CLI reference](docs/agent-cli.en.md)

## Build

Requirements: Node.js 22+, Go 1.25+.

```bash
npm install && npm run build
```

Outputs are written to `dist/standalone/`. See [development](docs/development.en.md) for cross-compilation and DMG packaging.

## Contributing

Include your environment, reproduction steps, and redacted logs in issues. Describe changes and validation in pull requests; update documentation when behavior changes.

## License

License information will be provided by the project owner.
