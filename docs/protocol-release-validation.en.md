# Release validation and performance experiments

[中文](protocol-release-validation.md)

> C24–C27 results are in the [2026-10-04 follow-up](cache-validation-2026-10-04.en.md). This report retains the original C19–C23 versions and scope; its Gemini counter interpretation is corrected below.

These experiments start from `5d43b83` on the local `feat/protocol-v2-gateway` branch. Nothing was pushed, published or deployed. Real requests used only `https://moyuu.cc` and `gpt-6.1-sol`; deterministic load tests used loopback servers. Results do not identify the site's underlying provider or establish behavior for another account or model.

## Race and shutdown

C19 (`5754293`) added reproducible verification reports and a portable official LLVM-MinGW `20260922` toolchain. The archive SHA-256 is `e3ad77d117a4bea19a7a3b333341824d79a5a371004a10e25b8504e7b3047666`; the official release is <https://github.com/mstorsjo/llvm-mingw/releases/tag/20260922>. Go is `1.26.5 windows/amd64`; CC, CGO_ENABLED and PATH changed only in test subprocesses.

The initial full run hit Go's ten-minute timeout. With a 60-minute test timeout, `go test -race ./... -count=1` passed, including Agent (1,403.890 seconds). No DATA RACE was reported. Single-core stress then exposed a WebSocket shutdown deadline failure: `CloseNow` could wait behind an active graceful handshake without a reader. The adapter now owns the upgraded underlying connection and closes that transport before waiting for forced WebSocket cleanup. Graceful close codes, Gin's immediate header behavior and the existing secure outbound transport remain intact.

A deterministic test waits until a close frame has actually been written before forcing shutdown. It fails on the isolated old snapshot and passes with the fix. Stress passed at GOMAXPROCS 1, 2 and 12, twenty repeats each, shuffle seed `1208900205`. C19's full race run predates the fix; its subsequent evidence consists of targeted race and full ordinary tests. Final optimized-engine evidence must be recorded separately, not inferred from those runs. The CI race command now includes Agent and uploads evidence; remote CI was not triggered.

## Real protocols and cache accounting

C20 (`49bc7b2`) records linked outbound request, raw upstream response, downstream response and isolated SQLite usage evidence. A separate JSON reader checks usage independently of the codec and distinguishes missing counters from explicit zero. Credentials enter only the test process through hidden input/environment, never source files, arguments, SQLite or reports. Body artifacts contain only synthetic test content and are checked for credential leakage before writing; authorization headers are not captured.

There were **254 of the allowed 256 paid calls**, including probes and unsuccessful early harness runs. Requests were serial, with no automatic generation retry or redirect, and a default 128-token requested output limit. Provider compliance with that limit is assessed from actual usage. The shared budget in the parent `.cache` directory was never reset.

| Target | JSON / SSE text | JSON / SSE two-round function calls | Observed cache reads |
| --- | --- | --- | --- |
| Chat `/v1/chat/completions` | Passed | Passed | Direct and gateway: 4,224; downstream and SQLite agree |
| Responses `/v1/responses` | Passed | Passed | Direct and gateway: 4,224; downstream and SQLite agree |
| Anthropic `/v1/messages` | Passed | Passed | Direct and gateway: 4,224; see input-total ambiguity below |
| Gemini `/v1beta/models/gpt-6.1-sol:generateContent` | Passed | Follow-up HTTP 400; streaming tool contract failure | Native zero conflicts with billing-extension reads of 4,224 / 8,320 / 16,512; not evidence of no cache read |

Gemini's largest actual input was 16,709/16,710 tokens; estimated prompt length was not substituted for observed counts. Chat and Responses accepted cache keys plus `24h` retention, and direct/gateway paths both observed 7,296 cached tokens. Anthropic system and tool breakpoints carried `1h` and both paths observed 7,424. Some repeated requests still returned zero. Field acceptance and nonzero reads do **not** prove retention duration or TTL expiry; expiry experiments were not completed. Gemini explicit cache-resource creation/reference remains unverified.

Anthropic returned `input_tokens=4421` and `cache_read_input_tokens=4224`, while its additional billing metadata declared OpenAI semantics with `prompt_tokens=4421`. Standard Anthropic normalization produces 8,645. That is not proof of the actual prompt total: the site's native and billing counters need clarification. No global Anthropic semantic change was made. If a different accounting contract is confirmed, it needs an explicit site-specific mapping. The cache-read count itself remained consistent.

The 4×4 real conversion matrix is **not entirely passing**. Provider-specific native extensions without a declared equivalent are rejected explicitly. All four arbitrary-ID preset copies passed JSON/SSE text. A new declarative envelope protocol passed offline text/usage combinations; actual non-streaming Responses, Anthropic and Gemini forwarding passed. Chat returned content beyond the declared profile, and all four streaming targets returned unmapped extensions; those positive verification gaps remain.

A Chat copy explicitly declaring cache breakpoints sent the Anthropic system breakpoint with `ttl=1h` unchanged and observed 8,320 upstream cached tokens. Downstream conversion rejected unmapped `/wire:claude` response fields. That proves request preservation and an upstream read, not full end-to-end success.

Two confirmed production defects were fixed: nullable Chat `tool_calls` now denotes no semantic calls while retaining native null, and Gemini `finishReason:null` no longer terminates a stream before STOP and the usage tail. Both regressions fail on the previous snapshot and pass after the fix. Non-array tool lists remain invalid. Compiler verification version advanced to `2.0.0-dev.10`.

A separate Responses encrypted-reasoning failure came from missing account scope in the test inspector, not production forwarding. Correcting the inspector made the real policy experiment pass. Gemini's non-streaming tool follow-up sends matching call/result IDs, but the site returns “No tool output found for function call call_1.” Streaming frames contain empty tool names and `args.arguments` fragments without a declared association contract. These remain failures; the gateway does not guess their association or replace malformed arguments with `{}`.

Commands: `node scripts/verify-protocol.mjs live --preflight`, or `live --suite=cache|matrix|extended|custom|breakpoint|diagnostics`. `ELYSIA_LIVE_TARGET` filters targets. Explicitly reusing `ELYSIA_LIVE_PREFLIGHT_REPORT` requires matching origin, model, compiler, definition hash and successful JSON/SSE evidence; its path and hash are recorded. The report uses passed, failed, inconclusive and not_run; HTTP 200 alone is insufficient.

Key evidence under the repository's parent `.cache`:

- `protocol-live-1791030580289`: four-target preflight and cache ladder.
- `protocol-live-1791030876155`: real ingress/target matrix.
- `protocol-live-1791031683055`: corrected tool prompts, arbitrary-ID copies and policies.
- `protocol-live-1791032315909`: scoped Responses policy checks.
- `protocol-live-1791032521055`: Gemini reversed cache execution order.
- `protocol-live-1791032813311`: declarative ingress after fixture coverage corrections.
- `protocol-live-1791032933161`: declared Chat breakpoint to Anthropic.
- `c20-final-checks`: failing-before regressions, passing complete backend tests and vet.

## Performance baseline

C21 (`bf89ab6`) measures isolated source snapshots with hashed test-only overlays. `performance --legacy-c01 --revision=d77aac7` runs the equivalent old cache workload; `performance --revision=5d43b83` runs C18. Plain `performance` measures the current tree; `--micro-only` omits HTTP load. Each microbenchmark uses ten unprofiled 300ms samples. Comparisons use a seeded 10,000-resample bootstrap interval for the ratio of medians.

On the same Ryzen 5 7600X / Go 1.26.5 Windows amd64 host, C01 cache conversion measured **17.871 µs, 17,997 B, 322 allocations**; C18 measured **82.588 µs, 53,856 B, 752 allocations**. The CPU ratio was 4.62 (95% interval 4.01–4.82), an absolute difference of **64.717 µs**. This replaces the earlier approximate three-times comparison and does not imply a 4.62× end-to-end slowdown.

The deterministic HTTP harness covers four paths, five workloads, immediate/10ms delayed providers and concurrency 1/8/32: 120 combinations with three repeats of 128 measured requests, plus 32 warmup requests per combination. All **46,080 C18 measured requests passed**. Measurement includes HTTP, authentication, routing, conversion and SQLite settlement. Process CPU includes the load generator and loopback upstream, not only the gateway. First-frame time is the complete first SSE frame, or the complete body for non-streaming responses. GC and retained heap deltas are recorded independently.

For context, C18 same-protocol short text at concurrency 1 had a 2.001ms P95. Chat→Anthropic with a 10ms delayed upstream had a 12.508ms P95. At concurrency 8, Chat→Responses with 256KiB history used 29.175ms of whole-harness CPU per request and had a 37.230ms P95. These are measured local workloads, not a production capacity claim. Three load repeats screen for effects; repeatable deterioration greater than 5% requires follow-up.

Baseline evidence: `protocol-performance-1791033212892` (C01), `protocol-performance-1791033219174` (C18), and `c21-c01-c18.json` (comparison). Final candidate and release-gate results are recorded after their checks finish.

## Structural resource metering

The candidate meters the fixed semantic roots directly, avoiding a complete JSON copy on successful resource checks. Read-only type plans come from the actual JSON field tags. It preserves omission, null, empty containers, exact number spelling, compacting, HTML escaping, Unicode separators and invalid UTF-8 behavior. Unsupported types, cycles, encoding errors and exceeded limits still use the original encoder/checker, preserving error precedence and messages. Capability, provenance, scope, native preservation and call-association checks remain intact. No per-request data or protocol-ID/address cache was added.

Tests populate every exported model field and compare exact byte/node/depth boundaries at -1/0/+1. A 60-second differential fuzz run completed 242,006 executions without a mismatch. CI includes the new fuzz target; no remote CI was run. The candidate compiler is `2.0.0-dev.11`, so preset and active-definition evidence is revalidated through the existing startup mechanism.

Ten unprofiled micro samples measured cache conversion at **57.639 µs**, versus C18's 82.588 µs: **30.2% less CPU and 32.4% fewer allocated bytes**, with a CPU ratio interval of 0.657–0.722. The candidate is still about 3.23× C01, an absolute difference of 39.8 µs. Chat→Anthropic 256KiB conversion improved from 15.009 to 11.011ms and allocated 44.7% fewer bytes. The 64KiB stream decode improved from 2.926 to 2.552ms. Some declarative CPU results remain inconclusive; allocation improvements do not automatically prove latency improvements.

The complete candidate HTTP matrix passed all 46,080 measured requests. Chat→Responses 256KiB at concurrency 8 used 20.874ms of whole-harness CPU/request instead of 29.175ms, allocated 9.93MB instead of 16.91MB, and measured 308.3 rather than 240.3 requests/second. Its P95 was 30.041ms rather than 37.230ms.

Six initial CPU/latency regressions exceeded 5%. They were not discarded: a candidate–baseline–baseline–candidate follow-up used five repeats of 1,024 requests per scenario per stage, giving ten samples per version and 122,880 successful measured requests. None of the initial regressions became statistically repeatable. Short-request CPU remains noisy: declarative short text at concurrency 32 had a 1.120 median CPU ratio with a 0.953–1.230 interval. This neither establishes universal improvement nor proves a 5% noninferiority margin. Stable conversion/allocation gains and representative long-input improvements support retaining the optimization, subject to final correctness/race gates.

Evidence: `c22-equivalence`, `protocol-performance-1791033908665`, `c22-comparison.json`, `c22-load-followup`, and `c22-final-checks`. Final race completion is reported separately.

## Additional native control and remaining evidence limits

The final two paid calls tested Gemini's original non-streaming tool history directly. The site's original content and absence of a call ID were preserved, without rebuilding history through the gateway. A native function response with the original name and `{"value":7}` still received the same HTTP 400 / `call_1` association error. Evidence is in `protocol-live-1791034671676`. This reproduces the failure outside gateway history conversion; adding a generic ID fallback is not justified.

The total is now **256/256 calls**. Real requests stopped and the temporary credential was cleared from session memory. The main real matrix used dev.10; this final native control used dev.11. Differential equivalence evidence is not a claim that the complete live matrix was rerun on dev.11.

Through C23, no nonzero live creation was observed. Anthropic reported zero. Later auditing established that Chat/Responses native cache_write_tokens:0 had been omitted from semantic statistics; semantic absence must not be described as wire-field absence. Nonzero creation fidelity remains supported by offline tests. Anthropic's conflicting input-total conventions also affect the hit-rate denominator; matching cache-read counts do not validate that denominator. Gemini explicit resources, actual TTL expiry, unsupported cross-protocol extensions and a production SLO remain open verification boundaries.

The paired nonzero live cache-read evidence comes from non-streaming requests. The real SSE cases did not produce a nonzero cache read. Nonzero usage-tail fidelity has offline regression evidence, not an equivalent live nonzero SSE control in this run.

Final race evidence is now complete: `protocol-race-1791034693583/report.json` is **passed**. The entire backend including Agent passed in 1,356.490 seconds, with no DATA RACE. GOMAXPROCS 1, 2 and 12 each passed twenty repetitions with shuffle seed `1208900205`, taking 382.281, 309.132 and 286.321 seconds respectively. The extracted native-history control helper separately passed twenty race repetitions and vet (`c23-control-checks`). Full ordinary backend tests, vet and Node verification tests also passed (`c22-final-checks`).

Focused performance reruns can use `ELYSIA_LOAD_SCENARIOS`, a comma-separated list of `path/workload/delayed/concurrency` such as `responses-api/short/false/1`. An empty matching selection fails. The report records scenario selection, requests and repeats. Performance tests remain entirely local and never read provider credentials.

The final metric audit identified four additional initial GC-pause regressions. They received a separate candidate–baseline–baseline–candidate follow-up: five repeats of 256 requests per scenario per stage, **20,480 successful measured requests**, and ten samples per version. GC-pause ratios were 0.878, 0.845, 0.958 and 0.831; every 95% interval crossed 1. None reproduced a significant regression, and none establishes a definite GC benefit. Other representative metrics had no significant regression. All original anomalies remain in the evidence (`c23-gc-followup`). For example, delayed Responses 64KiB streaming accumulated a median 33.786ms versus 28.082ms of GC pauses per 256 requests, not per-request latency.

## C23 acceptance boundary (historical)

The local validation and fixes are complete, but **not every release gate is passing**. Full race and all three stress settings passed; no DATA RACE was detected, and a shutdown lifecycle defect was fixed. Four real endpoints support JSON/SSE text; Gemini tools still fail in a native direct control. Non-streaming cache reads were observed and preserved for Chat, Responses and Anthropic. Gemini native counters were zero, but the same responses contained nonzero billing-extension reads (4,224 / 8,320 / 16,512). This is a conflicting counter contract, not proof that no cache read occurred or explicit resources are unsupported. Anthropic's input denominator, nonzero live creation, nonzero live SSE reads, explicit resources and actual TTL expiry remain unverified.

Retained metering optimization improves cache-conversion CPU by 30.2% and allocated bytes by 32.4%, with improvements in representative long-history HTTP cases. Every initially significant CPU, latency or GC regression received follow-up. Short-request and GC uncertainty remains; there is no production SLO or capacity certification. Unknown cross-protocol extensions still need explicit mappings. Remote CI was not triggered; C18 build/browser results remain historical evidence, not new dev.11 artifact execution.

Local commits: `5754293` (C19), `49bc7b2` (C20), `bf89ab6` (C21), `8a3bf15` (C22), followed by the C23 commit containing this report and index. Use `git log -5 --oneline` for the complete list. Nothing was pushed, published or deployed; no production database schema changed. Runtime rollback still requires the corresponding old program and compatible database backup; verification reports are engine-version-bound.

The [machine-readable evidence index](protocol-release-evidence.json) records results, SHA-256 hashes and paths relative to the repository parent's `.cache`. Raw bodies, logs and benchmark samples remain there, outside source and distributed artifacts. Request IDs and usage snapshots actually read from isolated SQLite are saved in `live.json`; the temporary databases themselves are cleaned up by the test lifecycle, not archived. Only completed, persisted evidence is indexed.
