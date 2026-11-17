# Implementation runbook: phases 1 and 4

Status: not started. Written 2026-11-16.

Scope: replace the semantic document layer (phase 1) and cut the redundant passes it
unblocks (phase 4). Background and measurements are in `full-refactor-handoff.en.md`;
this document is the build order and the gates. Phase 3 (generated executor) is a
separate document and must not be started here.

Hard constraint: shipped `spec.json`/`mapping.json` packages and custom user packages are
untouched. Schema 4 / DSL 2 do not change. No user protocol needs re-authoring.

Order is fixed: **corpus → epoch → representation → passes.** Two of the four pass
optimizations are only sound once the epoch exists, which is why the epoch is not deferred.

---

## Phase 0 — golden corpus (prerequisite)

No `.golden` files exist in the repository. This oracle has to be built before any
behaviour-changing step, or there is no way to prove the refactor preserved behaviour.

Build a generator that drives every builtin package through both directions and records,
byte for byte:

- Full wire output for short documents, long conversations (the `history256k` shape),
  tool-heavy and image-bearing requests, streaming frames at several frame counts, and
  every cross-protocol pair among the four builtins in both directions.
- Native-preserved **and** native-free paths. The generator must not clear `Native`; see
  the harness note at the end.
- Error cases: every expected `ConversionIssue` code, severity, stage, and path. Record
  the issue list, not only success. Changed error text or ordering is changed behaviour.
- Limit boundaries: inputs just under and just over each limit, recording which limit
  trips and the reported values.

Store under `protocol/testdata/golden/`, one file per case. Capture from the current tree
so it passes trivially now.

Gates: a replay test comparing exact bytes and exact issue lists for every entry; a
freshness test in the style of `typed_limits_test.go:238` so edits cannot pass silently;
the generator checked in and deterministic.

Do not start phase 1 until this is green and committed.

---

## Phase 1 — the document layer

### 1.1 Mutation epoch, on the existing structs

Smallest independently verifiable piece, and it unblocks phase 4. Do it before any
representation change so it can be validated in isolation.

- One monotonic generation counter on `Request` and on `Event`.
- One funnel: a single `touch()` method, called from every mutating helper.
- Convert every write site. They are scattered across `conversion_request_fields.go`,
  `core_control_*.go`, `package_model_controls.go`, `conversion_apply.go`
  (`applyClientOutputSettings`), and the clone/stamp paths in `runtime.go` and
  `request_workspace.go`.
- A test proving no assignment to an exported document field exists outside a `touch()`
  site. An AST audit beats a reflection audit here, because it catches writes in code no
  test exercises.

Gate: suite green, audit test green, corpus byte-identical.

### 1.2 Shrink `Node`, move to arena storage

`Node` is 296 bytes — the dominant allocation and the unit of recursion. A 64-message
conversation spends ~39.7 KB on struct footprint before payload bytes.

- Replace the three slice headers with one child range into a shared per-document arena,
  so a walk is a linear scan over cache-adjacent memory.
- Move rarely-set fields (metadata, cache intents, resources, attributes, the unexported
  association field) into side tables indexed from the node.
- Target under 128 bytes per node.

Do not touch `Value`. It is a 16-byte handle to immutable raw JSON, it preserves
absent / null / zero, and ~343 test field reads plus the `spanIdentity` caches depend on
it. It is the best-designed type in the layer.

Keep the unexported receipts — `verdict`, `continuationInput`, `rangeOrigin`,
`rangeAssociation`, `auxiliary`, `controlReceipt`, `requestWorkspace`. They are
load-bearing correctness and security state, several tests assert pointer identity on
them, and they move into the arena rather than disappearing.

### 1.3 Regenerate and reconcile the walkers

`cd backend && go generate ./protocol`. `internal/coreplan` and `internal/semanticmeter`
reflect over the document structs and write `plan_core_generated.go`,
`semantic_clone_generated.go`, `semantic_normalize_generated.go`, and
`typed_meter_generated.go`, so they follow the new layout automatically.

The oracle tests (`semantic_normalize_parity_test.go`, `semantic_clone_oracle_test.go`,
`typed_limits_test.go`) compare generated output against hand-written reflective
implementations. They will need behavioural-not-structural rewrites where they assert on
fields that moved. Change *how* the oracle observes; never *what* it demands.

### 1.4 Update ~380 external call sites

338 in `server`, 36 in `agent`. `agent` builds `[]protocol.Node` literals directly
(`agent/history.go:31,42,67,143`, `agent/context.go:131`). Keep field names stable where
meaning is unchanged so most sites need no edit. The failure mode to watch for is a site
that still compiles but now reads a different field; prefer accessor methods over widening
the struct.

Gate: full non-race suite green, `go vet ./...` clean, corpus byte-identical,
`internal/architecture` green, sharded suite still under five minutes.

---

## Phase 4 — cut the redundant passes

Now sound, because the epoch exists. Measured facts, not inspection: `equalCoreSnapshot`
is reached **109 times per request**; `requestContextUnchanged` runs 9 times and
`requestCheckUnchanged` twice.

### 4.1 Verdict compares — largest win

Each `requestContextUnchanged` / `requestCheckUnchanged` becomes a counter comparison when
the document has not been touched since the witness was taken, and falls back to the full
`equalCoreSnapshot` walk when it has.

This keeps the existing security posture exactly. `request_verdict.go:10-12` says a
redundant walk is preferable to accepting a stale security-relevant pass; a touched
document still gets the full walk.

Gates: corpus byte-identical; a test that mutates one field after the witness and proves
the full walk still runs; measured before/after.

### 4.2 Per-frame digest carry-forward

`digestSemanticEvents` marshals the whole event batch **twice per stream frame**. Once
`convertEvent` can report "no exported field mutated" via the epoch, and accepted event
count and order match the frame, the decode-side digest substitutes for the recompute.

Gates: corpus byte-identical for all streaming cases; a test proving a mutated event still
forces recomputation.

### 4.3 Warn-effect relaxation

Low risk, small: relax the warn-effect check in `canReuseOutput` when
`options.Diagnostics == nil`, since warn multiplicity is its only observable. Requires a
test that the diagnostics path is genuinely unused in that configuration.

### 4.4 Do not attempt these

Recorded so nobody spends a week rediscovering it.

**The second encode in `preserveMappedNative` (~17% of same-protocol encode) stays.**
`canReuseOutput` fails for all four builtins — not on plan flags, which are false for
every `encode_request` plan, but on `equalRequestSnapshot`, because conversion
legitimately sets `ClientOutput.CollectUsage` and `ResponsesStorage`. The shipped
openai-chat program reads `/clientOutput/collectUsage`, `/clientOutput/rawStreamOptions`,
and `/clientOutput/responsesStorage` inside conditionals, so those differences are
semantically live; outputs match on the benchmark fixture only because it is non-streaming.
Removing this needs a compile-time read-set analysis proving the plan cannot observe any
differing field, including root-enumerating ops (`keys`, `type`, `entries`, `project`,
`walk`, `visit`, empty-pointer `read`) — and for openai-chat that analysis still fails.
The gate is doing real work.

**Final wire re-validation stays.** `ValidateRequestOutput` uses `upstream-request`; the
encode path uses `generation-request`. Different validators, so not redundant despite
identical bytes. Note also that `plan_encode.go:109-112`'s `mapping.output.validate` is
nil for package directions — a no-op, not a pass.

Gate: corpus green, suite green, a measured delta reported per item.

---

## Discipline

- Never weaken an assertion, enlarge a limit, delete a sample, or skip a case to make a
  phase pass.
- Report "not measured" for anything not measured. Do not project.
- The full non-race suite stays under five minutes with bounded memory; the sharded runner
  exists for that and its budget is part of the contract.
- Record wall time and RSS in each phase note, in the existing Agent Note format (zh/en
  pair under `.agents/notes/implemented/performance/`).

## Harness defect to fix before trusting end-to-end numbers

`scripts/benchmark-performance.mjs` clears `Native`, so the ~17% same-protocol
second-encode cost never appears in end-to-end measurements, and every same-protocol
end-to-end figure quoted so far understates real production work. Fix it — drive the
harness through a path that preserves `Native`, or add a same-protocol case that does —
before using any end-to-end number to judge phase 4.

## What is not promised

No throughput projection is attached to phases 1 or 4. The only measured numbers are the
round-2 A/B result (1.06x geomean, peak heap −15% to −37%), the profile shares, call
counts, struct sizes, and the verdict-compare invocation counts. Measure each phase and
report what it actually delivered.
