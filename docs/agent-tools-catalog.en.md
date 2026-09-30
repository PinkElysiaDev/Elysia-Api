# Agent tools and permissions

[Documentation](README.en.md) · [简体中文](agent-tools-catalog.md) · **English**

## Tool contract

The built-in assistant exposes three tools to its model. `elysia` is command syntax inside `elysia_cli`, not an installable terminal executable.

| Tool | Purpose |
| --- | --- |
| `elysia_cli` | Execute gateway management commands |
| `ask_user` | Ask a question and wait for a selection or text response |
| `update_plan` | Record task steps and progress |

Call arguments:

```json
{"command":"elysia source ls"}
```

Legacy tool names have no compatibility aliases. Historical messages are not migrated; restart tasks whose pending legacy calls have expired.

## Prompt responsibilities

| Source | Content |
| --- | --- |
| [System prompt](../backend/server/agent_prompt.go) | Role, Chinese interaction, plans, questions, titles and charts; dynamically adds plan mode and the edit target ID |
| [Tool description](../backend/server/agent_cli_tool.go) | Purpose, command prefix and help entry points |
| [Command help](../backend/server/agent_cli_help.go) | Arguments, batches, pipelines, business constraints and integration examples |

Read `elysia help` on first use, then `elysia help <group> [command]` as needed. Known commands can run directly. See the [CLI reference](agent-cli.en.md) for the complete command set.

## Execution and permissions

The [CLI parser](../backend/server/agent_cli.go) dispatches commands to business handlers. Internal handler names are not model tools. Batch permissions are aggregated from the parsed commands.

| Entry point | Permissions and state |
| --- | --- |
| WebUI assistant, REST, A2A | Session permissions `save`, `live_test`, `delete`; `ask` pauses for confirmation, `always` allows, `never` rejects; plan mode blocks controlled operations |
| MCP | Exposes only `elysia_cli`; an `agent`-scoped key executes directly, without model calls, the built-in assistant approval chain or session plan mode |

Each MCP call creates a temporary CLI context. Reuse protocol drafts, test addresses and credentials within a single `command` batch. REST/A2A sessions are managed by the remote Agent service; see [remote access](remote-agent-api.en.md).

Neither entry point provides a batch-wide transaction or automatic rollback. Combine only operations with known arguments that do not depend on intermediate results. Live upstream tests may incur charges; readable log bodies depend on the [capture policy](deployment.en.md#request-logs).

## Verification

Run from `backend`:

```sh
go test ./agent ./server
```

Existing tests cover parsing, published tools, permission modes, approval recovery, rejected legacy names, and MCP authentication, isolation, batches and cancellation. The frontend `agent-stream-and-chart.spec.ts` checks live command display and historical replay.

Live-model tests are skipped by default. Only when actual model behavior needs evaluation, prepare a private JSON file containing `source` (`storage.ModelSource`) and `model` (`storage.Model`), then run this optional command. It generates real model usage:

```sh
ELYSIA_AGENT_EVAL_MODEL_FILE=/path/to/private-model-fixture.json go test ./server -run '^TestCLILivePromptTasks$' -count=1 -v -timeout 12m
```

Operational data goes into a temporary database, and the model-list upstream uses a local synthetic service. Inspect command order, help queries and final reports for failed-log analysis, group creation, empty model lists and protocol draft edits. Do not commit the credential file; delete it after testing.

## Troubleshooting

- Rejected command: check the entry point, key scopes, session permissions and plan mode.
- Missing MCP draft or test target: keep related operations in one call; context does not persist across calls.
- Batch stopped partway through: inspect completed operations before compensating or retrying; earlier writes are not automatically rolled back.
