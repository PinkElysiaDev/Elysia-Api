# Expr runtime evaluation

Tracking: [#50](https://github.com/PinkElysiaDev/Elysia-Api/issues/50). Baseline: `24dbb60` (#59). Candidate: **`github.com/expr-lang/expr` v1.17.8**.

## Decision

**Defer production adoption.** Keep the current protocol DSL compiler/evaluator. The evaluated native mapping changes important semantics; the compatibility-oriented scalar prototype adds compilation, callbacks, and budget/diagnostic adaptation without removing the domain-specific machinery. It also regresses most measured workloads and has a demonstrated operand-budget ordering gap.

This completes the focused prototype evaluation, not a production migration. There is no new runtime dependency, compiler-version change, or public compatibility change. A runnable isolated experiment is committed so the decision can be revisited with evidence rather than a permanently maintained second executor.

The results concern the tested adapters and this DSL, not Expr's general performance or suitability for other expression languages.

## Existing responsibilities

| Area | Current implementation | Why it still needs gateway logic |
| --- | --- | --- |
| Authoring contract | `catalog.go`, `expression_compile.go` | Closed JSON operation vocabulary; allowed/required fields, reference expansion/cycle detection, static schema inference and output checks |
| Value semantics | `value.go`, `value_pointer.go`, `value_schema.go` | Raw immutable JSON, exact numeric equality, absence distinct from null, strict pointers, owned selections |
| Execution | `expression_eval.go` | Per-DSL-node steps/cancellation, exact `all`/`any` evaluation, omission, bounded intermediate output |
| Collection operations | `evaluateArray`, `evaluateOperands`, `evaluateKeyedArray` | Item scope, ordered/omitting maps, strict predicates, incremental byte accounting, merge collision policy, stable exact-key sort and unique associations |
| Diagnostics | `mapping_error.go` | Original definition paths and wrapped error causes, not generated expression source locations |

Generic parsing/type checking cannot replace the first row without changing the public DSL. Reusing Expr for the other rows requires explicit adapters for these policies.

## Native compatibility probes

`TestExprEvaluationNativeDifferences` executes these observations against the pinned version:

1. **Exact numeric equality:** Expr evaluates `json.Number("1") == json.Number("1.0")` as false; the gateway compares them as equal. Decoding into float64 instead collapses distinct integers above 2^53. This is an input-representation/semantics mismatch, not a claim that Expr itself always converts numbers to float64.
2. **Presence:** ordinary dynamic-map property reads compare equal to nil for both missing keys and explicit null. The gateway needs a presence sentinel or explicit lookup result.
3. **Evaluation order:** native `&&` short-circuits. The gateway evaluates `all`/`any` operands in order even after the boolean result is known, including required-field failures and budget accounting.
4. **Cancellation:** a pre-canceled context passed through `expr.WithContext` does not stop a pure `map(1..32, {# * 2})` expression. [`WithContext`](https://github.com/expr-lang/expr/blob/v1.17.8/expr.go) injects context into functions that accept it; it is not an automatic per-instruction cancellation check.
5. **Budgets:** `vm.VM.MemoryBudget` rejects an oversized range in the probe but does not enforce a byte limit on an existing string returned from the environment. [`MaxNodes`](https://github.com/expr-lang/expr/blob/v1.17.8/expr.go) bounds source AST nodes, not the gateway's dynamically accumulated DSL execution steps. Neither maps directly to `Limits.Nodes`/`BufferBytes`.

Additional instrumentation could address these differences. It would need to be designed and maintained as part of a new executor rather than assumed to come from the native operators/builtins.

## Compatibility-oriented prototype

The experiment lowers an already compiled gateway tree into generated Expr source for `read`, `literal`, `omit`, `exists`, `present`, `not`, `equal`, `all`, `any`, and `if`. User values remain in an immutable node table and are not interpolated into generated source. Builtins are disabled and an Expr AST-node bound is configured.

- Leaf callbacks reuse the real evaluator, retaining JSON Pointer behavior, raw numbers, missing values and leaf diagnostics.
- An entry callback supplies the existing per-node step/cancellation rules.
- Aggregate callbacks preserve exact equality and strict booleans rather than using native operators.
- Conditional lowering uses Expr branching while retaining omission, selected-branch evaluation and output checks.
- Callbacks wrap errors with the original DSL path. Expr's wrapped causes preserve `errors.Is(context.Canceled)` and `mappingErrorPath` in the tested cases.
- Each execution has its own environment, budget and VM; compiled programs/node tables are shared read-only.

Two calling conventions were measured: reflected environment methods and typed **`expr.Function`** callbacks. The latter substantially reduces callback overhead, so the decision is not based only on the slower reflective route. Program execution is measured after compilation; compilation measures the existing DSL compiler plus lowering and Expr compilation, since the DSL compiler's checks are still required.

### Remaining ordering gap

`TestExprEvaluationOperandBudgetOrderingGap` deliberately asserts a known incompatibility:

- Evaluate `all(true, true, requiredRead("/missing"))` with an 8-byte intermediate budget.
- The current evaluator stops after the second operand exceeds the aggregate byte budget: three DSL steps including the root.
- The aggregate-callback prototype evaluates all function arguments first. It reaches the missing-field error before entering the aggregate byte check: four DSL steps.

Fixing this requires incremental accumulator callbacks or different control-flow lowering before each subsequent operand. Thus even the prototype's nominally compatible scalar subset is **not production-equivalent**. The passing test records an adoption blocker, not permission to change the gateway contract.

Loops, object/array construction, merge, join, sort, associate, cast, parse/stringify JSON and prefix operations were not migrated. Their current implementations expose additional ownership, accounting and semantic work. No performance or parity claim is made for a full Expr-based engine.

## Measurements

Linux/amd64, Go 1.27.2, AMD Ryzen 7 8845H, `GOMAXPROCS=1`. Medians of three 200 ms samples. Both candidates use the same parsed input and actual compiled DSL nodes; request-local state creation is included. All cases succeed under default budgets. These are evaluator microbenchmarks, not gateway HTTP latency measurements.

| Execution case | Current ns/op | Expr functions ns/op | Expr methods ns/op | Current B/op | Functions B/op | Current → functions allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Pointer read | 412.6 | 768.7 | 1,541 | 1,176 | 1,720 | 5 → 11 |
| Exact numeric equality | 2,967 | 3,646 | 7,053 | 3,160 | 4,056 | 49 → 59 |
| Missing-field predicate | 636.3 | 1,253 | 3,763 | 1,168 | 1,856 | 7 → 14 |
| 64-predicate conjunction | 47,659 | 67,437 | 242,010 | 73,431 | 87,224 | 581 → 804 |
| Conditional exact-number selection | 3,727 | 4,780 | 11,444 | 4,280 | 5,656 | 55 → 68 |
| Small field beside 1 MiB payload | 46,717 | 46,307 | 48,594 | 1,153 | 1,697 | 5 → 11 |

The roughly 0.9% timing difference in the large-input case is not evidence of a meaningful speedup. That case is dominated by the shared pointer scanner. The other function-adapter cases are approximately 23–97% slower and allocate more than the current evaluator. Reflected methods are materially worse here.

| Compilation case | Current ns/op | DSL + Expr functions ns/op | Current B/op | DSL + Expr B/op |
| --- | ---: | ---: | ---: | ---: |
| Read | 1,940 | 22,215 | 1,448 | 24,274 |
| Equality | 7,598 | 40,561 | 4,992 | 36,060 |
| Missing predicate | 4,308 | 35,604 | 2,976 | 32,179 |
| 64-predicate conjunction | 309,991 | 841,516 | 204,410 | 561,484 |
| Conditional | 14,776 | 61,316 | 9,721 | 47,958 |

Different lowering strategies, VM reuse or workload mixes may change these numbers. Such changes do not automatically resolve semantic and maintenance costs, and were not claimed as evaluated solutions.

## Reproduction and validation

From the repository root:

```sh
python3 tools/expr-evaluation/run.py -race \
  -run '^(Test(ExprEvaluation|Expression|CompileChecksNestedOutputSchema)|FuzzExprEvaluationSubset)' \
  -count=1 -timeout=2m

python3 tools/expr-evaluation/run.py -run '^$' \
  -fuzz '^FuzzExprEvaluationSubset$' -fuzztime=15s -parallel=2

GOMAXPROCS=1 python3 tools/expr-evaluation/run.py -run '^$' \
  -bench '^BenchmarkExprEvaluation' -benchmem -benchtime=200ms -count=3
```

The runner copies the backend module files into a temporary directory, adds only the pinned experimental Expr dependency there, and overlays the prototype into package `protocol` so it can exercise real private internals. It removes the temporary module/overlay on exit. No Expr dependency is added to backend `go.mod`/`go.sum`; the `.go.txt` experiment is excluded from normal builds.

- Focused race checks, existing expression checks and fuzz seeds: **PASS, 2.185s**.
- Differential fuzzing: **PASS, 145,028 executions**, 220 new interesting inputs in 15 seconds with two workers.
- Fuzzing compares raw results, success/failure, DSL step counts and original error paths across the tested scalar expressions, varied valid JSON and step budgets. It excludes oversized/invalid JSON and does not claim to cover all DSL programs or the separately documented byte-budget ordering gap.
- Concurrent shared-program execution and canceled-context cause retention passed. An additional deterministic cancellation-between-nodes regression passed under race detection (**1.064s**), preserving the fourth-step stop and original error path in both evaluators.
- The full backend suite was not run; production code is unchanged.

## Revisit criteria

Reconsider adoption only with a concrete design that removes a material portion of the custom executor while preserving bounded exact numbers, absence/ownership, ordered incremental budgets, cancellation and original diagnostics. A new or explicitly versioned expression language could make different choices, but that is a separate compatibility decision, not this migration.
