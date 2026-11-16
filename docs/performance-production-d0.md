# Measuring the production three-part runtime against D0

The `cutover` action in `scripts/allocation-optimization.py` compares the frozen
`backend-p0` default implementation with a separately frozen `C-production`
candidate. D0 is `cabd6b04-source-c6c398515bf0`, as defined in
[the original acceptance contract](backend-optimization-2026-11-05.md).
The historical `C-default` and `C-package` setup remains available unchanged.

The production branch uses the four actual default packages. It checks their
active and loaded revisions, every Spec/Mapping document against frozen source,
all six core directions, compiler version, offline activation evidence and the
selected route's conversion policy evidence. It checks these facts before load,
after reload and after measurement. It neither installs `measure-*` packages nor
changes the default mappings. Source metadata is created through `model-sources`;
the revision is pinned separately through `PUT /api/admin/protocols/bindings`.
The source JSON API has no `protocolBinding` member.

Ingress and upstream paths come from the candidate's actual Spec generation
operations. Current Chat ingress therefore includes `/v1/chat/completions`, and
its source base URL contains only the provider origin. The gateway proof records
`Execution=production`, its compiler, package revisions and verified policies.

The archived driver and provider retain their original `packages` fixture mode,
binary hashes and payloads. That environment value describes the fixture
processes; it does not label the measured gateway. The driver reads protocol
fixtures from its current directory, so **both sides** run it from D0's frozen
`C/source/backend/server`. The provider continues to run from the existing
`fixed-provider-build/source/backend/server`. Preflight verifies both source
archives, both D0 gateway binaries, the candidate binaries, and fixed driver and
provider identities. Changing fixture files or pointing at the mutable working
tree invalidates the comparison.

## Commands

Run these from the repository root. Wait until code edits, tests, race checks,
builds and profiles are finished. Freeze once, then run measurement phases
sequentially. Each stage and output label must be new; failed evidence is retained.

```powershell
python -B scripts/performance-production-test.py
node --test scripts/performance-production-compare.test.mjs
python -B scripts/allocation-optimization.py freeze --stages backend-three-part-final --reference backend-p0
python -B scripts/allocation-optimization.py cutover --stages backend-three-part-final --reference backend-p0 --label backend-three-part-final-standalone --phase standalone --rounds 5
python -B scripts/allocation-optimization.py cutover --stages backend-three-part-final --reference backend-p0 --label backend-three-part-final-counters --phase counters --rounds 5
```

`freeze` builds the candidate only, using the archived driver's identity from D0.
It does not rebuild D0, the driver or the provider. `cutover` requires all eight
scenarios and five rotating rounds: 80 windows per phase for one candidate.
Standalone and counter results remain separate. Each run retains source/binary
identity, exact commands, environment, active bindings, reload evidence, child
exit codes, driver working directory and raw measurements; the phase directory
also contains `cutover-preflight.json`.

| Scenario | Requests per window | Warmup |
|---|---:|---:|
| short | 4096 | 32 |
| history | 256 | 32 |
| tools | 1024 | 32 |
| burst | 4096 | 32 |
| stream | 128 | 8 |
| image | 128 | 8 |
| cross-json | 512 | 32 |
| cross-stream | 128 | 8 |

The runtime remains Go 1.27.2, `GOMAXPROCS=2`, `GOGC=100`, `GOMEMLIMIT=2GiB`,
`CGO_ENABLED=0`, `GOTOOLCHAIN=local`. Streams retain 256 frames of 256 bytes;
media retain 2 MiB; each window retains one repeat and 1000 ms natural recovery.
The fixed driver checks output and exact upstream request counts.

Configuration can be checked independently of measurement, using the current
standalone executable and new temporary databases. This starts no driver and
sends no generation requests; the loopback upstream is only a synthetic address.
The script checks both short and cross-stream bindings, active identities and
reload equality, then closes each standalone process.

```powershell
python -B scripts/performance-production-smoke.py --output .tmp-dev/three-part-cutover/d0-config-smoke/run-02
```

After freezing a candidate, the archived driver can also check all eight actual
workloads with one setup request each and their original warmup counts:

```powershell
python -B scripts/performance-production-workload-smoke.py --stage backend-three-part-probe157 --output .tmp-dev/three-part-cutover/d0-workload-smoke/run-03
```

This runs sequentially and stops at the first failure. Its records explicitly set
`setupOnly=true` and `performanceAcceptance=false`; the production comparator
rejects them. Preserve these smoke outputs separately from full-count five-round
measurement windows. The fixed provider and driver use the same frozen binaries,
fixture directories and assertions as formal measurements.

## Comparison

Use the dedicated production comparator after both phases finish:

```powershell
node scripts/performance-production-compare.mjs --standalone=.tmp-dev/allocation-optimization-2026-11-04/backend-three-part-final-standalone --counters=.tmp-dev/allocation-optimization-2026-11-04/backend-three-part-final-counters --output=.tmp-dev/three-part-cutover/d0-production-comparison.json
```

Either phase may be compared separately by omitting the other argument. The
comparator requires five complete rounds of all eight scenarios for both D0 and
one production candidate. It rejects failed child processes, incomplete windows,
changed driver/provider/runtime/load identities, stale bindings, revision/policy
drift, missing observations, and mismatched source identities across phases.
It reads the unchanged `compareSamples` estimator; throughput direction is
reversed so larger values mean improvement. Unavailable standalone allocation
counters and JSON first-frame timing are marked separately. Zero-valued observed
metrics retain the estimator's `below-measurement-resolution` classification;
missing, negative or nonfinite observed values invalidate the evidence.

One optional confirmation directory per phase is accepted through
`--standalone-confirmation=DIR` or `--counters-confirmation=DIR`. Each must contain
another complete five-round matrix with the same frozen identities. All original
and confirmation observations are combined into ten samples per condition; no
original failure or slower window is filtered. The initial directory cannot also
serve as its confirmation; matching raw measurement hashes also reject copied
initial observations. Output files must be new. The historical
`performance-versions-compare.mjs DIRECTORY` CLI remains unchanged.

Harness unit checks exercise API configuration, stale-evidence rejection,
historical setup, immutable source checks and window order without launching
processes. They do not establish runtime compatibility or performance acceptance.
Apply the original 95% ratio-interval criterion to the collected measurements:
all stable regressions count; 5% only prioritizes investigation. At most one
equal-sized confirmation may be combined with all original observations. Preserve
`C-default` and `C-production` labels in all evidence and comparisons.
