# Backend consumer optimization and package cutover gate

Starting commit: `00fba372`, branch `refactor/protocol-dsl`.
The same frozen backend supplies D0 (default presets) and P0 (explicit v4 packages).
The production default still uses legacy definitions. Package/DSL/Core remain 4/2/2.

## Acceptance contract

Each independent candidate must improve its intended consumer without stable
regression against its parent or P0. Final package switching additionally requires
no stable regression against D0, the default chain at the start of this round.
Historical 1.7.0 and A results are references, not this round's cutover gate.
The existing 95% ratio intervals classify regressions; 5% is investigation priority,
not a tolerance. At most one equal-sized confirmation is combined with all original
samples. Failed or rejected measurements are retained.

Only isolated databases, synthetic local providers and local commits are used.
Old protocol formats are not migrated. Existing databases, logs and credentials
are not changed. Default switching and legacy retirement remain conditional on
the gate; accepted local savings alone do not complete the migration.

## Evidence and candidates

Fresh stages use the `backend-` prefix under
`.tmp-dev/allocation-optimization-2026-11-04/`. Existing freeze, process isolation,
microbenchmark and comparison tools are reused. Source/binary/configuration,
provider/client, runtime and command identities are recorded by those tools.
Measurements run separately from builds, tests, profiles and forced-GC diagnostics.

Candidate order: small operand workspaces; wire array validation; constructed
field lookup; Agent conversion snapshots. Baseline fixtures cover small and wide
operands, empty through 256-element wire arrays, and sorted/unsorted constructed
objects with early/late/missing fields. Retention and error ordering remain part
of acceptance, not inferred from lower allocation counts.

Implementation and measurement results will be appended per candidate.

The baseline archive is `cabd6b04-source-c6c398515bf0` (`backend-p0`). No Go or
pprof process was present at the initial environment check. Single-iteration
fixture execution verifies setup only; its timings are not performance evidence.

## Operand workspace experiment

The first combined allocation/evaluation helper is rejected (`backend-operands`).
Ten alternating 20,000-iteration rounds find no allocation saving for two operands
and one additional allocation for wider operands and existing merge/boolean
consumers. Escape analysis confirms that returning the caller's slice through
the recursive evaluator moves each local array to the heap. `backend-operands-micro`
and `backend-operands-escape.log` retain this failure.

The corrected `backend-operands-local` shares only storage selection and no longer
adds heap objects. Its ten-round confirmation, combined with all original samples,
still regresses raw array length and same-input scope timing. It is rejected.

The final narrower experiment restores existing merge/boolean code and changes
only integer, expression-list join and usage consumers. Ten alternating 100,000-
iteration rounds plus the one allowed equal-size confirmation save one allocation
on two-operand cases, but still show stable 0.8–3.4% timing regressions in raw array
length/join controls. Both measurements and combined comparison are retained in
`backend-operands-narrow-{micro,confirm}`. No further confirmation is attempted.
All operand production changes are restored to P0; the failure-order regression
remains. These results do not justify accepting a change merely for fewer bytes.

## Wire array validation

The first array experiment factors member scanning into the existing public and
scalar consumers. Although wire validation improves, raw scalar length becomes
3.6–12.2% slower; `backend-arrays-micro` retains this rejected experiment against
its frozen operand parent. The public/scalar readers are restored.

`backend-arrays-only` changes only the array branch of the wire shape checker.
It reads validated immutable spans through the existing `valueReader.valueEnd`,
including its bounded large-subtree index, without a temporary member slice.
The full resource check still precedes shape traversal and failure diagnostics
still come from the existing schema validator. No values escape validation.
Protocol tests pass. Ten alternating 30,000-iteration rounds against P0 have no
stable timing/bytes/objects regression in wire-array and scalar control fixtures.
The five-pair HTTP batch (`backend-arrays-http`) completes all 40 runs with exact
output/upstream-call checks and no failed requests. Short/tools/SSE/cross-SSE have
no stable CPU, latency, first-frame, RSS or natural one-second heap regression.
CPU ratios are 0.9611/0.9907/0.9966/0.9863, all inconclusive; only tools throughput
has a small measured improvement (0.77%). No general HTTP speedup is claimed.
The wire-array implementation is retained subject to final full-matrix acceptance.

## Constructed field lookup: rejected

The early-exit candidate preserves duplicate/invalid-Unicode last-wins behavior
and passes encoded-key parity tests. Ten alternating million-iteration lookup
rounds (`backend-fields-micro`) nevertheless regress single-field reads by about
8–9% and sorted late/missing reads by up to 103%. Extra conditions and ordering
comparisons cost more than the eliminated scans in these cases. The production
change is restored; the independent encoded-key regression remains. This clear
failure needs no confirmation or HTTP run. `backend-fields` and the initial
`backend-agent` archive contain this rejected code and are not final candidates.

## Broader backend review

| Area inspected | Decision |
|---|---|
| Input views and Spec checks | Successful immutable-input/resource checks are already reused; do not duplicate that optimization. External and core lifecycle checks have different subjects. |
| JSON transport and Native | Keep owning copies that detach small fragments or outgoing wire from larger backing storage. Prior borrowed-transport counterexample still applies. |
| Routing cache and limiter | Keep generation checks around asynchronous loads, per-key permissions, and idempotent quota release; they handle concurrent changes, not unreachable fallback. |
| Outbound and relay | URL validation and dial-time address checks concern different facts. Keep both, stream idle deadlines and operation-specific redirect rejection. |
| Storage and usage writer | Keep transaction snapshots, generation guards, bounded queue fallback and persistence retries. Failures/shutdown/reset make these paths reachable. No database/schema tuning without a measured bottleneck. |
| Logging | No-quote redaction shortcut and disabled-body early exits already exist. Keep truncation/ownership and malformed-input redaction; no logging behavior change. |
| Refresh, health and shutdown | Existing owner lifetimes, coalescing and cancellation cover real concurrent transitions. No extra abstractions or speculative removal. |
| Agent | Reuse a checked candidate and the existing conversion snapshot instead of repeated binding validation, separate binding/policy reads and recompilation. Verify in-flight isolation and later revocation. |

This is a targeted implementation/caller review across backend subsystems, not
a claim that every backend source line was audited. No unrelated naming or
formatting changes are included.

## Agent conversion preparation

The Agent now prepares its checked candidate from the same immutable conversion
snapshot used to resolve policy. It reuses the existing bounded compiled-plan
cache, while each call retains its own provider evidence, diagnostics and execution
state. Execution copies are created after releasing the cache lock. No new cache,
pool or supplier-specific path is introduced. The original Agent transport
preference is preserved, including compatible SSE/NDJSON operation selection.

The gateway changes have five alternating pairs for short/tools/SSE/cross-SSE in
`backend-agent-http`, against `backend-arrays-only`. The first cross-SSE natural
one-second heap result is a regression (ratio 1.297, interval 1.078–1.650).
The single allowed equal-sized confirmation is retained in
`backend-agent-http-confirm`; combining all ten pairs gives 1.156, interval
0.932–1.424, inconclusive. No metric has a stable regression in the combined
comparison. This is not evidence of reduced retained memory, and no further
confirmation is taken for this comparison.

`backend-agent-calls` and `backend-agent-logged` retain the initial complete Agent
fixture measurements. Both include a local HTTP provider and synchronous usage
persistence in the measured process, so their CPU is not gateway-only. Eight
warmup calls precede 128 measured calls in two-turn histories; every call checks
text, usage, history, exact upstream count and exactly one persisted usage row.
Final Agent acceptance uses a fresh frozen source after preserving the original
transport preference; initial samples are not silently relabelled as that source.

In-flight tests hold the actual provider response while changing either binding
revocation or request conversion policy. The pinned call completes, while the next
call observes the update without reaching upstream. The policy test initially
used a response-rewrite rule, which the Agent's core collector intentionally does
not execute. That fixture assumption was corrected to a request rejection;
production response semantics were not changed. The initial full-test failure is
retained in `backend-final-checks/go-full`; the corrected test passes server race.

The final source is `8e6215e8-source-12803acec795` (`backend-final`), containing
only wire-array and Agent production changes. `backend-final-agent` and
`backend-final-agent-logged` each have five alternating pairs against the accepted
array parent. Both show stable CPU, latency and allocation improvements, without
a stable regression. Natural recovery heap remains inconclusive.

| Complete local Agent fixture | Before ms/call | After ms/call | Before B/call | After B/call |
|---|---:|---:|---:|---:|
| Array parent, body logging off | 8.114 | 1.287 | 7,180,130 | 133,932 |
| Array parent, body logging on | 8.714 | 1.492 | 7,217,461 | 170,644 |
| P0, body logging off, combined ten pairs | 8.487 | 1.298 | 7,182,104 | 133,932 |

The initial `backend-final-agent-p0` natural heap comparison regresses (ratio
1.480, interval 1.176–2.160). Its one equal-sized confirmation in
`backend-final-agent-p0-confirm`, combined with all ten pairs, gives ratio 1.204,
interval 0.992–1.488: inconclusive. The median still increases; memory equivalence
or improvement is not established. No third batch is taken. Combined CPU falls
from 10.559 to 0.916 ms/call, objects from 25,407 to 2,181. GC pause is below this
fixture's measurement resolution. All raw runs and both initial decisions remain.

The Agent changes are retained under the agreed no-stable-regression criterion,
subject to the final gateway matrix. Corrected server full tests pass in
`backend-fixture-checks` (171.7 seconds for the command). The original complete
Go run passed every other package and failed only the corrected test assumption.
Vet, staticcheck v0.8.2 (zero diagnostics), related protocol/server/backend race,
both generation checks, source inventory and measurement-tool tests pass in
`backend-final-checks`. These checks ran separately from performance windows.
