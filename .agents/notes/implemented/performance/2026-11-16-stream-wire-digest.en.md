# Agent Note: Shared stream wire digests

Status: implemented

## Problem

Sequenced stream events repeatedly encode a semantic Event, decode it into a generic canonical representation, encode it again, and hash it. Source accounting and target projection repeat this work independently. HTTP framing also copies and recompacts already encoded JSON and resolves the same event-name pointer per frame.

## Decision

`backend/protocol/event_frame.go` computes SHA-256 from the existing wire Value once when a decoded frame contains a sequence. The frame shares that digest between source and target replay through `EventFrame.ConsumeEvent`; `SequenceTracker` accepts the precomputed digest without re-encoding the Event. Compound members bind the frame digest to their decoded ordinal, so a reused sequence cannot suppress another member. The source does not project an entirely deduplicated frame again.

Events without a usable frame, including buffered or synthetic target events and a source event that projects into several target events, retain canonical semantic deduplication. Sequence validation, bounded history, resource accounting, and native replay authorization remain intact. No Spec/Mapping package, provider-specific conversion branch, assertion, sample, or limit changes.

`backend/protocol/transport_http.go` constructs one frame writer per gateway output stream, resolving its event-name pointer once. SSE passes `Value.JSONString()` directly to the existing eventstream writer, which safely frames multiline JSON. NDJSON uses `io.WriteString` rather than fmt but retains compaction: `ParseValue` and native replay preserve whitespace, and existing `TestHTTPFramingPreservesFragmentsAndBounds` explicitly requires a multiline read frame to become one NDJSON line. The compact-output invariant holds for encoded frames, not every accepted Value. Removing NDJSON compaction would violate that existing assertion; no production compactness fallback is added.

`digestSemanticEvents` remains a semantic mutation proof. The generic Core2 decoder constructs Events without already encoded bytes representing their final stamped semantic state. Hashing source wire bytes cannot detect later edits to public `EventFrame.Events`; existing native-replay mutation tests require this check. It therefore cannot safely reuse the wire deduplication digest.

## Measurements

Machine: Apple device, macOS/darwin arm64, Go 1.27.3. Five samples per benchmark; table entries are independent medians of ns/op, B/op, and allocs/op. These benchmark runs use the default logical processor count and precede the later request to cap test memory.

Commands run before editing and after editing, from the worktree root:

```sh
(cd backend && go test ./server -run '^$' -bench 'BenchmarkPackageStreamBudget/mapped/(128|2048)$' -benchmem -count=5)
(cd backend && go test ./protocol/builtin -run '^$' -bench 'BenchmarkStreamTextDecode' -benchmem -count=5)
```

| Benchmark | Before ns/op | After ns/op | Before B/op | After B/op | Before allocs/op | After allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| PackageStreamBudget/mapped/128 | 2,652,198 | 1,821,049 | 3,197,791 | 2,289,740 | 29,246 | 15,542 |
| PackageStreamBudget/mapped/2048 | 41,079,982 | 24,189,141 | 48,662,211 | 34,700,112 | 451,714 | 238,324 |
| StreamTextDecode | 7,017,851 | 7,608,759 | 5,585,654 | 5,538,692 | 79,349 | 79,349 |

Mapped/128 time decreases 31.3% and allocations decrease 46.9%; mapped/2048 time decreases 41.1% and allocations decrease 47.2%. The decode-only benchmark does not exercise sequenced replay and its allocation count is unchanged; its 8.4% timing increase is not claimed as an improvement. Other work on this machine runs concurrently, so timing is noisy. The reported after measurements are a repeat after this task's own validation process exited; an earlier overlapping run is not used for the comparison. Raw logs are in ignored `.tmp-dev/stream-before.txt`, `decode-before.txt`, `stream-after-repeat.txt`, and `decode-after-repeat.txt`.

Validation: **pending: test-suite memory fix**. Results collected before stopping further runs:

- `(cd backend && go vet ./protocol/... ./server/...)`: passed.
- `(cd backend && go test ./protocol/... -count=1)`: passed; protocol 24.072s, builtin 47.880s.
- `(cd backend && go test ./server -count=1 -run 'Stream|Replay|Sequence|Frame|SSE|Gemini')`: unrestricted final attempt failed with `signal: killed` after 407.664s. An earlier focused run passed in 185.353s, but is not a substitute for final validation.
- `npm run test:protocol -- --suite=gemini-stream`: passed, exit 0; the runner uses Node built-ins and did not require `npm ci` in this worktree.
- Focused wire-digest, canonical-fallback, event-name, and compact-encoder regressions passed before the final small absent-frame/index guards. No full rerun of those final guards is claimed.

After the memory request, a serial-package retry uses `GOMEMLIMIT=1536MiB GOMAXPROCS=2 GOFLAGS=-p=1 go test ./server -count=1 -parallel=2 -run 'Stream|Replay|Sequence|Frame|SSE|Gemini'`, wrapped in a process-tree RSS watchdog that kills above 1900 MiB. `GOMEMLIMIT` is a soft runtime target, not a hard RSS limit. This retry and its watchdog are explicitly stopped when the coordinator instructs no more test or benchmark runs this round. Final memory-bounded validation remains pending; no persistent harness or test-suite setting changes.

## Alternatives considered

**Canonicalize once and share.** The user rejects this alternative. It would preserve semantic equality across differently spelled wire retransmissions but still decode and allocate a complete generic semantic tree for every sequenced frame. Wire equality avoids that work entirely for decoded frames.

**Field fingerprint.** The user rejects this alternative. Maintaining a parallel traversal of Event fields risks missing new fields, native extensions, absent/null/zero distinctions, or associations. The existing immutable wire Value supplies the complete frame identity without a second conversion model.

**Replace the native semantic digest with the wire digest.** Rejected because unchanged source bytes do not prove that publicly mutable semantic events remain unchanged. Keeping the semantic mutation proof preserves the existing native-replay safety contract.

**Write every NDJSON Value directly.** Rejected because parsed and native Values can contain physical line breaks. The existing read-to-NDJSON regression requires one physical output line, so the current compaction remains rather than weakening the assertion or adding a compactness-detection fallback.

## Consequences

Decoded sequenced replay now uses wire equality: identical bytes deduplicate; whitespace, key-order, number-spelling, or unmapped-extension changes at a reused sequence conflict even if mapped semantics appear equivalent. Tests cover byte identity, projection independence, changed whitespace/extensions, and compound-member collisions. Wire-free replay keeps canonical equality and exact large integers.

The hot mapped path removes repeated semantic normalization and roughly halves replay-related allocation volume without retaining frame payloads in dedup history. Additional frame digest metadata and per-operation writer state increase code size. NDJSON compaction and the independent native semantic mutation digest remain intentionally unoptimized. Full final validation is pending the test-suite memory fix; no commit is created.
