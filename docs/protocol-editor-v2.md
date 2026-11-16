# Protocol editor

The protocol designer opens the versioned protocol service. New definitions
are drafts. Saving does not activate them. Define the supported directions,
operations and capabilities; add expected request/response/event examples; save;
run offline verification; then activate the exact verified revision.

Basic forms edit `/spec` identity and `/mapping` capabilities. The two source
tabs edit and import/export `spec.json` and `mapping.json`; full JSON contains
the complete schemaVersion 4 package. Spec declares the external contract and
operations. Mapping independently maps that protocol to and from the central
Maheshvara (大自在天) core2 contract. There are no module choices or direct
provider-pair converters. Unknown configuration keys are rejected by the server.

The source text is the single editable document. Form changes replace only the
selected field, preserving large integer literals, explicit null/false/zero,
arrays and extension objects. Incomplete JSON sections survive tab changes and
block saving/activation until corrected. Draft writes use the previous hash;
conflicts never overwrite a newer editor/Agent revision silently.

Expression fields accept both full objects and compact optional reads, for example
`"$input/model"` or `"$binding/message/content"`. The live schema exposes their
`oneOf` and the read operation help lists the supported sources. Required reads
retain object form. Literal, enum and sample strings are data. Source tabs,
import/export and unrelated form edits preserve the authored compact form;
server compilation retains the existing scope, pointer and budget checks.
Changing authoring syntax creates a different revision that needs verification.

Conversion preview invokes the same typed runtime as gateway forwarding. It
supports request/response mappings, event sequences, complete mixed session
traces and task receipt/control mappings. Preview responses preserve original
JSON numeric spelling in the browser. Protocol combination verification accepts
another complete definition. Diagnostics link to configuration paths or sample
evidence; they never automatically remove the unsupported capability.

The capability table and sample checks show offline evidence separately from
real upstream verification. Editing invalidates the visible activation result.
Version history can compare against the active version, load a revision as a
draft or reverify and roll back. The service enforces these gates even if a
client calls the API directly. A real upstream validation record is not inferred
from offline examples or an HTTP 200 response.

Retired protocol formats cannot be saved or executed. Current packages must be
verified through this editor before activation. Use
`node scripts/test-protocol-e2e.mjs` for an isolated backend/browser test.

Validation includes TypeScript checks, scoped ESLint and relevant backend tests.
Chromium against an isolated real Go server/SQLite store creates a standalone
protocol, preserves long integers through form/JSON/save, retains invalid edits,
previews, verifies, blocks unsupported tool declarations, activates, compares
versions and rolls back. The test also exercises direct service reads to check
persisted numeric literals. It uses a local fixture, not an external provider.

Run the real-service browser test with an isolated backend and:

```powershell
$env:ELYSIA_DEV_PROXY='http://127.0.0.1:18765'
$env:PROTOCOL_E2E_URL='http://127.0.0.1:18765'
$env:PROTOCOL_E2E_TOKEN='<isolated-panel-token>'
# From packages/webui
node node_modules/@playwright/test/cli.js test tests/protocol-v2.spec.ts --project=chromium --workers=1
```

Without `PROTOCOL_E2E_URL`, the integration case explicitly skips. The independent
source-span regression still runs. CI must provide an isolated backend when
using this case as an integration gate.
