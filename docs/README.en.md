# Documentation

[Project](../README.en.md) · [简体中文](README.md) · **English**

Deployment, protocols, APIs, and development for Elysia API. Start with [quick start](../README.en.md#quick-start) for the first request.

## Deployment and operations

| Document | Contents |
| --- | --- |
| [Deployment](deployment.en.md) | Binaries, macOS app, Docker, configuration, logs, upgrades, and recovery |

## Protocols and integration

| Document | Contents |
| --- | --- |
| [Maheshvara](maheshvara-protocol.en.md) | Internal model, protocol mappings, streaming, and capability limits |
| [Custom protocols](protocol-definition-reference.en.md) | JSON definitions, field mappings, conditions, stream frames, and model discovery |
| [Cache fidelity audit (Chinese)](cache-fidelity-audit-2026-10-01.md) | Four-protocol historical comparison, shared fixes, verification, and custom protocol migration |

## Management APIs

| Document | Contents |
| --- | --- |
| [Management API](webui-api.en.md) | Configuration, sources, groups, tokens, usage, and logs |
| [Data model](webui-data-model.en.md) | Field semantics, identifiers, secrets, pagination, and type sources |
| [Remote agent](remote-agent-api.en.md) | REST, MCP, A2A, authentication, sessions, and approvals |
| [CLI reference](agent-cli.en.md) | `elysia_cli` commands, arguments, batching, and limits |
| [Agent tools](agent-tools-catalog.en.md) | Tool responsibilities, help sources, and permissions |

## Development and validation

| Document | Contents |
| --- | --- |
| [Development](development.en.md) | Environment, local development, tests, cross-compilation, and release artifacts |
| [WebUI development](../packages/webui/README.en.md) | Frontend commands, proxies, builds, and interaction conventions |
| [macOS validation](macos-testing.en.md) | Native toolchain, automated checks, and device acceptance |
| [Login assets](../scripts/login-trace/README.en.md) | Extraction, review, asset validation, and performance reproduction |

See the [changelog](../CHANGELOG.md) for changes by release.

## Conventions

- Documentation describes the checked-out revision. Use the corresponding Git tag for older releases.
- Original paths contain Chinese; `.en.md` contains English. Field names, commands, endpoints, and protocol identifiers remain unchanged.
- Angle-bracket values such as `<RELAY_TOKEN>` are placeholders. Structures containing ellipses are labeled as illustrative fragments.
- Panel tokens, relay tokens, and Agent keys serve different purposes. See [authentication](remote-agent-api.en.md#authentication).
- Resolve discrepancies against routes, types, configuration loaders, and tests. See [documentation maintenance](development.en.md#documentation-maintenance).
