# Scoped allocation optimization, 2026-11-04

Baseline Q0 is `104e4858`, production `8a019137`, frozen as
`104e4858-source-68f96a100690`. Its archive and four executables are under
`.tmp-dev/allocation-optimization-2026-11-04/scoped-q0`.
Official/rebuilt 1.7.0 is `9b72829a`; A is `b2844846`, not 1.7.0.
This report records work in progress; it is not final performance acceptance.

## Isolated parallel verification

`VerificationRun.executeTasks` reuses the existing process execution, timeout
and process-tree cleanup. Each task has its own report/log directory. A single
parent writes the aggregate report after completion. Tasks use at most four
slots; heavy builds/race use two. Matching groups exclude each other, preceding
dependencies block failed descendants, and exclusive tasks form measurement
barriers. Timing/profiling tasks never overlap another managed task.

`node scripts/verification/batch.mjs manifest.json` accepts `root`, `directory`,
optional `slots` (default four) and `tasks`. Each task has `name`, `command`,
`args`, optional execution `options`, `slots`, `group`, `after` and `exclusive`.
Paths and environment belong to each child; Python workers do not share mutable
`EVIDENCE`. Manifests and child reports retain commands and source identities.
Evidence directories are fresh per run; previous measurements are not overwritten.

The runner self-tests pass, covering failures, missing commands, timeout cleanup,
weighted overlap, group exclusion, exclusive ordering and failed dependencies.
Synthetic failure cases intentionally exit unsuccessfully. Production runtime
and performance settings are unchanged. No speedup is inferred from these tests.
