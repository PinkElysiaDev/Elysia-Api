# Development

[Documentation](README.en.md) · [简体中文](development.md) · **English**

Develop, validate, and build Elysia API locally. Run commands from the repository root unless stated otherwise.

## Environment

| Tool | Requirement |
| --- | --- |
| Node.js | 22+; CI uses 22 |
| Go | 1.25+; CI uses 1.25.x |
| npm | Included with Node.js; workspace installation and builds |
| macOS packaging | A macOS host and a Swift / AppKit toolchain that links macOS 12 arm64 and x86_64 targets |

Python and vision dependencies are required only to regenerate login assets, not for ordinary frontend or release builds.

## Repository layout

```text
backend/
  agent/       # Agent engine
  config/      # Bootstrap and runtime policies
  relay/       # Protocols and upstream adapters
  server/      # HTTP routes, auth, CLI, orchestration
  storage/     # SQLite
  webui/       # Embedded frontend assets
packages/webui/ # React + Vite
scripts/       # Builds, native app, asset tooling
docs/          # Documentation
```

## Local development

Install workspace dependencies:

```bash
npm install
```

Start the backend in a separate terminal. This command creates its development config under `backend/`; obtain the initial panel token from the log:

```bash
cd backend
go run . --config ./config.json
```

In another terminal, start the frontend from the repository root:

```bash
npm run dev --workspace @root/webui
```

Open `http://127.0.0.1:5273/`. Vite proxies to `http://127.0.0.1:8765` by default; override it with `ELYSIA_DEV_PROXY`. `ELYSIA_DEV_HOST` controls the dev-server binding. See [WebUI development](../packages/webui/README.en.md) for proxy details.

Use development configuration and test data. Default outbound policies block loopback, private, and reserved ranges; adjust only relevant CIDRs for a local mock upstream.

## Validation

Run backend checks from `backend/`:

```bash
go test ./...
```

Run frontend checks from the repository root:

```bash
npm run lint --workspace @root/webui
npm run build:webui
npm exec --workspace @root/webui playwright install chromium
npm run test:e2e --workspace @root/webui
```

Browser tests mock APIs and start a frontend on `127.0.0.1:5274`. Set `PLAYWRIGHT_CHANNEL=chrome` to use an installed Chrome. Live model evaluations require explicit credentials and incur usage; they are not part of routine documentation checks.

Use `npm run test:macos-app` for native tests and `npm run test:macos-app -- --panel` for the packaged panel. See [macOS validation](macos-testing.en.md) for prerequisites.

## Release builds

```bash
npm run build
```

The build script compiles the frontend, copies assets into the backend embed directory, then cross-compiles six targets with `CGO_ENABLED=0`. Outputs go to `dist/standalone/`; each build recreates that directory.

| Platform | Outputs |
| --- | --- |
| Windows | `elysia-api-windows-amd64.exe`, `elysia-api-windows-arm64.exe` |
| Linux | `elysia-api-linux-amd64`, `elysia-api-linux-arm64` |
| macOS CLI | `elysia-api-darwin-amd64`, `elysia-api-darwin-arm64` |

macOS CLI artifacts support local use and app assembly. Releases distribute the macOS DMG. A frontend-only build writes `packages/webui/dist/`; the full build also updates the backend embed directory.

### macOS packaging

```bash
npm run build:macos-app -- --check-toolchain
npm run build
npm run build:macos-app
```

Outputs are the universal `ElysiaApi.app` and `elysia-api-macos.dmg`. Packaging validates architectures, signature, DMG integrity, and mounted contents. Resolve toolchain failures with a compatible toolchain rather than raising the deployment target or removing Intel support.

### Versions and publishing

The backend build resolves the latest Git tag and injects it into `/health` through ldflags; without a tag it uses `dev`. The native app has its own version fallback rules in the packaging script. CI builds on `v*` tags or manual dispatch; releases include SHA256 checksums.

## Documentation maintenance

- Verify source and tests, then update Chinese and the corresponding `.en.md` page with matching interfaces, arguments, defaults, and steps.
- README handles the first use; topic pages own configuration, limits, recovery, and complete references. Avoid duplicating long parameter tables.
- Include purpose, language switching, and an index link. Guides provide prerequisites, runnable examples, verification, and troubleshooting. References provide contracts, fields, examples, and limits.
- Use “model source,” “model group,” “panel token,” and “relay token” consistently. Do not translate field names, endpoint paths, or CLI identifiers.
- Label illustrative fragments with `jsonc`, `text`, or an explicit note. `json` blocks must parse. Use credential placeholders and do not commit real configuration, logs, or tokens.
- Preserve current documentation paths and referenced heading anchors. English pages link to English topic pages. Merge useful material from phase-specific design, review and acceptance records into maintained guides, then remove the old files and update references. Use Git history for historical details.

The Chinese CLI reference is generated from command definitions and help renderers. Do not edit the generated output directly. Run from `backend/`:

```bash
UPDATE_AGENT_CLI_DOCS=1 go test ./server -run '^TestCLIReferenceUpToDate$' -count=1
```

The generation template in `agent_cli_contract_test.go` controls the introduction, navigation, and section formatting. The English reference is a complete maintained translation. When commands change, update arguments, examples, and permission descriptions in both languages; run the same test without the update variable to check freshness.

## Contribution checks

Review the diff, links, code blocks, and language parity. Run checks relevant to the change and report actual results; historical test records are not current validation. Update affected documentation with behavior changes.
