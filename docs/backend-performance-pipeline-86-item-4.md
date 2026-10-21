# Pipeline refactor #86 — item 4: request/attempt/response lifetimes

Depends on #89 (`917041d`). Preparation retains alternative candidates only while retry is possible. Each attempt transfers out of the plan; the final attempt releases preparation after final outbound validation, and a successful upstream status releases remaining candidates before response processing. Delivery has an explicit small `gatewayResponsePlan` containing codec/framing, client alias and output policy, not request history. Current candidate/probe references are cleared. Dispatch snapshots only the final outbound model needed for Chat response attribution, so that feature does not keep a whole outbound body alive. Response recovery context is refreshed after continuation capture.

The request/transport/logging layers can still retain required buffers; this is not a zero-copy or exact process-memory bound. Task submission and WebSocket lanes retain their existing owners. Retriable upstream status failures preserve unused candidate preparations until the next attempt; successful response conversion failures do not resubmit generation.

## Targeted acceptance

`GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off -race -p=1 ./protocol/builtin ./server -run '^Test(ChatResponseModel|GatewayPreparationHandoff|GatewayChatMissingResponseModel|GatewayChatInvalidResponseModelIsNotAliased|ChatCompletionsFailoverToHealthyModel|ChatCompletionsAllModelsFail|ChatCompletionsMaxRetriesZero|GatewayContinuationPersistenceAndIsolation|GatewayResponsesEncryptedReasoningContinuation|GatewayReservationUsesExecutableRetryWindow|PreparationReleasedBeforeUpstream|GatewaySSEAndNDJSONBridge|EncodingFailureKeepsCandidateAndExactField)' -count=1 -timeout=6m` passed after correction.

The first targeted run caught stale recovery evidence: snapshotting the whole response conversion context at dispatch preceded continuation capture. The implementation now snapshots only outbound model provenance and refreshes recovery state at the original response boundary. Both the failing log and passing rerun are preserved. Five selected HTTP cases and ABBA then passed response/content/usage/provider-call checks. No full tests/benchmarks ran.

## Isolated held-stream retention diagnosis

The same new diagnostic test compiles against previous `917041d` using Go's source overlay (no old production changes), and against this stage. ABBA uses isolated client/gateway/provider processes, four active 2 MiB image/tool-history streams, logging off, three batches/process. Each client waits for an actual content delta; the provider remains blocked until cancellation. Two diagnostic GCs execute while all streams remain active. Preparation counters must already be zero. All streams then cancel/drain and usage/payload counters return to zero.

| Median of six held observations/version | Previous | New | Change |
| --- | ---: | ---: | ---: |
| Gateway held heap MiB | 36.021 | 19.915 | -44.7% |
| Gateway held RSS MiB | 113.916 | 95.375 | -16.3% |
| Settled heap MiB | 3.612 | 3.628 | +0.4% |

This demonstrates reduced retained preparation data in a controlled active-stream state; it is **not** formal latency, natural-GC reclamation, normal sampled peak memory, prolonged soak or universal RSS improvement. [Raw experiment and hashes](artifacts/performance-pipeline-86/item-4-held/). The unchanged overlay fixture is `backend/server/gateway_lifetime_profile_test.go`; before build: `go test -tags=sonic -pgo=off -overlay=OVERLAY_JSON -c -o BEFORE_BINARY ./server` from a detached item-3 backend worktree. Runner: `node .../item-4-held/runner.mjs BEFORE_BINARY AFTER_BINARY NEW_EMPTY_DIRECTORY`.

Normal-path timing remains mixed and has opposite directions across primary/ABBA datasets; all results follow. Do not combine the held diagnostic's forced-GC numbers with normal HTTP timings.

## Incremental selected HTTP results: item-3 → item-4

| Case | KiB/request | allocations/request | CPU ms/request | P50 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 141.5 → 141.1 | 2639 → 2652 | 5.18 → 6.98 | 2.42 → 5.72 | 15.78 → 15.60 | 67.49 → 67.23 |
| candidates4 | 255.7 → 249.8 | 5105 → 5107 | 10.95 → 11.67 | 8.86 → 8.84 | 14.64 → 15.70 | 68.57 → 69.46 |
| burst4 | 145.6 → 142.2 | 2646 → 2640 | 3.74 → 5.32 | 3.32 → 5.87 | 13.32 → 16.11 | 67.88 → 69.35 |
| stream256-cross | 20417.4 → 20533.3 | 133037 → 133021 | 176.04 → 132.02 | 119.48 → 76.13 | 10.59 → 10.71 | 66.86 → 67.24 |
| image2 | 21677.3 → 21750.3 | 4825 → 4823 | 102.82 → 106.34 | 83.82 → 90.85 | 35.24 → 33.41 | 92.85 → 93.72 |

32 requests × three repeats (image: four), four warm-ups, one-second idle: 396 counted requests/version. Separate client/gateway/provider processes; response/content/usage/call counts checked. Go 1.27.2, Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. CPU includes usage settlement; latency does not. Per-repeat medians and sampled peak maxima; no significance or universal latency/RSS claim. No forced GC or profiler in formal HTTP timing.

## Hash-verified ABBA confirmation

64 requests × three repeats × two processes/version (image stays four).

| Case | CPU ms/request | P50 ms | B/request change |
| --- | ---: | ---: | ---: |
| chat-short | 8.23 → 6.73 | 5.84 → 4.72 | -2.3% |
| burst4 | 4.62 → 3.69 | 4.73 → 3.89 | -0.9% |
| stream256-cross | 185.39 → 184.54 | 143.68 → 142.10 | +0.1% |
| image2 | 130.98 → 118.19 | 112.55 → 94.17 | -0.2% |
