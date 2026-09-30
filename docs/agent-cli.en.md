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
  protocol   Custom protocol drafts, offline previews, live tests and saving (draft, models, preview, read, save, test)
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

````text
elysia protocol — custom protocols (draft/offline preview/live test/save)

  elysia protocol draft '<complete configuration JSON>' [--example '<sample response JSON>']
    Write/update a draft (immediate validation and offline checks)
      --example                Upstream response JSON for offline response-mapping checks
      <config>                 Complete CustomProtocolConfig JSON (single quotes recommended)

  elysia protocol models [--base-url <URL>] [--api-key <key>]
    Test upstream discovery using the draft's models configuration (permission-controlled)
      --base-url               Upstream baseUrl (defaults to one recorded in the current CLI context)
      --api-key                API key (defaults to one recorded in the current CLI context)

  elysia protocol preview [--sample '<sample Maheshvara request JSON>']
    Render the draft request offline (do not send)
      --sample                 Custom sample request JSON

  elysia protocol read --id <protocol-id>
    Read a saved protocol's full configuration
      --id                     Protocol ID (including built-in presets)

  elysia protocol save [--update <protocol-id>]
    Save the current draft as a protocol (permission-controlled)
      --update                 Explicitly update an existing protocol; target must exist and match the draft ID

  elysia protocol test [--base-url <URL>] [--api-key <key>] [--stream] [--sample '<sample request JSON>']
    Send a test request to a real upstream (permission-controlled)
      --base-url               User-provided upstream baseUrl (defaults to one recorded in the current CLI context)
      --api-key                User-provided API key (defaults to one recorded in the current CLI context)
      --stream                 Test streaming (SSE)
      --sample                 Custom sample request JSON

Full semantics and examples: elysia help protocol <command>.
````

### protocol draft

````text
elysia protocol draft '<complete configuration JSON>' [--example '<sample response JSON>']
Write/update a draft (immediate validation and offline checks)

Arguments:
  --example                Upstream response JSON for offline response-mapping checks
  <config>                 Complete CustomProtocolConfig JSON (single quotes recommended)

Example: elysia protocol draft '{"id":"my-api","request":{...}}' --example '{"text":"hi"}'

Details: Integration workflow:
1. Read the user's API documentation/examples: identify authentication, endpoint paths, request/response shapes, SSE support and any model-list endpoint.
2. Submit elysia protocol draft '<complete configuration JSON>'; include --example '<sample JSON>' for offline response mapping when available. Failed validation returns issues; fix and resubmit.
3. Run elysia protocol preview offline. Ask for the test target (baseUrl / API key), then use elysia protocol test --stream and elysia protocol models for live tests (permission-controlled; pass credentials with --base-url / --api-key). Refine from results.
4. Save with elysia protocol save after passing. Serving requests also requires a source (--platform custom:<protocol-id>) and a group.

Top-level fields: id (required, short lowercase English identifier), name, version, type (llm default / reranker and embedding reserved / x- extensions), request (required), response.

### request (gateway → upstream)
- method: GET/POST/PUT/PATCH/DELETE; default POST.
- path: relative to the source baseUrl; supports {{maheshvara.<field>}} interpolation and must not contain a scheme.
- pathStream: streaming path override (for example Gemini :streamGenerateContent?alt=sse).
- headers / query: static key/value pairs (no authentication headers; use auth).
- contentType: default application/json.
- auth: {"mode": "bearer|none|header|query", "header": "...", "prefix": "...", "query": "..."}. Use bearer (default) for Bearer tokens, header for X-Api-Key, query for key parameters and none for no authentication.
- body: field-level request construction tree. Containers are ordinary JSON objects/arrays; leaves are one of:
  - Field reference {"field": "<request catalog field>", "mode": "json|string", "default": <optional JSON literal>, "omitIfEmpty": <optional true>}. json inserts native values (required for objects/arrays/numbers/booleans); string inserts strings.
  - Constant {"value": <any JSON>}: required upstream fields with no Maheshvara equivalent, such as versions or fixed formatting options.

### response (upstream → Maheshvara; JSON bodies only)
- body: response construction tree (recommended). Containers follow the sample's JSON object/array shape. Leaves are mappings {"field": "<response catalog field>", "value": <sample>, "transform": "<optional>"} or placeholders {"value": ...}. Preserve array levels; for example, annotate the first choices entry.
- fields: equivalent row list [{"path", "field", "transform"?}], with dot paths and array indices such as choices[0].delta.content. Choose either fields or body.
- Map text/reasoning/tool_calls/usage/stop_reason/id/model/error wherever the upstream documents them.
- stream: configure only for documented SSE: {"payloadPath": "...", "mode": "delta|cumulative", "events": [...], "doneValues": ["[DONE]"], "response": {"body": {...} or "fields": [...]}}.

Request catalog (field values in request.body leaves):
- model — Routed upstream model name (string)
- instructions — System instructions (string)
- messages — Message array (role + content) (native)
- input_items — Responses-style input items (native)
- stream — Whether streaming is enabled (scalar)
- stream_options — Streaming options (native)
- tools — Tool definitions (native)
- tool_choice — Tool selection policy (native)
- parallel_tool_calls — Allow parallel tool calls (scalar)
- max_output_tokens — Maximum output tokens (scalar)
- min_output_tokens — Minimum output tokens (scalar)
- temperature — Temperature (scalar)
- top_p — Top-P (scalar)
- top_k — Top-K (scalar)
- stop — Stop sequences (native)
- n — Candidate count (scalar)
- seed — Random seed (scalar)
- presence_penalty — Presence penalty (scalar)
- frequency_penalty — Frequency penalty (scalar)
- repetition_penalty — Repetition penalty (scalar)
- logprobs — Return log probabilities (scalar)
- top_logprobs — Log-probability count (scalar)
- typical_p — Typical-P (scalar)
- min_p — Min-P (scalar)
- top_a — Top-A (scalar)
- response_format — Response format, such as JSON schema (native)
- reasoning — Reasoning settings, such as effort (native)
- thinking — Thinking settings, such as budget_tokens (native)
- reasoning_effort — Reasoning effort produced by chat shaping (string)
- output_config — Output settings for anthropic adaptive thinking (native)
- thinking_config — Thinking settings produced by gemini shaping (native)
- tool_config — Tool selection settings produced by gemini shaping (native)
- modalities — Output modalities (native)
- audio — Audio settings (native)
- safety_settings — Safety settings (native)
- service_tier — Service tier (string)
- verbosity — Output verbosity (string)
- user — End-user identifier (string)
- include — Responses include list; shaping also appends encrypted reasoning (native)
- prompt_cache_key — Prompt cache key (string)
- metadata — Metadata (native)
- raw_extra — Unknown fields passed through from the client; alias extra (native)

Response catalog (field values in response mappings):
- text — Body text
- reasoning — Reasoning text
- tool_calls — Tool-call array
- usage — Usage object, with automatic recognition of multiple key names
- usage.input_tokens — Input tokens
- usage.output_tokens — Output tokens
- usage.total_tokens — Total tokens
- usage.cached_input_tokens — Cached input tokens
- usage.reasoning_tokens — Reasoning tokens
- stop_reason — Finish reason
- id — Response ID
- model — Model name
- status — Status
- error — Error object
- created_at — Creation timestamp in seconds
- service_tier — Service tier
- system_fingerprint — System fingerprint
- metadata — Metadata, including metadata.<key> subkeys
- output — Structured output items, with transforms such as output_items
- metadata subkeys (such as metadata.vendor) carry upstream-specific metadata.

Optional transforms (usually unnecessary; usage.* defaults to int):
(empty), identity, raw, string, text, join, int, integer, number, float, bool, boolean, json, parse_json, json_string, timestamp_ms, first, usage, content_parts, tool_calls, output_items

Design notes:
- Map a whole usage object with field "usage" for automatic key recognition; map individual fields only for unusual structures.
- Preserve array levels when inferring shapes from sample responses/screenshots.
- Supply required fixed parameters (versions, formats) with constant value leaves.
- Prefer request.shape (openai-chat/anthropic/gemini/responses) for messages/tools matching an existing wire format.
- Use leaf when/omitIf for conditional inclusion, stream.finishWhen/statusWhen for finish detection, and stream.done for typed terminal values.
- Use stream.frames when events have different shapes (select by event or match); use frame.tool for split tool-call frames.
- Declare aliases for unrecognized key names; use textFilter/reasoningFilter to extract typed blocks from arrays.
- Read built-in presets with `elysia protocol read --id <id>`: chat-completions-api / responses-api / anthropic-api / gemini-api.

Complete example (anthropic-api preset):
```json
{"id":"anthropic-api","name":"Anthropic API (preset)","version":"3","type":"llm","request":{"method":"POST","path":"/v1/messages","shape":"anthropic","headers":{"anthropic-version":"2023-06-01"},"auth":{"mode":"header","header":"x-api-key"},"body":{"model":{"field":"model","mode":"string"},"messages":{"field":"messages"},"system":{"field":"instructions","mode":"string","omitIfEmpty":true},"max_tokens":{"field":"max_output_tokens","default":65536},"stream":{"field":"stream"},"temperature":{"field":"temperature","omitIfEmpty":true},"top_p":{"field":"top_p","omitIfEmpty":true},"top_k":{"field":"top_k","omitIfEmpty":true},"stop_sequences":{"field":"stop","omitIfEmpty":true},"thinking":{"field":"thinking","omitIfEmpty":true},"output_config":{"field":"output_config","omitIfEmpty":true},"tools":{"field":"tools","omitIfEmpty":true},"tool_choice":{"field":"tool_choice","omitIfEmpty":true}}},"response":{"textPath":"content","textFilter":[{"path":"type","op":"equals","value":"text"}],"reasoningPath":"content","reasoningFilter":[{"path":"type","op":"equals","value":"thinking"}],"toolCallsPath":"content","usagePath":"usage","finishReasonPath":"stop_reason","stream":{"frames":[{"event":"error","response":{"errorPath":"error"}},{"event":"message_start","response":{"usagePath":"message.usage"}},{"event":"content_block_start","match":{"path":"content_block.type","op":"equals","value":"tool_use"},"tool":{"idPath":"content_block.id","indexPath":"index","namePath":"content_block.name"}},{"event":"content_block_delta","match":{"path":"delta.type","op":"equals","value":"text_delta"},"response":{"textPath":"delta.text"}},{"event":"content_block_delta","match":{"path":"delta.type","op":"equals","value":"thinking_delta"},"response":{"reasoningPath":"delta.thinking"}},{"event":"content_block_delta","match":{"path":"delta.type","op":"equals","value":"signature_delta"},"response":{"signaturePath":"delta.signature","signatureProvider":"anthropic"}},{"event":"content_block_delta","match":{"path":"delta.type","op":"equals","value":"citations_delta"},"response":{"citationsPath":"delta.citation"}},{"event":"content_block_delta","match":{"path":"delta.type","op":"equals","value":"input_json_delta"},"tool":{"indexPath":"index","argumentsPath":"delta.partial_json"}},{"event":"content_block_stop","tool":{"indexPath":"index"},"toolDone":true},{"event":"message_delta","response":{"usagePath":"usage","finishReasonPath":"delta.stop_reason"},"terminal":true}]}},"models":{"path":"/v1/models","listPath":"data"},"aliases":{"textKeys":["text","content","message","value","output","thinking"],"usage":{"cache_creation":["cache_creation_input_tokens"],"cache_read":["cache_read_input_tokens"]},"toolCall":{"arguments":["input"],"id":["id"],"name":["name"]}},"metadata":{"description":"Anthropic API wire preset; v3: vendor-neutral naming; v2: thinking/output_config shaping, signature/citations frames, argument-completion frames and cached-usage aliases","preset":true,"presetVersion":3}}
```

````

### protocol models

````text
elysia protocol models [--base-url <URL>] [--api-key <key>]
Test upstream discovery using the draft's models configuration (permission-controlled)

Arguments:
  --base-url               Upstream baseUrl (defaults to one recorded in the current CLI context)
  --api-key                API key (defaults to one recorded in the current CLI context)

Example: elysia protocol models --base-url https://api.example.com

Details: Fetch and parse a real upstream model list using the draft's models configuration, under server permissions and business policies. This validates discovery; missing models configuration is an error. Pass user-provided baseUrl/API key as arguments. Reuse only targets successfully recorded by this command or protocol test in the current CLI context, not credentials from other commands.
````

### protocol preview

````text
elysia protocol preview [--sample '<sample Maheshvara request JSON>']
Render the draft request offline (do not send)

Arguments:
  --sample                 Custom sample request JSON

Example: elysia protocol preview

Details: Render the draft's outbound request offline using a sample Maheshvara request (method/path/query/headers/body and credential-injection shape). No upstream request is sent. Use after submitting a draft to inspect its request shape.
````

### protocol read

````text
elysia protocol read --id <protocol-id>
Read a saved protocol's full configuration

Arguments:
  --id                     Protocol ID (including built-in presets)

Example: elysia protocol read --id anthropic-api

Details: Read a saved protocol's complete configuration JSON by ID, including built-in presets, as a reference or editing baseline.
````

### protocol save

````text
elysia protocol save [--update <protocol-id>]
Save the current draft as a protocol (permission-controlled)

Arguments:
  --update                 Explicitly update an existing protocol; target must exist and match the draft ID

Example: elysia protocol save

Details: Save the draft in the protocol registry under server permissions and business policies; writes take effect immediately. Edit mode must retain the original ID. Other contexts create by default and reject an existing ID. To update, explicitly pass --update <protocol-id>; the target must exist and match the draft ID. Prefer saving after successful live testing.
````

### protocol test

````text
elysia protocol test [--base-url <URL>] [--api-key <key>] [--stream] [--sample '<sample request JSON>']
Send a test request to a real upstream (permission-controlled)

Arguments:
  --base-url               User-provided upstream baseUrl (defaults to one recorded in the current CLI context)
  --api-key                User-provided API key (defaults to one recorded in the current CLI context)
  --stream                 Test streaming (SSE)
  --sample                 Custom sample request JSON

Example: elysia protocol test --base-url https://api.example.com --api-key sk-xxx

Details: Render the draft and send it to a real upstream under server permissions and business policies. Returns HTTP status, raw content and mapped results; stream=true samples SSE events and decoded results. Pass user-provided baseUrl/API key as arguments. Reuse only values successfully recorded by this command or protocol models in the current CLI context; other commands' credentials are not reused.
````

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
