# SDK replay audit

Use Node.js 24 or later (JSON.parse source context preserves large integer arguments). Install the pinned clients with `npm ci --prefix scripts/sdk-audit` from the repository root. Root workspace installation remains `npm ci`.

Capture complete JSON/SSE responses from the actual gateway talking to the isolated mock providers:

```powershell
$env:ELYSIA_AUDIT_CAPTURE = 'D:\temp\elysia-sdk-capture'
Set-Location backend
go test ./server -run '^(TestGatewayAuditTextMatrix|TestGatewayAuditToolRoundTrips|TestGatewayAuditParallelToolRoundTrips)$' -count=1
Set-Location ..
node scripts/sdk-audit/replay.mjs $env:ELYSIA_AUDIT_CAPTURE
```

Use an empty capture directory for each audit. Text, single-tool and parallel-tool producers select all 16 ingress/upstream pairs. Both JSON/SSE and both tool rounds are explicit: 32 text captures plus 64 single-tool and 64 parallel-tool captures, totaling 160. `coverage` lists every present and missing capture and reports `completeCurrent` and `completeCartesian` only when the whole set exists. Historical 84-capture audits remain incomplete under this contract; startup-prelude captures are listed separately. Partial replay is allowed but never marked complete by its check count.

The replay checks visible text in order, tool IDs/names/arguments and successful terminal states in official OpenAI, Anthropic and Google clients and pinned Vercel v2/v4 adapters. Tool expectations come from the independent gateway fixture producer, not the captured answer. Metadata cannot satisfy a text/tool assertion. JSON argument strings preserve large integers; if an SDK has already rounded a numeric object member, the audit reports the loss. It does not change the expected integer or repair the returned object. Every client failure is retained in the result and causes a nonzero exit status.

Run pure semantic assertion tests without starting a gateway:

```powershell
node --test scripts/sdk-audit/observe.test.mjs
```

The replay server returns the exact captured gateway bytes. It must not add missing fields or otherwise repair fixtures. Streams are consumed fully and checked for error and unexpected events. Success-response captures and deliberate error-contract captures have different schemas and must use separate directories. This is a local wire-contract audit, not a live provider test or a Cherry Studio acceptance result. Signature acceptance and retention by the actual client require separate live verification. Historical capture replay does not establish current gateway revision behavior.
