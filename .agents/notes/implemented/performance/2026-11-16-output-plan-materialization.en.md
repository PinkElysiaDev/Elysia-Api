# Agent Note: Output-plan materialization storage

Status: implemented

## Problem

Output-plan execution allocates a separate `JSONOutput` range variable for every item in `buildIterationOutput`: assigning `&item` to the child evaluation forces the variable onto the heap even when a particular item is a raw leaf. This is request-time materialization, not compile-time plan construction.

`buildOperandValues` also allocates a result slice for every `merge_deep` and `merge_usage` invocation, although both callers consume its elements synchronously. The existing operand helper already accepts caller-owned stack storage for other operations.

## Decision

`buildIterationOutput` iterates by index and borrows `&items[index]` rather than taking a range variable's address. Raw arrays use the slice produced by `plannedArrayItems`; constructed, captured and walked arrays use their existing item slice. The borrow is scoped to the synchronous body invocation. Reads and returned containers copy `JSONOutput` values; no evaluation pointer is retained in the result, and the source slice is not mutated.

`buildDeepMerge` and `mergeUsage` each own a four-element stack array, select storage through `selectOperandStorage`, and fill it through `buildOperandValuesInto`. The allocating `buildOperandValues` wrapper is removed. Nested evaluations and concurrent requests cannot share these arrays. Larger operand lists retain the existing `make` fallback; the capacity remains four. Evaluation and operand order, accounting, diagnostics and declarative dispatch remain unchanged. Production code is six lines shorter overall.

## Alternatives considered

**Request provenance memo — rejected.** `core_read_context_test.go:330` deliberately fails with “memo owner expanded beyond synchronous response encoding” if memo ownership expands to requests. `provenanceReadCapacity` is two, and capture authority can tag a provenance root, so root reuse also has an authority caveat. This change does not connect `provenanceReadMemo` to `*Request` or expand memo ownership.

**Avoid copying the evaluation struct — rejected.** Escape analysis disproves the proposed heap-allocation mechanism: `child := state` does not escape; its copy stays on the stack. The evaluation copy remains unchanged. The actual escaping object is the addressed range variable.

**Per-evaluation or shared operand scratch — not used.** A nested operand evaluation can run while its caller's operands are still live. Reusing one mutable slice would require explicit ownership and nesting management. Caller-local arrays use the existing helper, require less code, and cannot alias another invocation's storage.

**Increase the stack capacity or optimize other iteration operations — deferred.** Neither is necessary to remove the verified allocations here. The four-element capacity and the filter/group-run iteration implementations are unchanged; this is not a claim that all output-plan allocations disappear.

## Consequences

Every measured workload has fewer allocations per operation. The largest reduction is anthropic/history256k: 39,323 to 37,844, or 1,479 fewer allocations/op (3.76%). The measurements do not establish an overall latency improvement: five timing medians increase, and bytes/op increase for two workloads. The effects of the two fixes are not measured separately.

The raw-array `make([]JSONOutput, len(values))` in `plannedArrayItems` still escapes, as do output containers and their storage. The generic operand helper still reports escaping `make` fallback paths. The change eliminates the iteration's separate per-item heap objects, not all slice/container allocation. Escape output at the two new operand call sites reports the inlined `make` as not escaping, and neither local `storage` array is moved to the heap.

Regressions cover raw and constructed sources, recursive walked/captured sources, map/flatmap/walk results retained across another execution, item identity and exact order, immutable source storage, first-error paths, byte/step/cancellation parity, and nested/concurrent operand use at counts two, four and five. Existing tests and assertions are unchanged.

## Measurements

Environment: Apple device, macOS. Worktree: `/Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r3-meters`; clean baseline revision: `4efe8fb64bd0a394fcac215036f197ccdb5b161e`.

The following exact workload command runs before editing and again after the production changes and regressions. Runs are sequential, never concurrent. Each table cell is the independent median of three samples; percentage change is `(after / before - 1) * 100`. These are new measurements, not the previously supplied reference medians.

```sh
cd /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r3-meters
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend test ./protocol/builtin -run '^$' \
  -bench 'BenchmarkRequestWorkloads/(same|anthropic)/(short|history256k|tools32)' \
  -benchtime=2000x -count=3 -parallel=2 -benchmem
```

Before:

| Case | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| same/short | 94,466 | 51,606 | 707 |
| same/history256k | 5,639,786 | 5,885,511 | 26,504 |
| same/tools32 | 1,482,630 | 754,955 | 8,802 |
| anthropic/short | 92,636 | 59,386 | 911 |
| anthropic/history256k | 5,704,679 | 6,530,666 | 39,323 |
| anthropic/tools32 | 1,231,292 | 628,503 | 8,625 |

After:

| Case | ns/op | B/op | allocs/op | Time change | Bytes change | Allocations change |
|---|---:|---:|---:|---:|---:|---:|
| same/short | 91,336 | 51,226 | 699 | -3.31% | -0.74% | -1.13% |
| same/history256k | 5,741,766 | 5,893,028 | 26,055 | +1.81% | +0.13% | -1.69% |
| same/tools32 | 1,521,542 | 766,610 | 8,634 | +2.62% | +1.54% | -1.91% |
| anthropic/short | 94,970 | 58,562 | 881 | +2.52% | -1.39% | -3.29% |
| anthropic/history256k | 5,865,066 | 6,528,177 | 37,844 | +2.81% | -0.04% | -3.76% |
| anthropic/tools32 | 1,269,162 | 624,606 | 8,467 | +3.08% | -0.62% | -1.83% |

The first clean-tree baseline attempt is externally killed before anthropic/tools32 and excluded. A complete retry finishes before any tracked edits (86.386s); the complete after run finishes in 88.700s. Allocation counts are identical across the three samples except after anthropic/history256k (37,844 / 37,845 / 37,844).

This is an active, non-isolated macOS session with other applications. One-minute load averages are 2.82 at the complete baseline start, 1.40 at the after start, and 3.67 after verification. These snapshots do not identify which benchmark interval had greater contention. Extra contention biases ns/op upward (against the change if it affects after more; in its favor if it affects before more). It cannot be used to dismiss the measured slowdowns. Bytes/op also vary across samples; no broad latency or byte-reduction claim is made.

Escape-analysis producing command, run before and after:

```sh
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend build -gcflags='-m=1' ./protocol
```

Before: `protocol/plan_output.go:490:13: moved to heap: item`.

After: no `moved to heap: item` or `moved to heap: child` anywhere in `plan_output.go`. The index borrow is at line 491. Remaining raw-item slice escape: `protocol/plan_container.go:110:15: make([]JSONOutput, len(values)) escapes to heap`. The inlined operand selections at `plan_merge_deep.go:23:34` and `plan_usage.go:43:34` report `does not escape`; the helper's generic `make` fallback still reports `escapes to heap`.

Raw benchmark and escape logs remain ignored under `.tmp-dev/output-plan-{before,after}.txt` and `.tmp-dev/output-plan-escape-{before,after}.txt`; the interrupted attempt is `.tmp-dev/output-plan-before-interrupted.txt`.

## Testing

All required packages pass:

```sh
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend test ./protocol ./protocol/builtin ./internal/architecture -count=1 -parallel=2 -timeout=20m
```

Results: protocol 8.127s; protocol/builtin 31.764s; internal/architecture 0.477s.

Full backend vet passes with no diagnostics:

```sh
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend vet ./...
```

Focused regressions pass normally and under the race detector (0.453s and 1.584s):

```sh
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend test ./protocol -run '^(TestOutputPlanIteration|TestMergeOperandStorage)' -count=1 -parallel=2 -timeout=5m
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend test -race ./protocol -run '^(TestOutputPlanIteration|TestMergeOperandStorage)' -count=1 -parallel=2 -timeout=10m
```
