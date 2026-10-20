# Runtime JSON Schema validation migration

Tracking: [#50](https://github.com/PinkElysiaDev/Elysia-Api/issues/50). Baseline: `9077dff`; prerequisite: #58.

## Decision and scope

Use **`github.com/santhosh-tekuri/jsonschema/v6` v6.0.3**, pinned to JSON Schema draft 2020-12, for runtime `ValueSchema` validation. Remove the handwritten recursive object/array/type/enum validator. This is a maintainability migration with measured performance tradeoffs, not a universal speed improvement.

`ValueSchema` remains the closed gateway authoring subset. The compiler still rejects unsupported types, misplaced object/array rules, unknown/duplicate required keys, incompatible enum types, and schemas exceeding its depth budget. Expression type inference and static output analysis remain compiler responsibilities. The library receives only generated in-memory schemas, with no user-authored `$ref` or remote schema vocabulary.

Mappings (including task/model/Agent mappings), operation inputs, and the model-page contract are compiled with their protocol revision. Typed and JSON mapping execution reuse the same immutable validators. `CheckOperationInput(name, body)` selects the revision-owned validator by operation name. Transport metadata copies cannot alter the input contract; schema changes require compiling a new definition. This removes per-request schema serialization and the unused customized-schema compilation path introduced by the initial migration. Compile-time literal checks also use the library.

## Adapter semantics

- `any` omits the standard `type` restriction.
- Object `allowUnknown` maps to `additionalProperties`, defaulting to false; `required`, `properties`, and array `items` retain their meanings.
- `nullable` becomes a null branch in `anyOf`, preserving the existing bypass of **all** other constraints, including enum.
- Duplicate enum entries are deduplicated using existing exact semantic equality before compiling the standard schema. Empty enum means no enum constraint.
- Sonic decodes validation inputs with `UseNumber`; `encoding/json.Number` is only the numeric representation. Large integers and decimal/exponent equivalence are checked without float64 conversion. Raw `Value` storage is never rewritten.
- Missing values are rejected before decoding. Explicit null, false, zero, and empty containers remain distinct.
- Duplicate object keys retain last-decoded-key-wins. Invalid UTF-8 is normalized only in the temporary validation view to preserve replacement-character behavior.
- All request-local trees are temporary. Compiled schemas are immutable and safe for concurrent use. The library may retain rejected values in its error causes; callers should treat errors as request-scoped diagnostics.

## Breaking changes

**CompilerVersion changes from `2.0.0-dev.72` to `2.0.0-dev.73`.** Existing verification evidence must be regenerated before it is considered current.

1. **Numeric conversion bounds apply throughout schema-validated values**, including unconstrained/unknown descendants and schema enum entries: a number's JSON spelling may not exceed 4096 bytes, and its exponent must fit the inclusive range -4096…4096. The library uses `big.Rat` for integer/enum comparisons; this preflight prevents short extreme exponents from requesting unbounded arithmetic work. Previously some `number`/`any`/unknown fields accepted such lexemes. Native `Value` storage and schema-free paths remain unrestricted by this new preflight.
2. **The complete value is resource-checked before interface decoding.** Bytes, nodes and container depth are checked even beneath `any` or unknown properties. Mapping boundaries already perform whole-value checks; this also closes gaps in standalone operation-schema validation. Limits are not raised.
3. **Validation diagnostics use library messages and error trees.** Error causes remain available through `errors.As`; mapping diagnostics use an escaped instance JSON Pointer from a leaf error. Multiple errors can be reported, and their ordering is not an API guarantee. Required/additional-property failures can point at their containing object rather than the old manually constructed field path.

No new authoring keywords, format assertions, or external schema loading are exposed. Reflection/catalog generation continues to use invopop independently.

## Benchmarks

Linux/amd64, Go 1.27.2, AMD Ryzen 7 8845H, `GOMAXPROCS=1`. Median of three samples, 200 ms per sample. Baseline was measured before modifying the validator with the same fixture generator. Final repeated validation excludes schema compilation, but includes Sonic decoding, numeric preflight, whole-value resource checks, and error creation.

| Repeated validation | Before ns/op | After ns/op | Before B/op | After B/op | Allocs before → after |
| --- | ---: | ---: | ---: | ---: | ---: |
| Small object | 863.7 | 1,399 | 800 | 1,272 | 20 → 24 |
| 64-field object | 29,403 | 34,957 | 27,768 | 29,098 | 776 → 707 |
| 128-item array | 118,849 | 132,904 | 112,709 | 142,677 | 2,945 → 2,722 |
| Object with 1 MiB opaque string | 630,054 | 472,479 | 752 | 1,241 | 18 → 19 |
| Exact numeric enum | 2,457 | 1,245 | 2,280 | 872 | 51 → 26 |
| Invalid object | 590.2 | 6,201 | 640 | 5,264 | 17 → 134 |

The invalid case now constructs detailed multi-error diagnostics rather than stopping at the first handwritten failure. The opaque-string case borrows immutable string storage during validation; it does not copy the entire payload. These microbenchmarks do not predict end-to-end gateway latency.

Compilation now includes generating and validating a standard schema, whereas the baseline only checked the gateway schema declaration:

| Schema preparation | Before ns/op | After ns/op | Before B/op | After B/op |
| --- | ---: | ---: | ---: | ---: |
| Small object | 339.6 | 28,951 | 352 | 30,601 |
| 64-field object | 10,463 | 531,334 | 10,896 | 554,494 |
| Array | 401.8 | 37,923 | 424 | 38,913 |
| Open object | 201.0 | 20,969 | 184 | 22,552 |
| Enum | 532.9 | 10,688 | 336 | 10,304 |

The preparation increase is why validators are revision-owned, not rebuilt on the regular request path. No throughput improvement is claimed for small/wide objects, arrays, invalid inputs, or compilation.

From `backend/`:

```sh
GOMAXPROCS=1 go test -tags=sonic ./protocol -run '^$' \
  -bench '^BenchmarkRuntimeSchema' -benchmem -benchtime=200ms -count=3
```

To reproduce the baseline, use a worktree at `9077dff` and copy `runtime_schema_bench_test.go`. Remove `compileValueSchema` setup in the repeated-validation benchmark and replace `validator.validate(value, "/input", limits)` with `checkValueSchema(value, tc.schema, "/input", 1, limits)`. In the preparation benchmark, replace `compileValueSchema` with `validateSchema(tc.schema, "/schema", 1, DefaultLimits())`, handling its single error return. All fixture values and timing settings remain identical.

## Validation

```sh
go test -tags=sonic -race ./protocol ./protocol/builtin ./server \
  -run '^Test(RuntimeSchema|Expression|Compile|Operation|Model|Request|JSONModule|Native|Mutation|Typed|Verification|Schema|AgentProtocolV2|ProtocolRevision|ProtocolAdmin|CLIReference)' \
  -count=1 -timeout=3m

go test -tags=sonic -race ./protocol/builtin \
  -run '^Test(ShippedDefinitionsVerify|BuiltinVerifiedProfilesKeepTextRoutesAndFullDiagnostics)$' \
  -count=1 -timeout=3m

go test -tags=sonic -race ./protocol \
  -run '^TestRuntimeSchemaMappingValidation$' -count=1
```

All passed: first command `protocol` **4.209s**, `protocol/builtin` **3.565s**, `server` **22.287s**; shipped-definition/profile checks **4.997s**; additional mapping-cause/location regression **1.066s**.

Tests cover exact integers and decimal equivalence, enum deduplication, nullable enums, open/closed objects, empty containers, absent values, duplicate/escaped/invalid-Unicode keys, escaped diagnostic paths, numeric/resource bounds, schema-free values, precompiled operation reuse, metadata isolation, mapping input/output validation, and concurrent readers. Shipped definitions and verification profiles pass with the new compiler version.

A **temporary Go overlay** extracted `checkValueSchema` from `9077dff`, renamed it as the historical oracle, and added a differential fuzz harness over the benchmark schemas plus nullable enum cases. The harness compared acceptance on valid JSON inside the new numeric/resource bounds; it did not assert equality for intentional breaking changes. A 15-second run with two workers passed **193,338 executions**, with 24 new interesting inputs. The historical validator and temporary harness were not retained as redundant production/test implementations. Local artifacts: `/tmp/opencode/runtime-schema-{legacy_test.go,differential_test.go,differential-overlay.json}`.

The full backend suite was not run.
