# Sonic JSON traversal evaluation

Tracking: [#50](https://github.com/PinkElysiaDev/Elysia-Api/issues/50). Baseline: `0c1efcf`.

## Decision

Adopt the existing **Sonic v1.15.4 AST/Search APIs for JSON Pointer reads**. Retain the bulk object/array readers after measuring the broader prototype.

The old pointer implementation materialized and detached every member of each object/array along a path. The selected implementation enumerates an object's AST properties and uses Search for array indices, retaining raw intermediate fragments only during the call. It copies only the final selected fragment. This reduces unrelated payload copying while retaining the existing `Value` contract.

The full AST-container prototype improved large-payload cases but increased memory and/or execution time in small-object, wide-object and array cases. It was removed from the final implementation. The existing container scanners remain for bulk readers and domain-specific resource accounting.

## Semantic and ownership requirements

- Values remain raw JSON: large integers, exponent spelling, negative zero, explicit null/false/zero, and container whitespace are preserved without conversion through floating point.
- A missing field/index returns an absent `Value`. Traversing a present scalar, including null, returns an error. Array indices remain canonical nonnegative decimal integers; object keys such as `"01"` remain ordinary strings.
- Object lookup remains **last-decoded-key-wins**, including duplicate keys with escaped spellings. Sonic Search's string-key lookup returns the first match, so object selection uses AST property iteration instead. Invalid UTF-8 keys retain the standard decoder's replacement behavior.
- AST nodes and searchers are call-local. Intermediate slices may borrow the validated input; the final nonidentity selection uses independent storage. An empty pointer preserves identity behavior. Existing request-scoped bulk-view ownership rules are retained.
- `Value` validates external JSON before these operations. Search skips redundant validation only on that immutable representation. Compiler/execution resource checks retain their existing role.
- `Value` validation and pointer selection both use Sonic after #49. The old fallback for the standard decoder's higher nesting ceiling has been removed; AST/search failures propagate directly. The deepest accepted sibling remains covered by a focused regression.

No public API or JSON Pointer behavior change is intended. No new dependency is added.

## Exploratory container prototype

Linux/amd64, Go 1.27.2, AMD Ryzen 7 8845H; default `GOMAXPROCS=8`. Three samples per case, 200 ms per sample. These initial sequential timings were noisy and are used as prototype-screening evidence, not end-to-end throughput predictions. Memory increases were also visible independently of timing.

| Bulk operation | Baseline median ns/op | Full AST prototype ns/op | Baseline B/op | Prototype B/op |
| --- | ---: | ---: | ---: | ---: |
| Small object | 2,452 | 3,068 | 448 | 1,529 |
| 128-key object | 89,591 | 197,041 | 23,080 | 35,077 |
| 1 MiB object, owning children | 3,083,945 | 1,094,257 | 1,057,225 | 1,063,095 |
| 1 MiB object, request-scoped view | 500,782 | 89,948 | 456 | 1,561 |
| 128-item array | 17,965 | 66,100 | 8,484 | 13,046 |

The final change leaves these bulk readers intact. It does not add payload-size dispatch thresholds or retain a second general-purpose container implementation.

## Final pointer comparison

To reduce scheduler/GC variability observed in the exploratory runs, the selected pointer paths were measured with `GOMAXPROCS=1`, five samples per case and 500 ms per sample. Both implementations used the exact same benchmark harness. The baseline was built using a Go overlay that restored `expression_eval.go` from `0c1efcf` and replaced the newly added `value_pointer.go` with an empty `package protocol` file.

| Scenario | Before median ns/op | After median ns/op | Before B/op | After B/op | Before allocs/op | After allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Small nested object/array path | 701.8 | 888.2 | 864 | 2,209 | 23 | 8 |
| Last key in 128-key object | 17,842 | 13,066 | 23,448 | 14,419 | 402 | 22 |
| Small head field beside 1 MiB payload | 515,998 | 46,519 | 1,057,584 | 2,187 | 16 | 7 |
| Small tail field beside 1 MiB payload | 514,990 | 46,002 | 1,057,608 | 2,203 | 16 | 7 |
| Last value in 128-item array | 7,376 | 3,618 | 9,256 | 2,212 | 146 | 9 |

Observed timing ranges across the five samples (ns/op):

| Scenario | Before range | After range |
| --- | ---: | ---: |
| Small nested path | 700.0–705.2 | 881.7–903.4 |
| Wide object | 17,602–17,885 | 12,759–13,149 |
| Large head | 510,832–517,224 | 45,768–46,590 |
| Large tail | 510,019–520,834 | 45,305–46,673 |
| Array | 7,245–7,814 | 3,567–3,653 |

These are historical microbenchmarks of selected reads, not gateway request latency measurements. The additional small-path check used the same five-sample settings and showed a tradeoff: roughly 27% more time and 1.3 KiB more allocation per read, despite fewer allocation objects. This implementation favors reducing unrelated copying in wide/large inputs and array traversal; it does not claim every pointer read is faster. Object selection still visits every property to preserve duplicate-key behavior. Large-result copying costs remain workload dependent.

### Reproduce

From `backend/`:

```sh
# All exploratory workloads, including retained bulk readers
go test -tags=sonic ./protocol -run '^$' \
  -bench '^BenchmarkJSONTraversal$' -benchmem -benchtime=200ms -count=3

# Selected pointer paths
GOMAXPROCS=1 go test -tags=sonic ./protocol -run '^$' \
  -bench '^BenchmarkJSONTraversal/pointer' -benchmem -benchtime=500ms -count=5
```

For the baseline comparison, use the same working-tree benchmark harness with a `-overlay` JSON file whose `Replace` map points the absolute path of `backend/protocol/expression_eval.go` to a copy from `git show 0c1efcf:backend/protocol/expression_eval.go`, and the absolute path of `backend/protocol/value_pointer.go` to a file containing only `package protocol`. Add `-overlay=/path/to/overlay.json` to the second command. The historical implementation is not duplicated in production or permanent benchmark helpers.

## Validation

Focused race command:

```sh
go test -tags=sonic -race ./protocol ./protocol/builtin ./server \
  -run '^(Test(JSONPointer|RequestViews|RequestResources|Value|Expression|CompileChecksNestedOutputSchema|Native|MutationTransaction|TypedMeter|RequestWireSchemaTypedParity|JSONModuleFallbackMatchesShippedSamples|HTTP|GatewaySSEAndNDJSON|GatewayBrokenStream|AgentProtocolV2)|FuzzJSONPointerObjectLookup)' \
  -count=1 -timeout=3m
```

Results: `protocol` **3.112s**, `protocol/builtin` **3.251s**, `server` **18.555s**, all PASS.

The regressions cover raw numeric lexemes, absent/null/scalar distinctions, strict array indices, duplicate/escaped/invalid-Unicode keys, deep siblings, ownership and concurrent reads. Existing expression, mutation/limit, request-view, built-in sample, HTTP bridge and Agent authoring tests also passed.

Bounded differential fuzzing against the standard decoder's object lookup contract:

```sh
go test -tags=sonic ./protocol -run '^$' \
  -fuzz '^FuzzJSONPointerObjectLookup$' -fuzztime=10s -parallel=2
```

PASS: **124,587 executions**, 169 new interesting inputs. The full backend suite was not run.
