# Isolated Expr evaluation

This is a **rejected-for-now prototype**, not a production executor. See the [decision, compatibility gaps, and benchmarks](../../docs/backend-expr-evaluation.md).

Run from the repository root with Python 3 and the backend's Go toolchain:

```sh
python3 tools/expr-evaluation/run.py
python3 tools/expr-evaluation/run.py -race -run '^TestExprEvaluation' -count=1
python3 tools/expr-evaluation/run.py -run '^$' -fuzz '^FuzzExprEvaluationSubset$' -fuzztime=15s -parallel=2
GOMAXPROCS=1 python3 tools/expr-evaluation/run.py -run '^$' -bench '^BenchmarkExprEvaluation' -benchmem -benchtime=200ms -count=3
```

Extra arguments are passed to `go test`. The runner uses a temporary `-modfile` and Go overlay to expose real protocol internals to the experiment, with **Expr v1.17.8** pinned only in the temporary module. It does not modify backend module files. Go may download dependencies into its usual module cache.

`prototype_test.go.txt` is Go source deliberately excluded from normal package discovery. Format it with:

```sh
gofmt -w tools/expr-evaluation/prototype_test.go.txt
```

Tests include both passing scalar-adapter comparisons and explicit demonstrations of native/prototype incompatibilities. In particular, the operand-byte-budget ordering test passes when it observes the documented mismatch; it does **not** certify full semantic equivalence.
