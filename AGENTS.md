# AGENTS.md

This file provides guidance to coding agents working with code in this repository.

## Commands

Requirements: Node.js 22+, npm workspaces, Go 1.27.2+ (see `backend/go.mod`). Run commands from the repository root unless noted. There is no root `npm test` or root backend Go module.

```sh
npm ci
# Use a mirror when direct Go downloads are unavailable; scoped to this command:
GOPROXY=https://goproxy.cn,direct go -C backend mod download

# Backend development; creates config/data under backend/ on first launch.
# Obtain the initial panel token from the startup output/configuration.
(cd backend && go run . --config ./config.json)
# Frontend development, in a separate terminal:
npm run dev --workspace @root/webui
```

Backend defaults to `127.0.0.1:8765`; Vite defaults to `127.0.0.1:5273`. Set `ELYSIA_DEV_PROXY=http://127.0.0.1:<port>` to change Vite's backend target, `ELYSIA_DEV_HOST` to change its binding, and `ELYSIA_API_OPEN_BROWSER=false` to suppress automatic browser opening. Verify the server with `curl -fsS http://127.0.0.1:8765/health`. Configuration paths are relative to the current working directory; configured relative data paths resolve against the configuration directory.

```sh
# Frontend checks
npm run lint --workspace @root/webui
npm run build:webui
npm exec --workspace @root/webui -- tsc --noEmit

# Backend checks
(cd backend && go test ./...)
(cd backend && go vet ./...)
(cd backend && go test ./internal/architecture)
# One test (or use a subtest suffix in -run):
(cd backend && go test ./server -run '^TestHealthProbeRequiresSuccessfulContract$' -count=1)
# Race/order-sensitive regression; see backend/TESTING.md for targeted groups:
(cd backend && go test -race -shuffle=on -count=1 -timeout=30m ./...)

# Bounded protocol checks; default is conversion, NOT the complete Go suite:
npm run test:protocol
npm run test:protocol -- --suite=gemini-stream
npm run test:protocol -- --suite=output
npm run test:protocol -- --suite=all --jobs=2
npm run test:protocol -- --suite=conversion --race
node --test scripts/verification/backend.test.mjs scripts/verification/runner.test.mjs

# Browser tests: install the engines for the selected projects.
npm exec --workspace @root/webui -- playwright install chromium webkit
npm run test:e2e --workspace @root/webui
# One file/test on Chromium only:
npm run test:e2e --workspace @root/webui -- navigation-and-filters.spec.ts --project=chromium
npm run test:e2e --workspace @root/webui -- --project=chromium -g 'test title'
# Protocol authoring tests against an isolated real backend and Vite instance:
node scripts/test-protocol-e2e.mjs

# Releases: frontend + embedded assets/source snapshot + six cross-compiled binaries
npm run build
# macOS packaging requires macOS and the compatible Swift/AppKit toolchain:
npm run build:macos-app -- --check-toolchain
npm run build:macos-app
npm run test:macos-app
```

Ordinary Playwright tests mock management APIs and start Vite on port 5274. Projects include Chromium, WebKit, and a serial Chromium cinematic-login project. Use `--project=chromium` if only Chromium is installed. `PLAYWRIGHT_CHANNEL=chrome` selects installed Chrome; `PLAYWRIGHT_PORT` changes the test port. The protocol E2E runner separately builds/starts its own backend and frontend, supplies test credentials, and retains isolated data under the system temporary directory. Live-provider verification is separate and requires credentials/billable upstream usage.

`npm run build:webui` verifies/prepares login trace assets, typechecks, and writes `packages/webui/dist`; it does not update embedded Go assets. `npm run build` recreates `dist/standalone`, copies frontend assets into `backend/webui/dist`, refreshes the generated `backend/server/_snapshot`, and builds Windows/Linux/macOS amd64/arm64 binaries with `CGO_ENABLED=0`. A plain `go build` from a clean checkout has only an embed placeholder, not a built WebUI. No special JSON build tags are required.

## Architecture

Elysia API is a self-hosted AI gateway with a Go/Hertz backend and a React 18/TypeScript/Vite management frontend. It routes model-group requests to sources, converts protocols through a shared semantic representation, and exposes permission-controlled Agent operations through WebUI, REST, MCP, A2A, and an application command language.

### Backend ownership

- `backend/main.go` owns process flags, explicit configuration bootstrap, signals, and exit status. `server/bootstrap.go` composes services; `server/lifecycle.go` owns startup, draining, and ordered shutdown. Importing configuration must not create files.
- `server` is the application coordinator: HTTP/authentication, CLI definitions, gateway orchestration, protocol administration, Agent adapters, and usage accounting. It remains a substantial application layer, not merely HTTP handlers.
- `internal/routing` owns immutable route snapshots, scheduling, cursors, quotas, concurrency reservations, and invalidation. Successful mutations invalidate the routing generation. Reservation release must remain idempotent and isolated across day changes/group recreation.
- `protocol` owns compilation, semantic documents, mappings, verification, and protocol lifecycle contracts. `relay` executes HTTP/WebSocket transport with shared outbound policies, nonstream deadlines, and independent stream-idle timeouts; it is not a second protocol interpreter.
- `agent` is host-independent and receives model callers, persistence, tools, and prompt/render dependencies through interfaces. `server` supplies their concrete implementations and approvals. Sessions allow one active turn; browser disconnect is not turn cancellation.
- `storage` owns SQL and SQLite persistence; main application state and logs use separate database handles/files. Current storage accepts a new empty database or the exact supported schema, rather than automatically migrating arbitrary older databases. `config.json` contains listener/runtime policies, while model sources, groups, tokens, and usage records live in SQLite.
- Shared filesystem/network policies live in `internal/dbpath` and `internal/outbound`, avoiding config dependencies on storage/relay. `internal/architecture/dependencies_test.go` enforces dependency allowlists, Hertz boundary confinement, SQL ownership, and process-exit ownership. Extend explicit boundaries when adding packages rather than bypassing these checks.

### Protocol execution model

The production format is package schema 4 / DSL 2 / Core 2. The conversion path is **external A → A Mapping → Core2 → B Mapping → external B**, not a matrix of provider-specific converters.

- **Spec** describes external documents, endpoints, transports, and lifecycle; **Mapping** defines the protocol's relationship to Core2 and conversion choices; the **DSL** provides generic execution primitives. Four shipped protocols live as `spec.json`/`mapping.json` pairs under `backend/protocol/builtin/packages/`.
- Parsing/compiling is not authorization to activate. Runtime revisions and verification evidence are pinned; ingress selects its immutable registry snapshot before authentication/body access. Preserve that snapshot across concurrent activation. Shipped presets are read-only; custom variants need distinct IDs.
- Startup restores/verifies protocols. Protocol initialization failure leaves management available for repair while disabling generation.
- Preserve absent versus null versus zero, ordering, tool associations, and source-owned extension semantics. A target Mapping must not depend on another provider's wire extension. Native replay needs provenance and semantic proofs, not an unchecked passthrough shortcut.
- Current preset base URLs use provider origins; Specs supply `/v1` or `/v1beta` paths. Transport does not automatically deduplicate path prefixes. Default outbound policy blocks loopback/private/reserved addresses; local mock upstreams require explicit policy adjustment.
- Do not retry an already-generating stream as a new request. Late failures must follow supported stream failure delivery contracts.

Read `docs/protocol-three-part-contract.md` for the current contract and `docs/protocol-cutover-v2.md` for runtime/repair behavior. Despite its filename, `docs/protocol-definition-v2.md` describes the current package format. Historical DSL migration records and generated `server/_snapshot` are not implementation authority; `docs/storage-schema.md` also contains older migration descriptions that must be checked against current storage code.

### Frontend integration

`packages/webui/src/App.tsx` uses lazy route pages and HashRouter. Production uses `/ui/` with no history fallback, so preserve hash routing. `src/lib/api.ts` centralizes `/api/admin` requests and authentication failures; `src/lib/auth.ts` coordinates token state. Shared types/API contracts are in `src/lib`, Agent streaming has its own `src/lib/agent` layer, and protocol authoring lives in `pages/protocol-designer` with protocol document helpers in `src/lib`.

Vite proxies administration, generation, gateway WebSocket, health, remote Agent, and diagnostics endpoints to the backend. Panel tokens, inference relay tokens, and agent-scoped keys are separate authorization domains; they are not interchangeable.

## Maintained references

- `docs/development.en.md`, `packages/webui/README.en.md`: setup, build semantics, frontend integration.
- `backend/TESTING.md`: targeted regression groups, race tests, fuzzing, benchmarks, and generated-reference checks.
- `docs/architecture.md`: backend ownership and dependency direction.
- `docs/webui-api.en.md`, `docs/webui-data-model.en.md`: management API and UI data contracts.

Maintain Chinese documentation and its `.en.md` counterpart together when changing documented interfaces. The Chinese CLI reference is generated, not hand-edited:

```sh
(cd backend && UPDATE_AGENT_CLI_DOCS=1 go test ./server -run '^TestCLIReferenceUpToDate$' -count=1)
```

Its introduction/formatting template lives in `agent_cli_contract_test.go`; the English CLI reference is a maintained translation. Keep temporary run data and logs in ignored `.tmp-dev/`, not new per-iteration documentation.
