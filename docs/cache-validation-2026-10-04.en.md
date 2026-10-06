# C24–C27: focused cache validation

[中文](cache-validation-2026-10-04.md) · [Contract and custom mapping guide](cache-usage-contracts.en.md) · [Evidence index](protocol-release-evidence.json)

The shared creation-counter mapping is fixed. Real nonzero SSE reads were observed for Chat, Responses and Anthropic, together with nonzero Anthropic creation. **This does not close every cache gate.** The site rejected Gemini resource creation; Gemini gateway cohorts did not return a read counter; Anthropic `1h` cohorts recreated their cache after 55 minutes on both direct and gateway routes.

## Scope and versions

The origin was `https://moyuu.cc`. Chat/Responses used `gpt-6.1-sol`, Anthropic `claude-haiku-4-5`, and Gemini `gemini-3-flash-preview`, each with its designated credential. The new ledger used **71/96 calls**: 51 in the main experiment and 20 in the final-revision follow-up. The previous **256/256** ledger was not reset. Calls ran from 2026-10-03 15:59:03 UTC until approximately 17:07:50, within the two-hour limit. Four cleanup slots remained reserved; no remote resource was created.

Requests were serial, with a fixed source, no generation retries or model rotation, and a requested 128-token output limit. Secrets entered only child-process memory through hidden input/environment, never command arguments, files, SQLite or reports. Bodies contain synthetic content and remain outside the repository in `.cache`. No production configuration, schema or default capture behavior changed; nothing was pushed, deployed or published.

The main process remained on `5333765` / compiler **dev.12** throughout its TTL experiment. The follow-up's Node report was created on dev.13, but its actual Go step and `live.json` identify **dev.14**, including Agent fix `64d9d9b`. Per-step source hashes are authoritative. The hour-long TTL experiment was not repeated on dev.14.

## Confirmed defects

| Defect | Change | Commit |
| --- | --- | --- |
| Nested Chat/Responses `cache_write_tokens` was omitted from semantic usage, including zero | Shared parsing and standard nested encoding; preserve missing/zero; reject conflicts with legacy aliases and invalid counts; do not add cached tokens again to inclusive input | `fb87ccf` |
| New nested encoding could leave a stale native top-level alias after a semantic edit/deletion | Reconcile recognized aliases; unchanged compatible native responses retain their original shape | `4dfa0d2` |
| Native SSE replay checked only the encoder and could bypass a custom usage decoder | Require compatible decoder and encoder hashes, including referenced expression implementations; diagnose incompatible extensions instead of deleting them | `d70d9e0` |
| Full race exposed Agent EOF before releasing the session guard | Finish resource cleanup and release turn ownership before closing the event channel; a gated regression fails deterministically before the fix | `64d9d9b` |

Protocol fixes advanced verification through dev.12, dev.13 and dev.14, using existing startup reverification without overwriting edited definitions. The Agent lifecycle fix does not alter compiler semantics. Before/after failures remain available; no sleeps, weakened assertions or reduced concurrency were used to hide them.

Two inspector corrections are separate from production defects: raw usage components are merged before normalization, and Gemini response inspection now derives signature scope from the persisted model account. The original Gemini HTTP response and SQLite counters were already correct. A synthetic signed fixture and offline replay of the actual response passed after the inspector fix; subsequent real JSON/SSE gateway forwarding passed too.

## Cache-intent and usage-detail validation (C28–C29)

The compiler enforces the following before encoding; a violation fails with a specific path instead of passing silently. Together with the [usage-contract](cache-usage-contracts.en.md) bucket omission they are the C28–C29 behavior change, and the compiler version moves to `2.0.0-dev.18`.

- **Reserved keys**: a Chat/Responses target's `Details` may not occupy `input.cached_tokens` or `input.cache_write_tokens` — those names belong to the canonical counters (read and creation). A mapping that wrote the detail form would overwrite the count derived from the counters and make the wire disagree with the semantic usage; it is now rejected with its path.
- **Duplicate intents**: several cache intents of the same `Kind` that map to one wire field are rejected. Previously only the `breakpoint` branch refused a duplicate; `key`/`retention`/`resource` let a later intent silently replace an earlier one, so the request that reached the provider was not the request the client described. All four kinds now report the conflict.
- **Anthropic breakpoint TTL ordering (non-blocking)**: the provider processes markers tools → system → messages and documents that a longer TTL should appear before a shorter one; a breakpoint with no explicit `ttl` counts as the **5m default** for this ordering. Unlike the two rules above, violating the order does **not** fail: `warnBreakpointOrder` emits a `SeverityWarning` and the request still encodes. The provider does not document a failing status for the order and no reference implementation validates it, so rejecting a non-optimal layout with a 4xx would refuse traffic that caches correctly — record the warning, forward anyway, and let the caller optimize.

## Live results

| Target | Site availability and nonzero observations | Accounting | Remaining limits |
| --- | --- | --- | --- |
| Chat | JSON/SSE available; main direct/gateway SSE reads 4,224; dev.14 arbitrary-ID gateway copy also reads 4,224 | Returned counters agree across raw frames, client and SQLite | Live creation explicitly zero; `24h` untested |
| Responses | JSON/SSE available; direct/gateway SSE reads 4,224 on dev.12 and dev.14 | Consistent | Live creation explicitly zero; `24h` untested |
| Anthropic | New Claude model follows standard subtotal semantics; dev.14 cold creation 4,633 and repeated SSE read 4,633; the gateway cold request is itself SSE | Input totals, read, creation and creation buckets retained | `1h` retention not established; old GPT-wrapper billing ambiguity remains |
| Gemini | JSON/SSE available; direct SSE native reads 4,079 / 16,351 with actual input 9,252 / 18,468 | Reported fields preserved; gateway missing read counters remain missing | No nonzero gateway read observed; explicit resource endpoint rejected; no equivalent generation creation counter |

Gemini's initial actual input was 4,645 tokens. The nominal 8K/16K follow-up produced actual 9,252/18,468-token inputs. Direct nonzero readings prove **automatic** cache use under those conditions, not explicit-resource support. Both gateway cohorts lacked `cachedContentTokenCount` in the original upstream response; there is no observed nonzero upstream value lost by conversion. Site routing/cache conditions remain unresolved.

The main experiment retains a 90-second Anthropic header timeout without automatic replay and the corrected Gemini inspector failure; its report remains `failed`. All twenty final-revision forwarding cases passed, but missing Gemini gateway nonzero evidence keeps that report `inconclusive`. Neither HTTP 200 nor forwarding success was relabeled as complete cache validation.

## Input denominator and aggregates

Anthropic TTL warmups returned uncached input 3, creation 4,635 and read 0: normalized input **4,638**. At five-minute retention probes, input remained 4,638, with uncached 3, read 4,635 and creation 0. Dev.14 follow-up prefixes similarly returned uncached 3 plus creation/read 4,633, totaling **4,636**. Client, SQLite and the independent raw-frame checker agree; the new Claude responses did not show the old billing conflict.

The final gateway pairs aggregate to:

| Target | Total input | Total read | Aggregate hit rate |
| --- | ---: | ---: | ---: |
| Chat | 8,850 | 4,224 | 47.7288% |
| Responses | 8,844 | 4,224 | 47.7612% |
| Anthropic | 9,272 | 4,633 | 49.9676% |

The Anthropic aggregate includes a cold creation and a read; it must not be replaced with the single warm request's approximately 99.9353%. Independent auditing deduplicates persisted request IDs and checks every aggregate snapshot. Gemini's aggregate read value of zero reflects the existing summary treatment of unreported usage, **not an observed zero-hit result**. Per-request absence remains intact. No denominator formula or database structure changed.

The old GPT wrapper reported input 4,421, read 4,224 and billing-total input 4,421. Standard normalization gives 8,645 / 48.86%; billing semantics give 4,421 / 95.54%. New Claude evidence cannot choose a contract for the old wrapper. No private billing override was generated for moyuu. Historical Gemini native zero and billing-extension reads of 4,224 / 8,320 / 16,512 are now explicitly documented as conflicting contracts, without rewriting historical raw evidence.

## Timed experiments

Direct and gateway cohorts used independent prefixes. Warm JSON and probe SSE requests differ only in `stream`; system content and cache policy are unchanged.

| TTL / cohort | Actual elapsed, direct / gateway | Read, both routes | Creation, both routes | Interpretation |
| --- | --- | ---: | ---: | --- |
| 5m retention | ~262 / 258 s | 4,635 | 0 | Read observed before the minimum lifetime |
| 5m quiet | ~420 / 420 s | 0 | 4,635 | Read disappeared and recreation was observed |
| 1h retention | ~3,300 / 3,300 s | 0 | 4,635 | Positive one-hour retention not established; direct route also differs from the intended expectation |
| 1h quiet | ~3,900 / 3,900 s | 0 | 4,635 | Recreation observed; does not repair the 55-minute positive gap |

One-hour warmup and recreation buckets were 4,624 one-hour tokens and eleven five-minute tokens. Expiry of the shorter bucket alone cannot explain a total read count of zero. The site's underlying provider/nodes/routing were not fixed, so this is not a proof that an upstream vendor violated its retention contract.

TTL is a read-refreshed **minimum** lifetime. A hit after TTL is not failure. This run did not probe a refreshed cohort beyond its original expiry, so refresh extension remains independently unverified. Older main-process timing labels indicate completed observations only. New classification distinguishes no read before the minimum, recreation afterwards, later reads, and inconclusive counters.

Gemini `POST /v1beta/cachedContents` returned HTTP 404 / `Invalid URL (POST /v1beta/cachedContents)`. The resource test stopped there. No resource identity was available for get/reference/`expireTime`/cleanup; those steps were not executed. Automatic caching is not a substitute for this evidence.

## Offline, race and performance

Tests cover creation presence/zero/nonzero/invalid/conflicting fields, native alias mutation/deletion, JSON/SSE and late usage, arbitrary-ID copies, declarative cache protocols, Gemini scope/unrepresentable-creation diagnostics, budget/resume/no-replay and redaction. The mapping example also covers editor APIs, Agent, preview, save/verify/activate, JSON/SSE, SQLite and retained extensions. No browser E2E is claimed for this round.

Final `64d9d9b` / dev.14 `go test -race ./... -count=1 -timeout=60m` **passed**, including Agent, in **1,290.010 seconds**, with no DATA RACE. Full ordinary backend tests passed in 154.473 seconds, followed by `go vet ./...` and all five Node verification-tool tests. Frontend definitions were unchanged; no browser or frontend type-check execution is claimed for this round.

Agent completion regressions passed twenty race repetitions at each GOMAXPROCS=1, 2 and 12, shuffle seed `20261004`. Cache alias editor/Agent workflows, timing classification and Gemini signed-response inspection passed ten race repetitions (133.024 seconds). These focused tests are distinct from the historical C23 full concurrency stress matrix.

| Fuzz target | Configuration | Actual executions | Result |
| --- | --- | ---: | --- |
| `FuzzCompileDefinition` | 30s, four workers | 961,303 | Passed |
| `FuzzJSONMappingPresence` | Same | 2,012,801 | Passed |
| `FuzzEventReplay` | Same | 1,219,254 | Passed |
| `FuzzTypedLimitEquivalence` | Same | 434,169 | Passed |

Wall times were approximately 31–34 seconds including shutdown; execution counts do not establish exhaustive coverage. Evidence: `c27-agent-final-checks`, `c27-agent-completion-stress`, `c27-supplemental-checks`, and `c27-scope-replay`.

The earlier dev.14 full race report remains failed in `c27-dev14-checks`: `TestRunTurn_ConcurrencyGuard` was its only failure, with no DATA RACE. The deterministic regression established the defer-order defect. Old dev.12/dev.13 passes were not substituted for final verification.

Baseline `fc31fea` / dev.11 and candidate `64d9d9b` / dev.14 were measured on the same Ryzen 5 7600X, Go 1.26.5, Windows amd64 host, after live traffic and other checks ended. Unprofiled sampling used ten 300ms groups for nineteen benchmarks: cache conversion, 64KiB stream decoding, four request paths × four workloads, and decode/mutate/encode of a legacy creation alias.

Thirteen initial CPU comparisons had a 95% ratio interval entirely above 1.05. All nineteen workloads were therefore repeated in candidate–baseline–baseline–candidate order, five groups per stage, giving ten samples per version again. Comparison uses 10,000 seeded bootstrap median ratios (`20261003`).

| Workload | Baseline → dev.14 median | CPU ratio / 95% interval | Allocated bytes |
| --- | --- | --- | --- |
| Cache request conversion | 55.791 → 58.635 µs | 1.051 / 0.990–1.092 | 36,409 → 36,408.5 B |
| 64KiB stream decode | 2.262 → 2.345 ms | 1.037 / 0.982–1.086 | 2,077,174 → 2,081,269.5 B |
| Chat → Responses 256KiB | 10.258 → 10.429 ms | 1.017 / 1.001–1.071 | 8,042,600 → 8,040,330.5 B |
| Legacy cache-alias mutation | 91.767 → 104.954 µs | **1.144 / 1.088–1.211** | **66,020 → 77,878 B** |

The first eighteen workloads did not reproduce a statistically supported regression exceeding 5%; this does not prove strict 5% noninferiority. Legacy alias mutation repeatedly costs **14.4% more CPU, 13.187 µs, 11,858 additional bytes (~18.0%), and 208 allocations (1,101 → 1,309)**. It specifically measures modifying a response carrying the old native alias, not every request. No HTTP load matrix was rerun, and these microbenchmarks do not establish production throughput.

The correctness fix adds standard nested fields and reconciles native aliases so stale counters cannot survive edits. Its measured overhead is retained and disclosed. A separate three-second profile attributes cumulative 7.82% CPU / 10.32% allocations to `encodeResponseUsage`, and 22.53% CPU / 44.61% allocations to native reconciliation. Cumulative percentages overlap and are not predicted savings; profiled timing is excluded from comparisons.

The follow-up optimization design is to parse compatible native usage presence/aliases once **within one response-encoding call**, share that immutable view between before/after construction, and reduce repeated object decoding/intermediate encoding. Preserve provenance, missing/zero, conflict, deletion and unknown-extension rules; do not introduce cross-request caches or retain borrowed JSON views in long-lived state. Require mutation/deletion differential cases, at least ten swapped-order samples, representative HTTP load and full race before retaining a candidate. This optimization is not implemented or claimed as a proven gain here.

Evidence: `c27-performance-baseline`, `c27-performance-candidate`, `c27-performance-comparison.json`, and `c27-performance-followup`. Initial anomalies and follow-ups are both retained. **The alias-mutation overhead remains a known performance regression; not all performance release gates are cleared.**

## Delivery and open evidence

Local commits: `5fa69b9` (C24 evidence/budget), `fb87ccf` (C25 creation), `5333765` (C26 timing/resources), followed by `4dfa0d2` (native aliases), `10b090e` (inspector scope), `b5277d4` (bounded follow-up), `d70d9e0` (SSE decoder contract), and `64d9d9b` (Agent completion). C27 contains this report, supplemental tests, mapping guidance and indexed evidence.

Still unverified: Gemini explicit-resource lifecycle and nonzero gateway SSE reads; nonzero Chat/Responses creation and 24-hour behavior; site one-hour retention and read-refresh extension; the old GPT-wrapper denominator. Existing unknown cross-protocol extensions, Gemini tools, remote CI, new six-platform artifacts and a production capacity SLO are not automatically cleared by cache tests.

The evidence index retains historical artifacts and adds SHA-256 hashes. Raw bodies remain outside source. SQLite evidence consists of actually read request IDs and usage snapshots, not archived temporary databases. Runtime rollback requires the corresponding program and compatible database backup; version-bound reports do not certify another engine.
