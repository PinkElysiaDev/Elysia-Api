# Agent Note: Single-pass hot paths with conservative reuse

Status: implemented

## Problem

Requests repeatedly validate the same semantic content at decode, conversion, routing, and encoding boundaries. Each owner effect independently traverses the content tree, while same-wire native reconciliation repeats baseline finishing and encoding. Successful walks allocate diagnostic strings, and fixed semantic strings and simple scalars repeatedly enter the general codec.

The architecture must remain external A → A Mapping → Core2 → B Mapping → external B, without provider-specific Go branches. Diagnostic paths, codes, stages, suggestions, and first-error precedence are compatibility requirements, not details to trade for performance.

## Decision

`package_owner_walk.go` combines node and metadata owner selection into one DFS, evaluating local conditions in declaration order at each owner. Each effect retains its logical cost, committed at its original declaration position. Speculation itself shares the original remaining step allowance; it does not multiply the request budget by the number of effects. Any rejection, expression failure, incomplete result, or cumulative budget shortage falls back to the original effect-major checker. Consequently, failures on different owners, ordinary rejections/warnings interleaved with owner effects, resource errors, and metadata source paths retain their original precedence. Cold failures can repeat bounded work; successful owner checks do not create diagnostic paths.

A `requestVerdict` is published only after successful decoding without a scope resolver. It owns a private deep-cloned witness, a complete target capability copy, and effective limits. Because callers can directly mutate nested maps and slices in a public `Request`, pointer identity cannot establish continued validity. Reuse requires exact structural comparison; `CheckRequest` reuse additionally requires identical protocol, direction, scope, capabilities, and limits. Comparison uses existing immutable typed plans, not a cross-request digest cache.

Neither content validator reads `ClientOutput`, so delivery-only changes do not invalidate the content verdict. This is the only explicitly excluded field. All other exported fields and the protected node range association are compared. Mapping-output reuse uses a stricter whole-document comparison and must not inherit this exclusion. New validator inputs require reviewing this boundary.

`conversionState.contextValid` covers only a privately and continuously owned document. Reloading, JSON rule boundaries, and potentially mutating typed actions invalidate it. A decode verdict survives only if its witness proves validation inputs unchanged. Failures are not cached. Scope resolvers run after initial context validation and can edit content, so their output receives no receipt for that earlier pass; the next boundary validates normally. Accepting a stale security-relevant pass is worse than another walk: uncertainty about content or effective inputs forces revalidation.

The server's `CheckRoute`, binding, and transport checks were not removed. Different or stricter routing limits/capabilities still trigger complete content checks; different budgets are not silently treated as equivalent.

The native workspace also retains a private, never-exposed successful finished decode. Read-only reuse requires the existing workspace's compiled/value/source/context/limits proof, no editable resolver, and matching effective replay limits. Otherwise, the original rebuild remains. `ReplayNative` authorization/provenance, carrier checks, reconciliation, and final wire validation still execute. The already-generated output can serve as baseline output only when current and baseline requests, decoder contexts, and encoding budgets are strictly identical, and the program neither observes mutable state/delivery nor writes stream state nor declares warning effects. Continuations, delivery changes, different contexts/limits, stateful programs, and warnings preserve independent baseline encoding. In particular, distinct warnings produced by baseline encoding must not disappear. `DiagnosticSink` still collapses completely identical issues under its existing rule.

Diagnostic walks use borrowed `diagnosticPath` scratch, creating owned strings only for an issue or an explicit path binding. Children, reasoning content, tools, cache owners, and resources retain their order and pointer spellings. Callback walks can still have a constant per-walk scratch allocation; this is not a claim of zero allocations at every depth or for every callback, and no nesting limit was enlarged. Successful tool status checks allocate no paths.

Core controls share a small execution frame and directly read the current typed document instead of eagerly creating root-reader closures. Budget and value reader are bound during actual execution; bindings, context encoding, and the context object index initialize on demand. `StringValue` uses compile-time JSON spellings for a closed semantic constant set, not an unbounded global interning map retaining arbitrary user text. Authored literals already retain immutable `Value`s in the compiled graph.

Scalar fast paths accept only provably complete successful ASCII strings, booleans, canonical integers, and null. Strings are copied to retain decoder ownership. Unicode, escapes, overflow, wrong types, nil targets, and malformed JSON return to the original Sonic path, preserving accepted inputs, error types/messages, and partial destination writes. Absent and null remain distinct.

## Alternatives considered

**Return the first node-major rejection during the combined DFS.** Rejected: it need not be the original declaration-major first rejection. Metadata and intervening ordinary effects also retain their original ordering.

**Add a mutable version number to public requests and trust pointer identity.** Rejected: direct edits to nested containers bypass setters and version counters. Exact comparison against a private immutable witness is conservative but still costs a comparison walk and witness copy.

**Remove routing checks or equate different limits.** Rejected: binding/transport are independent authorization boundaries, and the original check can reject under stricter budgets. Without an equivalence proof, it runs again.

**Skip baseline encoding for every same-wire request.** Rejected: delivery, context, state, continuations, and warnings can make repeated execution observable. Only provably identical work is removed; there is no unchecked native passthrough.

**Implement a separate scalar error parser or intern all strings.** Rejected: the former can change codec errors and partial writes; the latter retains user text without a bound. Fast-success with the original codec fallback and a closed constant set preserves compatibility.

## Consequences

Successful owner checks share a traversal while retaining logical resource accounting and declaration precedence. Content validation can reuse a verdict, but public-document equality still traverses the document; actual content conversion, resolvers, and different targets/limits revalidate. The finished native decode adds private snapshot containers. Proven unchanged cases avoid repeated finishing and output-plan work; others rebuild. Failures, warnings, deep paths, complex scalars, and dynamic state intentionally retain slower compatibility paths.

New regressions cover complete issue fields for every shipped owner category; first-error precedence across owners and ordinary effects; budget boundaries; valid and invalid mutations; resolver edits; delivery-only validation versus strict replay; baseline warning observations/order; nested/reasoning/tool/cache paths; and scalar parity with the old decoder for success, malformed inputs, null, wrong types, nil targets, and partial writes. No samples, table cases, assertions, or protocol limits were removed.

### Diff deletion review

- `capability.go` and `server_tool.go` replace eager-path traversal/branch text with equivalent `*Path` methods. String-call wrappers remain for other semantic boundaries. Metadata failures rerun the original validator with the real path.
- `conversion_request_projection.go` and `conversion_tool_results.go` replace per-owner `fmt.Sprintf`, concatenation, and recursive calls with borrowed scratch. Validation order and failure construction remain. Tool streaming options receive their original paths on failure.
- `conversion_cache_projection.go` and `package_cache_projection.go` replace eager callback strings and remove an unused import. Existing rejection/diagnostic sites materialize the original path.
- `core_control_node.go` replaces per-node evaluation copies and eager `strconv` recursion with a shared state pointer and borrowed-path internals. Original string visitor entry points remain.
- `core_controls.go` moves eager context struct encoding, three root-reader closures, independent reader/budget/object-reader allocation, and bindings initialization into a lazy shared frame and direct typed reader. Document type/version checks remain.
- `conversion_state.go` replaces unconditional validation returns and load assignments with an invalidatable private verdict. The validator and post-mutation checks remain.
- `package_execute.go` replaces unconditional per-effect walking with successful verdict commits or original-walk fallback. The effect-major implementation was not deleted.
- `plan_input.go` and `plan_output.go` replace closure-only reader conditions and eager bindings allocation to support typed reading and lazy bindings. Non-control paths retain their original allocation/execution.
- `runtime.go` and `request_workspace.go` replace unconditional baseline finishing/encoding only when proof succeeds; original rebuild branches remain. Replay creation now includes all requests with compatible Native, allowing strictly unchanged non-projection requests to reuse work. This does not skip provenance checks.
- `value.go` splits grouped scalar decoding into fast-success plus the original codec fallback; complex types retain their codec. Other deletions are imports/blank lines resulting from these replacements, not tests or contracts.

## Measurements

Environment: Apple device, macOS. `go -C backend version` reported Go 1.27.3. All Go measurements use `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`. Commands below run from the assigned worktree root, `/Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-hotpath`.

Before and after use the same command wrapped by `/usr/bin/time -l`. The table uses per-case medians across repetitions; percentage change is `(after/before - 1) × 100`.

```sh
/usr/bin/time -l env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 \
  go -C backend test ./protocol/builtin -run '^$' \
  -bench 'BenchmarkRequestWorkloads/(same|anthropic|responses)/(short|history256k|tools32)' \
  -benchtime=20000x -count=3 -parallel=2 -benchmem
```

The final after measurement is still running. The complete comparison has not been measured; no percentages are estimated.

The following final command produced the complete correctness/architecture results. All passed.

```sh
/usr/bin/time -l env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 \
  go -C backend test ./protocol ./protocol/builtin ./server ./internal/architecture \
  -count=1 -parallel=2 -timeout=20m
```

| Check | Result | Go-reported duration |
| --- | --- | ---: |
| `./protocol` | PASS | 5.667 s |
| `./protocol/builtin` | PASS | 30.840 s |
| `./server` | PASS | 448.700 s |
| `./internal/architecture` | PASS | 0.503 s |

The same complete command took **489.50 s wall**, with **1,426,407,424 bytes peak RSS**. `GOMEMLIMIT` is a soft Go heap limit, not a hard OS process RSS cap; these are the actual RSS measurements. Its log remains in ignored `.tmp-dev/hotpath-full-final.log`.

Early focused checks briefly overlapped the baseline measurement. Final complete suites and the after measurement run sequentially, not concurrently. System activity affects wall timing; fewer allocations do not by themselves prove lower latency. Live-provider usage, race behavior, and end-to-end service throughput were not measured; no claims are made about them or achievement of the overall speedup target.
