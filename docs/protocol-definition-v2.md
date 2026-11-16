# Protocol package execution

This URL remains for existing documentation links. The supported configuration
is a schemaVersion 4 package, with DSL2 Mapping and the Maheshvara core2 model.
The old Definition/Module language is retired. The [three-part contract](protocol-three-part-contract.md)
is the normative execution reference; the [cutover record](protocol-three-part-cutover-2026-11-14.md)
tracks completion and acceptance.

## Sources and the conversion hub

`spec.json` declares external documents, operations, constraints and stream
lifecycle. `mapping.json` declares that protocol's independent wire-to-core and
core-to-wire programs, native ownership, model policy, target dispositions and
samples. Both sources form one immutable package and one activation unit.

Maheshvara is the center of the conversion topology. A request from protocol A
to B executes A's decoder and B's encoder through the core. Responses and events
use the reverse pairing. No Mapping parses another provider's wire format, and
no supplier-pair converter bypasses the core. DSL operations are generic
mechanisms; they do not dispatch on provider names.

`CompileSources` and complete-package compilation use the same backend. A
request pins its compiled revision, limits, model and resource scope. Updating
configuration affects subsequent requests; it does not recompile or mutate an
in-flight execution.

## Validation and ownership

Spec checks the external input before Mapping decodes it. Core checks enforce
semantic associations, resource ownership and lifecycle. Target dispositions
handle only declared representation changes. Final wire validation applies
after encoding and any user wire changes. These checks concern different
objects; one cannot replace another merely because it ran earlier.

Missing, null, false, zero and empty containers remain distinct. Numeric values
are not routed through floating-point conversion. Unknown fields retain their
namespace and source ownership; undeclared cross-protocol use is rejected.
Native replay requires matching provenance, scope, semantic state and delivery
conditions, and cannot restore intentionally removed fields.

Streaming state belongs to one flow. Failed frames do not commit state or
delivery positions. A terminal must be observed, not fabricated from EOF.
Declared usage snapshots, tool fragments and protected continuation state keep
their distinct accounting and ownership rules.

## Configuration and evidence

The live catalog publishes package, spec, mapping, semantic and binding schemas,
the DSL operation catalog and generic conversion actions. It does not advertise
modules or the retired definition schema. Configuration objects are strictly
decoded, and authoring never installs executable code.

Every package supplies static examples of expected output and rejection.
Verification binds the package, compiler and sample identities; saving alone
does not activate a revision. Editor, Agent, preview, verification and actual
forwarding share this contract. Full fixtures live under
`backend/protocol/testdata/packages` and the default source pairs under
`backend/protocol/builtin/packages`.

The new execution chain is initialized in an independent database. Old
configuration, bindings, tasks and continuation state are not translated.
Existing databases, logs and credentials remain untouched. There is no
runtime fallback to the old engine. Functional, race, resource and performance
acceptance must complete before release; old revisions serve only as isolated
external performance references.
