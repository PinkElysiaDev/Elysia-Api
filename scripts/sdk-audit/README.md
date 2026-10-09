# SDK replay audit

Install the pinned clients with `npm ci --prefix scripts/sdk-audit` from the repository root. Root workspace installation remains `npm ci`.

Capture complete JSON/SSE responses from the actual gateway talking to the isolated mock providers:

```powershell
$env:ELYSIA_AUDIT_CAPTURE = 'D:\temp\elysia-sdk-capture'
Set-Location backend
go test ./server -run 'TestGatewayAudit' -count=1
Set-Location ..
node scripts/sdk-audit/replay.mjs $env:ELYSIA_AUDIT_CAPTURE
```

Use an empty capture directory for each audit. The matrix produces 160 files: text, single-tool first/second rounds and mixed text with parallel same-name tools first/second rounds, for all 16 protocol pairs and both JSON/SSE. The replay performs 320 checks across the official OpenAI, Anthropic and Google clients and pinned Vercel adapters, including two Anthropic versions.

The replay server returns the exact captured gateway bytes. It must not add missing fields or otherwise repair fixtures. Streams are consumed fully and checked for hidden error events. This is a local wire-contract audit, not a live provider test or a Cherry Studio acceptance result. Signature acceptance and retention by the actual client require separate live verification.
