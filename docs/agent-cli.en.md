# elysia CLI reference

[Documentation](README.en.md) · [简体中文](agent-cli.md) · **English**

This page translates the tool definition, command table and runtime help. `elysia` is command syntax inside `elysia_cli`, not a standalone terminal program. Angle brackets and ellipses in `text` blocks are placeholders, not scripts to paste into a shell.

The built-in assistant also exposes `ask_user` and `update_plan`; see the [tool catalog](agent-tools-catalog.en.md) for responsibilities and permissions. REST/A2A follow session approvals. MCP executes directly with an `agent`-scoped key, outside that approval chain. Each MCP call is stateless; reuse drafts and test targets within one `command` batch. See [remote access](remote-agent-api.en.md).

The Chinese reference preserves runtime help verbatim. Reading notes: the `key create` name description still says “cannot change after creation”, although `key update --new-name` supports renaming; “encrypted storage” requires a successfully loaded master key; request bodies follow the [capture policy](deployment.en.md#request-logs) and are not saved by default. The legacy `--thinking` help values also differ from the current model capability values `both` / `non-thinking-only` / `thinking-only`; this field is separate from the Agent setting `thinkingEffort`. See the [definition reference](protocol-definition-reference.en.md) for complete protocol fields.

## Call contract

Execute Elysia API gateway operations. Every command starts with `elysia`. Read `elysia help` on first use, then `elysia help <group> [command]` as needed.

```json
{"command":"elysia source ls"}
```

## Overview

```text
elysia — gateway operations CLI (all operations run through elysia_cli)

Command groups:
  source     Source management (create, delete, ls, refresh, update)
  model      Individual model management (ls, rm, set)
  group      Groups and members (create, delete, ls, member add, member rm, update)
  key        API keys (inference access tokens) (create, delete, ls, update)
  protocol   Protocol design and operations (activate, diagnose, diff, draft, models, preview, read, rollback, save, schema, test, validate, verify)
  code       Bundled source and protocol references (ls, read)
  usage      Usage statistics and request logs (log, logs, stats, trend)
  syslog     System logs
  outbound   Denied outbound IP ranges (SSRF protection) (get, reset, set)

Help levels: elysia help <group> (all flags) / elysia help <group> <command> (full semantics and examples).

Syntax: 'quotes' ('' means empty), --flag value or --flag=value, batches (&& skips the rest of its chain on failure; semicolons or newlines continue),
and trailing pipelines (| grep <substring> filters case-insensitively; | head <n> keeps the first n text lines, not records; head also accepts -n N / -N).
Batches have no overall transaction or automatic rollback. Combine only operations with known arguments that do not require intermediate results. Use separate calls when results determine later arguments or actions.
Prefer each command's own filters and --limit. Output can be truncated; determine operation status from the actual returned result.
Writes, live outbound requests and deletions are subject to server permissions and business policies.

Common combinations:
  elysia source ls && elysia model ls --source primary --limit 20
  elysia usage logs --days 1 --status failed --limit 10
  elysia group create --name main --models s1:gpt-4o
```

## source

````text
elysia source — source management

  elysia source create --name <name> --base-url <URL> [--platform openai|anthropic|gemini|responses|custom:<id>] [--api-key <key>] [--auto-fetch] [--manual-models a,b] [--fetch-base-url <URL>]
    Create a source (subject to permission policy)
      --name                   Source display name
      --base-url               Upstream baseUrl (http/https)
      --platform               openai (default)/anthropic/gemini/responses/custom:<protocol-id>
      --api-key                API key (encrypted storage)
      --auto-fetch             Mark as automatic; run elysia source refresh after creation for a live upstream model list
      --manual-models          Comma-separated manual model names
      --fetch-base-url         Model-list URL (defaults to base-url)

  elysia source delete --source <id|name>
    Delete a source (irreversible, permission-controlled; cascades to models and group references)
      --source                 Source ID or name

  elysia source ls
    List all sources (masked keys)

  elysia source refresh --source <id|name>
    Fetch the upstream model list (live outbound request, permission-controlled)
      --source                 Source ID or name

  elysia source update --source <id|name> [--enabled] [--name <name>] [--base-url <URL>] [--platform <platform>] [--api-key <new-key>] [--auto-fetch[=false]] [--manual-models a,b]
    Update a source (permission-controlled; empty api-key preserves the existing key)
      --source                 Source ID or name
      --enabled                Enable/disable
      --name                   Rename
      --base-url               Replace baseUrl
      --platform               Change platform
      --api-key                New API key (empty preserves the existing value)
      --auto-fetch             Toggle automatic-source flag
      --manual-models          Replace the entire manual model list

Full semantics and examples: elysia help source <command>.
````

### source create

````text
elysia source create --name <name> --base-url <URL> [--platform openai|anthropic|gemini|responses|custom:<id>] [--api-key <key>] [--auto-fetch] [--manual-models a,b] [--fetch-base-url <URL>]
Create a source (subject to permission policy)

Arguments:
  --name                   Source display name
  --base-url               Upstream baseUrl (http/https)
  --platform               openai (default)/anthropic/gemini/responses/custom:<protocol-id>
  --api-key                API key (encrypted storage)
  --auto-fetch             Mark as automatic; run elysia source refresh after creation for a live upstream model list
  --manual-models          Comma-separated manual model names
  --fetch-base-url         Model-list URL (defaults to base-url)

Example: elysia source create --name primary --base-url https://api.example.com --api-key sk-xxx

Details: Create a source under server permissions and business policies. platform accepts openai/anthropic/gemini/responses or custom:<protocol-id>. autoFetchModels=true marks an automatic source; fetch the list afterward with elysia source refresh or manually in the UI. Manual sources use the manual list or manualModels. Confirm baseUrl, platform and credential provenance with the user before creation.
````

### source delete

````text
elysia source delete --source <id|name>
Delete a source (irreversible, permission-controlled; cascades to models and group references)

Arguments:
  --source                 Source ID or name

Example: elysia source delete --source primary

Details: Delete a source under server permissions and business policies. This is irreversible: all its models and group member references are deleted. Identify it by source ID or name. Confirm the target and impact (model count and affected groups) with the user first.
````

### source ls

````text
elysia source ls
List all sources (masked keys)


Example: elysia source ls

Details: List all sources with platform, baseUrl, enabled state, automatic fetching, masked key policy, model count and latest refresh status. No plaintext keys are shown.
````

### source refresh

````text
elysia source refresh --source <id|name>
Fetch the upstream model list (live outbound request, permission-controlled)

Arguments:
  --source                 Source ID or name

Example: elysia source refresh --source primary

Details: Fetch a source's upstream model list through a real outbound request under server permissions and business policies. For manual sources, synchronize the manual list. Use elysia source refresh after creating an automatic source. An empty upstream list fails the refresh and preserves existing cache; this neither proves that no refresh occurred nor establishes why the upstream list is empty.
````

### source update

````text
elysia source update --source <id|name> [--enabled] [--name <name>] [--base-url <URL>] [--platform <platform>] [--api-key <new-key>] [--auto-fetch[=false]] [--manual-models a,b]
Update a source (permission-controlled; empty api-key preserves the existing key)

Arguments:
  --source                 Source ID or name
  --enabled                Enable/disable
  --name                   Rename
  --base-url               Replace baseUrl
  --platform               Change platform
  --api-key                New API key (empty preserves the existing value)
  --auto-fetch             Toggle automatic-source flag
  --manual-models          Replace the entire manual model list

Example: elysia source update --source primary --enabled=false

Details: Update an existing source under server permissions and business policies: enable/disable, rename, change baseUrl/platform, replace its API key (empty preserves it), or adjust automatic fetching/manual models. Identify the source by ID or name.
````

## model

````text
elysia model — individual model management

  elysia model ls [--source <id|name>] [--search <substring>] [--limit <n>]
    Query locally cached models (optionally filter by source; use source refresh for live upstream data)
      --source                 Source ID or name
      --search                 Fuzzy name matching
      --limit                  Result count (default 50, maximum 200)

  elysia model rm --source <id|name> --model <model-id>
    Delete a model (irreversible, permission-controlled)
      --source                 Source ID or name
      --model                  Model ID

  elysia model set --source <id|name> --model <model-id> [--name <name>] [--type <type>] [--max-tokens <n>] [--vision] [--tools] [--structured] [--thinking <mode>] [--enabled[=false]]
    Update a model (permission-controlled)
      --source                 Source ID or name
      --model                  Model ID
      --name                   Rename
      --type                   Type (llm default; reranker / embedding reserved)
      --max-tokens             maxTokens
      --vision                 Vision capability flag
      --tools                  Tool capability flag
      --structured             Structured-output capability flag
      --thinking               Thinking mode (disabled default / enabled / adaptive)
      --enabled                Enable/disable

Full semantics and examples: elysia help model <command>.
````

### model ls

````text
elysia model ls [--source <id|name>] [--search <substring>] [--limit <n>]
Query locally cached models (optionally filter by source; use source refresh for live upstream data)

Arguments:
  --source                 Source ID or name
  --search                 Fuzzy name matching
  --limit                  Result count (default 50, maximum 200)

Example: elysia model ls --source primary --search gpt --limit 20

Details: Query gateway-cached models, not live upstream state (all sources by default). Filter source by ID/name and search by fuzzy name. Confirm member IDs before creating a group. Returns the first limit entries (default 50, maximum 200); summary contains the total. An empty result does not prove that no refresh occurred: check source/search filters and source state from elysia source ls. If live information is needed, run elysia source refresh --source <source> and query again.
````

### model rm

````text
elysia model rm --source <id|name> --model <model-id>
Delete a model (irreversible, permission-controlled)

Arguments:
  --source                 Source ID or name
  --model                  Model ID

Example: elysia model rm --source primary --model gpt-4o

Details: Delete one model under server permissions and business policies. This is irreversible and removes group references. Automatically fetched models may return on the next refresh; for temporary removal, prefer elysia model set --source <source> --model <model-id> --enabled=false. source accepts ID/name; model is the model ID.
````

### model set

````text
elysia model set --source <id|name> --model <model-id> [--name <name>] [--type <type>] [--max-tokens <n>] [--vision] [--tools] [--structured] [--thinking <mode>] [--enabled[=false]]
Update a model (permission-controlled)

Arguments:
  --source                 Source ID or name
  --model                  Model ID
  --name                   Rename
  --type                   Type (llm default; reranker / embedding reserved)
  --max-tokens             maxTokens
  --vision                 Vision capability flag
  --tools                  Tool capability flag
  --structured             Structured-output capability flag
  --thinking               Thinking mode (disabled default / enabled / adaptive)
  --enabled                Enable/disable

Example: elysia model set --source primary --model gpt-4o --vision --enabled=false

Details: Update a model under server permissions and business policies: enabled state, name, type, maxTokens, vision/tool/structured-output flags and thinking mode. Edited capabilities are no longer overwritten by refresh (capability_source=manual). source accepts ID/name; model is an ID obtained from elysia model ls.
````

## group

````text
elysia group — groups and membership

  elysia group create --name <group-name> [--models <src:model,...>] [--strategy round-robin|random|sequential] [--max-retries <n>] [--enabled[=false]] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]
    Create a group (permission-controlled)
      --name                   Group name (client-facing model name)
      --models                 Comma-separated members (sourceId:modelId or model name)
      --strategy               sequential=ordered failure fallback / random=random start with wraparound / round-robin=cursor rotation (default)
      --max-retries            Failure retry count (default 3)
      --enabled                Default true
      --max-concurrency        Concurrency limit (0=unlimited)
      --daily-limit-requests   Daily request limit (0=unlimited)
      --daily-limit-tokens     Daily token limit (0=unlimited)

  elysia group delete --group <group|id>
    Delete a group (irreversible, permission-controlled; may disable keys)
      --group                  Group name or ID

  elysia group ls
    List all groups and members

  elysia group member add --group <group|id> --models <list>
    Append group members (permission-controlled)
      --group                  Group name or ID
      --models                 Members (sourceId:modelId or model name)

  elysia group member rm --group <group|id> --models <list>
    Remove group members (permission-controlled)
      --group                  Group name or ID
      --models                 Member references

  elysia group update --group <group|id> [--name <new-group-name>] [--add-models <list>] [--remove-models <list>] [--enabled[=false]] [--strategy <strategy>] [--max-retries <n>] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]
    Update a group (permission-controlled; rename/members/strategy/quotas)
      --group                  Group name or ID
      --name                   Rename; changes the client model name, without migrating keys' old group grants
      --add-models             Append members
      --remove-models          Remove members
      --enabled                Enable/disable
      --strategy               sequential=failure fallback / random=random start with wraparound / round-robin=cursor rotation (default)
      --max-retries            Retry count
      --max-concurrency        Concurrency limit
      --daily-limit-requests   Daily request limit
      --daily-limit-tokens     Daily token limit

Full semantics and examples: elysia help group <command>.
````

### group create

````text
elysia group create --name <group-name> [--models <src:model,...>] [--strategy round-robin|random|sequential] [--max-retries <n>] [--enabled[=false]] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]
Create a group (permission-controlled)

Arguments:
  --name                   Group name (client-facing model name)
  --models                 Comma-separated members (sourceId:modelId or model name)
  --strategy               sequential=ordered failure fallback / random=random start with wraparound / round-robin=cursor rotation (default)
  --max-retries            Failure retry count (default 3)
  --enabled                Default true
  --max-concurrency        Concurrency limit (0=unlimited)
  --daily-limit-requests   Daily request limit (0=unlimited)
  --daily-limit-tokens     Daily token limit (0=unlimited)

Example: elysia group create --name main --models s1:gpt-4o,s1:gpt-4o-mini

Details: Create a group under server permissions and business policies, with member references (sourceId:modelId or model name), strategy and retries. Names must be unique. Confirm members with elysia model ls and check existing names with elysia group ls first.
````

### group delete

````text
elysia group delete --group <group|id>
Delete a group (irreversible, permission-controlled; may disable keys)

Arguments:
  --group                  Group name or ID

Example: elysia group delete --group retired

Details: Delete a group under server permissions and business policies. This is irreversible; clients can no longer call its name. Keys authorized only for this group are disabled; the result lists them. Identify the group by name or ID.
````

### group ls

````text
elysia group ls
List all groups and members


Example: elysia group ls

Details: List all groups with name, enabled state, strategy, retries, concurrency/quotas and members (sourceId:modelId references, or sometimes only a model ID).
````

### group member add

````text
elysia group member add --group <group|id> --models <list>
Append group members (permission-controlled)

Arguments:
  --group                  Group name or ID
  --models                 Members (sourceId:modelId or model name)

Example: elysia group member add --group main --models s1:o1,s1:o2

Details: Update an existing group under server permissions and business policies: name, enabled state, strategy, retries, concurrency/quotas and members. Identify it by name or ID. Renaming changes the client-facing model name. API key grants (allowedGroups) referencing the old name do not migrate automatically; confirm the impact before renaming.
````

### group member rm

````text
elysia group member rm --group <group|id> --models <list>
Remove group members (permission-controlled)

Arguments:
  --group                  Group name or ID
  --models                 Member references

Example: elysia group member rm --group main --models s1:o2

Details: Update an existing group under server permissions and business policies: name, enabled state, strategy, retries, concurrency/quotas and members. Identify it by name or ID. Renaming changes the client-facing model name. API key grants (allowedGroups) referencing the old name do not migrate automatically; confirm the impact before renaming.
````

### group update

````text
elysia group update --group <group|id> [--name <new-group-name>] [--add-models <list>] [--remove-models <list>] [--enabled[=false]] [--strategy <strategy>] [--max-retries <n>] [--max-concurrency <n>] [--daily-limit-requests <n>] [--daily-limit-tokens <n>]
Update a group (permission-controlled; rename/members/strategy/quotas)

Arguments:
  --group                  Group name or ID
  --name                   Rename; changes the client model name, without migrating keys' old group grants
  --add-models             Append members
  --remove-models          Remove members
  --enabled                Enable/disable
  --strategy               sequential=failure fallback / random=random start with wraparound / round-robin=cursor rotation (default)
  --max-retries            Retry count
  --max-concurrency        Concurrency limit
  --daily-limit-requests   Daily request limit
  --daily-limit-tokens     Daily token limit

Example: elysia group update --group main --add-models s1:o1 --enabled=false

Details: Update an existing group under server permissions and business policies: name, enabled state, strategy, retries, concurrency/quotas and members. Identify it by name or ID. Renaming changes the client-facing model name. API key grants (allowedGroups) referencing the old name do not migrate automatically; confirm the impact before renaming.
````

## key

````text
elysia key — API keys (inference access tokens)

  elysia key create --name <name> [--secret <secret>] [--allowed-groups <groups,...>] [--enabled[=false]]
    Create an inference key (permission-controlled; empty secret generates one; plaintext returned once)
      --name                   Key name (primary key; help says it cannot change after creation)
      --secret                 Plaintext key; use the supplied value (weak values may be flagged but not rejected); empty generates a random value
      --allowed-groups         Comma-separated allowed groups (empty=unrestricted)
      --enabled                Default true

  elysia key delete --name <name>
    Delete an API key (irreversible, permission-controlled; remote-access keys rejected)
      --name                   Key name

  elysia key ls
    List API keys (masked)

  elysia key update --name <name> [--new-name <new-name>] [--enabled[=false]] [--allowed-groups <groups,...>] [--new-secret <new-secret>]
    Update an API key (permission-controlled; rename/enable/grants/secret; remote-access keys rejected)
      --name                   Key name
      --new-name               Rename (fails if the target name is taken)
      --enabled                Enable/disable
      --allowed-groups         Replace the entire allowed-group list
      --new-secret             New plaintext; empty preserves the existing value

Full semantics and examples: elysia help key <command>.
````

### key create

````text
elysia key create --name <name> [--secret <secret>] [--allowed-groups <groups,...>] [--enabled[=false]]
Create an inference key (permission-controlled; empty secret generates one; plaintext returned once)

Arguments:
  --name                   Key name (primary key; help says it cannot change after creation)
  --secret                 Plaintext key; use the supplied value (weak values may be flagged but not rejected); empty generates a random value
  --allowed-groups         Comma-separated allowed groups (empty=unrestricted)
  --enabled                Default true

Example: elysia key create --name mobile-app --allowed-groups main

Details: Create an API key for inference calls to /v1 under server permissions and business policies. Use a user-supplied secret exactly as given (warn about weak values if appropriate, but do not reject them on the user's behalf); empty generates a random secret. The complete secret is returned once in this result; ask the user to save it immediately. Empty allowedGroups grants all groups, so confirm this broader scope first. Remote-access keys used to drive the assistant are managed by the user in Runtime Configuration; elysia key cannot create them.
````

### key delete

````text
elysia key delete --name <name>
Delete an API key (irreversible, permission-controlled; remote-access keys rejected)

Arguments:
  --name                   Key name

Example: elysia key delete --name mobile-app

Details: Delete a key under server permissions and business policies. This is irreversible; clients using it immediately lose access. Delete remote-access keys (agent scope) in Runtime Configuration. Confirm the name first.
````

### key ls

````text
elysia key ls
List API keys (masked)


Example: elysia key ls

Details: List API keys (access tokens), with masked token values. Empty allowedGroups grants all groups. Keys with agent scope are remote-access keys for the assistant, excluded from inference and managed by the user in Runtime Configuration; these commands cannot write them.
````

### key update

````text
elysia key update --name <name> [--new-name <new-name>] [--enabled[=false]] [--allowed-groups <groups,...>] [--new-secret <new-secret>]
Update an API key (permission-controlled; rename/enable/grants/secret; remote-access keys rejected)

Arguments:
  --name                   Key name
  --new-name               Rename (fails if the target name is taken)
  --enabled                Enable/disable
  --allowed-groups         Replace the entire allowed-group list
  --new-secret             New plaintext; empty preserves the existing value

Example: elysia key update --name mobile-app --allowed-groups main,backup

Details: Update a key under server permissions and business policies: rename (newName fails on a name collision), enable/disable, change allowed groups or replace plaintext (empty newSecret preserves it). Remote-access keys with agent scope are user-managed in Runtime Configuration and cannot be modified by elysia key. Empty allowedGroups is unrestricted; confirm this scope before changing it.
````

## protocol

All commands use the running v2 engine. Read the vendor documentation and complete
examples, then `elysia protocol schema`. Declare independent directions,
transports, capabilities, mappings and expected fixtures in a complete
`schemaVersion: 2` definition. Legacy shape templates are import-only.

The workflow is draft ? validate/preview/verify ? repair ? save ? activate.
Unsupported mechanisms must be reported; do not delete required fields/tools,
reduce requested capabilities or shrink necessary tests to hide a gap. A saved
draft is not an active protocol. The editor and CLI use the same compiler,
verifier and immutable revisions. Offline and real-target evidence are separate;
HTTP 200 does not prove fidelity. Protocol mappings cannot change gateway
authorization or execute client business tools.

### protocol schema

```text
elysia protocol schema [--section <section>] [--type <type>]
```

Without a section, return the installed compiler/schema version, features,
directions, transports, operation kinds and capabilities. Sections are
`definition`, `semantic`, `binding`, `directions`, `capabilities`, `operations`,
`events`, `diagnostics`, `modules`. `--type` selects a named type from a section
containing `$defs`; it is not valid for a list catalog.

```text
elysia protocol schema --section definition --type Definition
elysia protocol schema --section semantic --type Request
elysia protocol schema --section operations
```

### protocol draft

```text
elysia protocol draft '<complete schemaVersion=2 JSON>'
```

The sole positional argument is the complete definition, not a patch. Retain the
working draft in this tool context and compile it. A malformed definition is
retained for repair but reports failure; it is not persisted or activated. Edit
mode must retain the selected protocol ID. REST/A2A use session context; stateless
MCP callers keep dependent draft operations in one command batch. `read` returns
stored content; pass edited content through `draft` to set the working draft.

Complete standalone examples: [text-alpha](../backend/protocol/testdata/text-alpha.json),
[text-beta](../backend/protocol/testdata/text-beta.json), and
[cached-text](examples/cached-text-v2.json). See the
[definition reference](protocol-definition-reference.en.md) for the contract.

### protocol validate

```text
elysia protocol validate [--id <id>] [--hash <revision-hash>]
```

Compile the working draft, or select a saved protocol with case-sensitive `--id`.
With ID but no hash, read its draft; with both, read the immutable revision.
Return validity, structured issues and the compiled hash when valid. This does
not run the full fixture suite, persist evidence or activate a revision.

### protocol verify

```text
elysia protocol verify [--id <id>] [--hash <revision-hash>]
```

Select content as in `validate`, compile and run offline capability/fidelity
verification. Return a report bound to content hash, compiler version and
fixtures. No provider request is sent and this command does not persist the
report. `save` stores current draft evidence; the management API can reverify
an existing immutable revision for rollback.

### protocol diagnose

```text
elysia protocol diagnose [--id <id>] [--hash <revision-hash>]
```

Run the same offline verifier as `verify` and return structured diagnostics for
repair. This is not a live provider probe and does not modify the definition.
Use issue codes, directions, paths and suggestions to revise the draft.

### protocol preview

```text
elysia protocol preview --sample '<JSON>' [--direction <direction>] [--sequence] [--mode mapping|session|task|models|agent] [--sample-id <id>] [--operation <name>] [--kind decode|encode|control] [--purpose submit|status|result|cancel]
```

Preview the working draft using the shared runtime, without saving or enabling:

| Flag | Meaning |
| --- | --- |
| `--sample` | Complete input JSON; event arrays for a sequence, semantic input for an encoder |
| `--direction` | Independent mapping direction from the current schema; required in mapping mode |
| `--sequence` | Replay the input as an ordered event sequence |
| `--mode` | `mapping` by default; `session`, `task`, `models` and `agent` select workflow previews |
| `--sample-id` | Declared session fixture ID; session mode uses this fixture instead of `--sample` |
| `--operation` | Declared operation name for model/task previews |
| `--kind` | Task mapping: `decode`, `encode` or `control` |
| `--purpose` | Task phase: `submit`, `status`, `result` or `cancel` |

For `models`, supply the wire model-list response. For `agent`, supply semantic
`AgentPreferences`; it previews parameter rendering, not a model invocation.
Model discovery requires declared model operations and `modelSamples`. Agent
use requires tool result input type, parameter policies and thinking-mode
fixtures. Results include output, semantic data and located issues.

### protocol test

```text
elysia protocol test --operation <name> --sample '<complete semantic request JSON>' [--base-url <URL>] [--api-key <key>]
```

Probe an actual upstream using the working draft. The operation must be declared;
its transport selects streaming behavior. `--sample` is a semantic `Request`, not
a vendor wire request. URL/key may come from the current test target; explicit
arguments override it. Credentials are not written to verification reports.
Server outbound permissions, address restrictions and approval policies still
apply. The result is separate real-target contract evidence, not activation.

### protocol models

```text
elysia protocol models [--base-url <URL>] [--api-key <key>]
```

Probe the declared model-discovery operation against the real target. Target
selection and permissions match `test`. The definition must contain a supported
model operation; do not invent an endpoint from its provider name.

### protocol save

```text
elysia protocol save [--expected <current-draft-hash>]
```

Persist the working draft. Use the previously read draft hash to detect concurrent
edits. If compilation succeeds, also store the immutable revision and current
offline report. Failed verification leaves a repairable draft and cannot justify
activation. A successful save means persistence succeeded; inspect its report
before activation. Saving never activates, binds sources or replaces in-flight
request revisions. Writes remain permission-controlled.

### protocol read

```text
elysia protocol read --id <id> [--hash <revision-hash>]
```

Read the case-sensitive ID's draft and conditional-save hash, or the immutable
revision selected by `--hash`. It does not implicitly replace the working draft.

### protocol diff

```text
elysia protocol diff --id <id> --from <revision-hash> --to <revision-hash>
```

Compare two persisted immutable revisions without changing either or activating.

### protocol activate

```text
elysia protocol activate --id <id> --hash <revision-hash> [--expected <current-active-hash>]
```

Activate exactly the selected immutable revision with current valid offline
evidence. `--expected` checks the current active pointer, not the draft hash;
an empty expected value denotes no active revision. Edit sessions must preserve
their selected ID. Existing permission/approval gates apply; reports supplied by
the caller cannot bypass server verification. In-flight requests retain their
pinned revision.

### protocol rollback

```text
elysia protocol rollback --id <id> --hash <revision-hash> [--expected <current-active-hash>]
```

Activate a prior revision under the same gate as `activate`. It must be loadable
and verified by the current engine. This does not roll back the executable or
database migration; those require matching backups.

The runtime currently exposes the shared selector flags
`--id`, `--hash`, `--expected`, `--section`, `--type`, `--from`, `--to` on the seven
schema/validate/verify/diagnose/diff/activate/rollback commands. Only the flags
listed under each command have meaning for that operation. Check
`elysia help protocol <command>` for the current parser's accepted flags.

## code

Read the source snapshot bundled by `scripts/build-standalone.mjs`. This is a
read-only build snapshot, not arbitrary filesystem access. A plain development
`go build` with no generated snapshot returns a descriptive notice.

### code ls

```text
elysia code ls [prefix]
```

List sorted repository-relative paths, optionally filtered by a positional
prefix. The snapshot includes backend Go, definitions/fixtures, WebUI source,
protocol guides and CLI references.

```text
elysia code ls backend/protocol/builtin/definitions
elysia code ls packages/webui/src/lib/
elysia code ls docs/
```

### code read

```text
elysia code read <path>
```

Read one repository-relative snapshot file. Browse with `code ls` first; use
`grep`/`head` pipelines to limit large output. Missing paths return an error and
may include a nearby path hint. Presets in `backend/server/presets/` are historical
migration inputs; active v2 definitions live under `backend/protocol/builtin/definitions/`.

```text
elysia code read backend/protocol/builtin/definitions/anthropic-api.json
elysia code read docs/protocol-guide.en.md
```

## usage

````text
elysia usage — usage statistics and request logs

  elysia usage log <requestId>
    Read a request log (including four captured bodies)
      <requestId>              Request log requestId

  elysia usage logs [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <name>] [--key <name>] [--group <name>] [--status success|failed] [--code <status-code>] [--limit <n>]
    Query request logs (including error filters)
      --days                   Last N days (default 7, maximum 366)
      --from                   Start time (RFC3339)
      --to                     End time (RFC3339)
      --model                  Filter by model name
      --key                    Filter by API key name
      --group                  Filter by group
      --status                 success | failed
      --code                   Exact status code
      --limit                  Result count (default 20, maximum 100)

  elysia usage stats [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <name>] [--key <name>] [--group <name>]
    Query usage totals and model distribution
      --days                   Last N days (default 7, maximum 366)
      --from                   Start time (RFC3339)
      --to                     End time (RFC3339)
      --model                  Filter by model name
      --key                    Filter by API key name
      --group                  Filter by group

  elysia usage trend [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <name>] [--key <name>] [--group <name>]
    Query daily usage trends
      --days                   Last N days (default 7, maximum 366)
      --from                   Start time (RFC3339)
      --to                     End time (RFC3339)
      --model                  Filter by model name
      --key                    Filter by API key name
      --group                  Filter by group

Full semantics and examples: elysia help usage <command>.
````

### usage log

````text
elysia usage log <requestId>
Read a request log (including four captured bodies)

  <requestId>              Request log requestId

Example: elysia usage log req-123

Details: Read a complete log by requestId: four captured bodies (inbound/outbound/upstream response/client response), retry chain and error details. Use for detailed failure analysis.
````

### usage logs

````text
elysia usage logs [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <name>] [--key <name>] [--group <name>] [--status success|failed] [--code <status-code>] [--limit <n>]
Query request logs (including error filters)

Arguments:
  --days                   Last N days (default 7, maximum 366)
  --from                   Start time (RFC3339)
  --to                     End time (RFC3339)
  --model                  Filter by model name
  --key                    Filter by API key name
  --group                  Filter by group
  --status                 success | failed
  --code                   Exact status code
  --limit                  Result count (default 20, maximum 100)

Example: elysia usage logs --days 1 --status failed --limit 20

Details: List logs newest first, with status, error/error category, model, key, latency and tokens. status=failed selects failures. Use elysia usage log <requestId> for captured request/response details.
````

### usage stats

````text
elysia usage stats [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <name>] [--key <name>] [--group <name>]
Query usage totals and model distribution

Arguments:
  --days                   Last N days (default 7, maximum 366)
  --from                   Start time (RFC3339)
  --to                     End time (RFC3339)
  --model                  Filter by model name
  --key                    Filter by API key name
  --group                  Filter by group

Example: elysia usage stats --days 7 --group main

Details: Query request/success/failure/token/cache-hit/average-latency totals and model distribution. Select the window with days (default 7) or from/to (RFC3339); filter by model, key or group name.
````

### usage trend

````text
elysia usage trend [--days <n>] [--from <RFC3339>] [--to <RFC3339>] [--model <name>] [--key <name>] [--group <name>]
Query daily usage trends

Arguments:
  --days                   Last N days (default 7, maximum 366)
  --from                   Start time (RFC3339)
  --to                     End time (RFC3339)
  --model                  Filter by model name
  --key                    Filter by API key name
  --group                  Filter by group

Example: elysia usage trend --days 30

Details: Query daily trends (requests/successes/failures/tokens) for charts. Window and filters match elysia usage stats.
````

## syslog

````text
elysia syslog [--level info|warn|error] [--limit <n>]
Query system logs

Arguments:
  --level                  info | warn | error (all by default)
  --limit                  Result count (default 30, maximum 100)

Example: elysia syslog --level error --limit 50

Details: Query system operation logs at info/warn/error levels to diagnose gateway problems.
````

## outbound

````text
elysia outbound — denied outbound IP ranges (SSRF protection)

  elysia outbound get
    Read denied outbound IP ranges (SSRF protection)

  elysia outbound reset
    Restore preset denied ranges (permission-controlled)

  elysia outbound set --ranges <CIDR,...>   # Empty list = allow all
    Replace all denied outbound ranges (permission-controlled; high impact)
      --ranges                 Comma-separated denied CIDRs (empty=allow all)

Full semantics and examples: elysia help outbound <command>.
````

### outbound get

````text
elysia outbound get
Read denied outbound IP ranges (SSRF protection)


Example: elysia outbound get

Details: Read the current denied outbound IP ranges and preset defaults without modifying them.
````

### outbound reset

````text
elysia outbound reset
Restore preset denied ranges (permission-controlled)


Example: elysia outbound reset

Details: Restore the built-in SSRF baseline (loopback/private/link-local/multicast and other ranges). This discards the custom list; confirm with the user first.
````

### outbound set

````text
elysia outbound set --ranges <CIDR,...>   # Empty list = allow all
Replace all denied outbound ranges (permission-controlled; high impact)

Arguments:
  --ranges                 Comma-separated denied CIDRs (empty=allow all)

Example: elysia outbound set --ranges 10.0.0.0/8,172.16.0.0/12

Details: Replace the complete denied outbound IP list for SSRF protection. --ranges supplies the full replacement CIDR list, not incremental additions/removals; empty allows every address. If a local/private upstream (such as 127.0.0.1) is blocked with "refused to dial denied IP", verify the target, service ownership and user authorization, then explain the exact CIDRs, impact and risk. Do not relax policy just because a request was blocked. Preserve unrelated denied ranges and change only within authorization.
````

## Documentation synchronization

The test generates the Chinese reference; change its template rather than editing generated output. Maintain this complete English translation manually, synchronizing every command, flag, default, constraint and example against the same Chinese generation. Runtime help remains Chinese.

Regenerate Chinese from `backend`, then check freshness:

```sh
UPDATE_AGENT_CLI_DOCS=1 go test ./server -run '^TestCLIReferenceUpToDate$' -count=1
go test ./server -run '^TestCLIReferenceUpToDate$' -count=1
```
