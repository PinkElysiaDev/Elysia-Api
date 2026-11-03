# Three-version performance investigation

The investigation pins the official Windows amd64 v1.7.0 asset, its source
revision `9b72829a`, historical A `b2844846`, and current `d7e29817` (runtime
implementation `f2b298f8`). Measurements and profiles use isolated application,
client and simulator processes; no existing database or log is opened.

The release checksum is
`06b878052d2cd9a16f1b7a0be2359257116f6d318945b1fd912645db48644970`.
Its embedded commit is `9b72829`, and its toolchain is Go 1.27.2. Rebuilt
executables use the same toolchain with PGO/CGO off and explicit source identity;
Git stamping is disabled in archived sources to avoid inheriting the enclosing
worktree's commit. Rebuilt backend archives retain tracked embed placeholders;
the release retains its shipped UI/source assets. This build difference is
recorded separately and no UI requests participate in the gateway comparison.

The production management API rejects replacing built-in preset IDs. Current
packages are therefore copied to `measure-*` IDs, with matching sample identity
references, then saved, verified and activated through HTTP. All versions use
their declared operation paths under the common `/gateway/:id/*path` route.
Legacy Chat declares `/chat/completions`; the package declares
`/v1/chat/completions`. Source origins follow those contracts. The current
package process retains four legacy presets plus four custom packages; previous
test-injected measurements replaced presets and are not mixed into this report.

`scripts/performance-versions.py` reuses the existing Go workload client,
deterministic provider and full-response assertions. The standalone process
has only OS CPU/working-set and existing admin heap/GC measurements; unavailable
allocation/heap-peak fields are not reported as zero. The matching test-only
process runs the normal server lifecycle and supplies TotalAlloc/Mallocs at
batch boundaries. No diagnostic endpoint is added to a production binary.

Evidence lives under `.tmp-dev/three-version-2026-11-04/`. Initial smoke failures
(read-only preset replacement, legacy endpoint selection, and inherited VCS
metadata) are retained. Formal measurements begin only after these adaptations
are fixed. Timing, profiling and diagnostic-counter runs are separate.
