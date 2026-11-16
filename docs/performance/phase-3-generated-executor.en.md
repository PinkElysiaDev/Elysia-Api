# Handoff: phase 3, generating the executor

Status: idea-stage, deliberately not a runbook. Written 2026-11-16.

This document is direction, not instructions. Phase 3 is the largest and least certain
change in the project; writing it as a fixed sequence today would be pretending to a
confidence we have not earned. What follows is the shape of the thing, the ideas worth
trying, what will block them, and the questions that must be answered before any code is
written.

Prerequisite: phases 1 and 4 are landed and measured. Phase 3 over a pointer-heavy
document produces generated code that still chases pointers through a dozen walks — it
compounds only over a compact representation.

---

## The idea in one line

Today the runtime interprets a `compiledOutputPlan` tree once per request. Phase 3 would
instead emit straight-line Go, once per compiled Mapping, and call it per request.

The analogy is a regex engine: an interpreter walks a node tree per match; a compiler emits
a matching function per pattern. Same language, same semantics, very different constant
factors and very different failure modes.

## Why it is the only step that could reach the goal

Measured, not assumed: interpreter dispatch is 2.74% (`buildNode`) + 1.90%
(`buildOperation`) flat, and the string switch inside it is ~1.5 ns because Go already
compiles it into a length-bucketed jump table. Removing the switch alone recovers 1–2%.
So the claim is *not* that interpretation is slow because of a switch.

The claim is that per-node interpretation forces choices the compiler cannot optimise away:

- Every node boundary pays a function call, a prologue, an `advance()` step check, an
  error-boxing path, and a result struct return — even for `literal`, which is a constant.
- The tree shape is only known at runtime, so the compiler cannot inline children, keep
  values in registers, fuse adjacent reads, or prove a subtree allocation-free.
- Operands are built into generic `[]plannedOutput` arrays and re-inspected, rather than
  living in named locals with types fixed at generation time.

Generated code can do all of those things. Whether it *does*, in practice, is the open
question this document cannot answer without a prototype.

## Shape: what gets generated

One function per program per Mapping: `encode_request`, `encode_response`, `encode_event`.
Generated at the same point the current plan is compiled, cached on the same revision
identity, so activation, verification and hot-reload semantics do not move.

The function signature stays close to the current entry — it takes the semantic document
and the per-request evaluation state, returns the planned output or an error. What changes
is the body: no tree, no dispatch, no generic operand array.

Keep the interpreter. It is the reference implementation, the differential-testing oracle,
and the fallback for programs the generator declines to handle. Deleting it is a later
decision, made on evidence, not a requirement of the design.

## Ideas worth trying, roughly in order of expected value per unit of risk

**1. Constant folding at generation time.** `literal` nodes, statically-resolvable
pointers, and unconditionally-selected branches can collapse to constants or direct
accesses. This is free of semantic risk and probably the cheapest win available.

**2. Named locals instead of operand arrays.** `buildOperands` currently materialises
`[]plannedOutput` (4-slot stack array, heap fallback) and then re-inspects. Generated code
can hold each operand in a typed local, which also removes the escape we already fixed in
`buildIterationOutput` and the remaining `make([]JSONOutput, len(values))` allocation.

**3. Direct struct field access for statically-known pointers.** The plan already parses
pointers at compile time and `expression.pointer` is static. A generated function can read
`request.Content[2].Payload` rather than walking a pointer path per request.

**4. Fusion across parent and child.** `object` with `read` children, `array` with `map`
bodies — these are the shapes the 12 walks come from. Generated code can inline the child
into the parent's loop, which the interpreter structurally cannot.

**5. Merging meters into the walk.** Byte/node counters are separate passes today. A
generated function can accumulate them as it goes, which is the same idea as phase 4's
epoch but done structurally rather than by caching a comparison.

**6. Specialising on document shape at generation time.** If the Mapping's structure
proves a program only reads `Content` and `Tools`, generated code can skip the rest of the
document entirely rather than walking a general visitor.

## The hard part, stated plainly

Everything the interpreter does *generically* must be reproduced *per program*:

- Every error message and its exact `path`, with first-error ordering preserved.
- All resource-limit checks and their reported values (`Nodes`, `BufferBytes`,
  `walkNodes`) — including the order in which they trip.
- The `advance()` step limit.
- Absent versus null versus zero, and field ordering.
- `Value` raw-byte preservation, including the span-identity caches.

That is the whole risk. A generator defect is a silent semantic difference, not a compile
error. There is no type system check that says "your generated encoder now drops an absent
field." Only the corpus catches it.

## Open questions to answer before writing code

1. **Generate at build time or at package-activation time?** Build-time generation means
   shipped packages have code ahead of time but custom user packages need a compiler at
   runtime — and the whole point of the Spec/Mapping format is that users author packages
   without a Go toolchain. This probably forces activation-time generation, which makes the
   generator a runtime dependency and changes the startup cost story. Settle this first;
   it dominates the design.
2. **What is the size and startup cost of generated code per package?** Roughly a hundred
   operations, four packages, several programs each. Is the generated source compiled to a
   binary, or is this a specialised interpreter with a flattened program? The answer
   decides whether this is codegen at all.
3. **How does differential testing work in production, not just in tests?** Keeping the
   interpreter as an oracle is easy in CI. Is there value in a sampled production
   comparison, and what does it cost?
4. **What happens when the generator declines a program?** Every Mapping that uses an op
   the generator does not support needs a defined fallback, and that fallback must be
   tested, or the "some programs get faster" property becomes "some programs differ."
5. **Is the win real?** Prototype one op family — `object` with `read` children, which is
   the most common shape — and measure against the interpreter before committing to the
   rest. If that prototype shows less than a meaningful multiple, stop and report; phases 1
   and 4 will have been the right investment anyway.

Question 5 is the one that matters. Prototype before designing further.

## Alternatives worth weighing against it

**Flatten the program instead of generating Go.** Compile the plan tree into a flat
instruction array with precomputed operand slots. Removes the tree walk and most dispatch
without any code generation, keeps a single runtime implementation, and avoids the
"generated code is a second semantics" problem entirely. Less upside, far less risk. If
phase 1's arena makes walks cheap, this may capture most of the remaining win.

**Specialise the hot ops only.** `object`, `read`, `array`/`map` and `literal` probably
account for the majority of nodes. Hand-writing fast paths for those inside the interpreter
is a fraction of the work and needs no generator.

**Leave interpretation alone and attack the passes.** Phases 1 and 4 already target the
walks. If they deliver well, the remaining interpreter overhead may not justify a rewrite.

Honest expectation: on the measurements we have, "flatten" and "specialise hot ops" are
the two options most likely to deliver most of the win for a fraction of phase 3's risk.
Phase 3 is worth attempting only if the prototype in question 5 shows a large gap over both.

## Gates, if it proceeds

- Corpus byte-identical across every case, including error paths and issue ordering.
- Differential run against the interpreter over the full corpus and a generated input
  sweep.
- Interpreter remains reachable behind a build tag or config switch until the corpus and a
  production soak both pass.
- Per-scenario measured improvement, reported individually, not aggregated.
- No regression in any scenario; a phase that trades one case for another has not succeeded.

## What is not promised

No throughput projection. The only measured numbers behind this document are the dispatch
shares (2.74% / 1.90% flat), the per-node cost (5.8–8.3 ns), call volume (~1,400 per short
request, ~40,000 for `history256k`), and the round-2 A/B result. Whether code generation
reaches 4x–13x is unknown and unmodelled; the prototype in open question 5 exists to answer
it cheaply before the expensive commitment.
