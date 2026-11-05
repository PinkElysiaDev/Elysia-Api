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
