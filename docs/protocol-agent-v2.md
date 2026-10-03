# Protocol authoring with the native Agent

The editor, CLI and Agent use the same definition compiler, previews, offline
verifier, revision repository and activation gate. The Agent's `elysia_cli`
tool is an adapter to that CLI; it does not have another protocol interpreter.

## Authoring workflow

1. Read the provider documentation and complete request/response/event examples.
2. Run `elysia protocol schema`. Fetch individual sections with `--section
   definition|semantic|binding|directions|capabilities|operations|events|diagnostics|modules`.
   Use `--type Request` (or another named type) to avoid truncating large schemas.
3. Write a complete `schemaVersion: 2` definition using `protocol draft '<JSON>'`.
4. Run `protocol validate`, `protocol preview --direction <direction> --sample
   '<JSON>'`, and `protocol verify`. Session/task previews use `--mode`,
   `--sample-id`, `--operation`, `--kind` and `--purpose`, as in the editor.
5. Repair diagnostics without removing capabilities that the user needs. New
   mechanisms beyond installed engine support must be reported as unsupported.
6. `protocol save [--expected <draft-hash>]` stores the draft and offline report.
   Saving never activates it. Invalid verification leaves a repairable draft.
7. `protocol activate --id <id> --hash <revision-hash> --expected <active-hash>`
   enables that verified revision. Omit `--expected` only for first activation.

Draft hashes guard edits; compiled revision hashes identify executable versions.
They are not interchangeable. Read the revision hash from the save/verify result.
`protocol read`, `diff`, `diagnose` and `rollback` use the same service as the UI.
Rollback passes the current compiler's verification gate again.

## Target probes and model calls

`protocol test --operation <id> --sample '<semantic Request JSON>' --base-url
<URL> [--api-key <key>]` requires passing offline evidence and an explicit model.
It uses the same typed adapters, secure outbound client, capability checks and
event replay as gateway forwarding. HTTP JSON, SSE and NDJSON generation probes
are implemented; session/task probes and v2 model-discovery probes explicitly
report their missing workflow instead of running generation mappings.

The editor's target probe uses the same endpoint/service. Target reports are
persisted separately from offline reports and never authorize activation. They
record the definition/compiler/sample hashes, opaque target/account identities,
model and contract outcome. They do not store prompts, outputs or credentials.
Success means the observed exchange passed the declared contract, not that all
capabilities, models, provider cache hits or repeated requests were tested.

Agent model calls with a v2 binding pin its revision, require function-tool
support from both model and binding, and check actual requests/responses before
consumption. They use declared HTTP generation operations. Streaming results
use a bounded collector over the shared event replay; no generation is replayed
after an error. The gateway does not execute client business tools.

The Agent's save/activate/rollback commands retain the existing write gate;
target probes retain the live-test gate. Plan mode, approval and remote access
policies remain in force. Edit sessions preserve the protocol ID. Exact draft
JSON is carried as a string to the editor so large numbers are not rounded.

## C14 evidence and remaining migration work

Tests cover authoring/revisions/CAS, shared diagnostics, HTTP 200 with invalid
response shape, separate target evidence, real local custom-protocol model calls,
tool capability rejection before sending, and a simulated Agent that diagnoses
an incorrect path, repairs it and enables the definition. Chromium uses a real
isolated Go/SQLite server to open an Agent draft in the editor without rounding
numbers and to verify the editor's creation/activation/rollback workflow.

No credentialed provider validation was run. C15/C16 have completed the
migration and removed the old Agent conversion and authoring paths. The native
Agent consumes the ordered protocol contract and a declared Agent parameter
policy. Current evidence and limitations are in
[system validation](protocol-system-validation.md).
