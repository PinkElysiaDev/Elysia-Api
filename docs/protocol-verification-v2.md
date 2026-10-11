# Protocol v2 verification

Offline verification uses the shared compiler and typed runtime. It does not activate a registry entry or send network traffic. The revision service attaches these
results to persisted revisions and the activation service.

## Evidence and activation

`protocol.Verify` runs each declared sample through the actual request,
response or event adapter. Samples contain input and either expected output
or one expected diagnostic. Positive samples are compared as JSON; semantic
comparisons remove framework identity/native bookkeeping, not payload fields.
When an inverse direction exists, the verifier also checks semantic roundtrip.
With native replay enabled, it checks wire values and array order against the
original input. Native-extension declarations additionally receive an engine
probe containing null, false, zero, a long integer and nested empty containers.

Every implemented direction needs a passing positive sample. Negative tests
cannot substitute for capability evidence. Each declared capability needs
actual semantic content in the applicable direction; merely listing the
capability in a sample is insufficient. Function/free-text tool request
support also needs a full definition → associated call → result fixture.
Declarations can restrict capabilities per direction using
`directions.<direction>.capabilities`. An explicit empty object means no
capabilities; omission inherits the definition-level set.

Event directions require at least one complete `sequence: true` fixture.
`EventReplay` uses the same bounded stream rules for JSON/text tool input,
item/call association, cumulative text, sequence deduplication and usage tails.
Missing terminal events, malformed completed tool arguments, conflicting
identities, content after completion and changed cumulative prefixes fail.
Cancellation/error terminals can leave partial tool input. Usage arriving
after a terminal remains visible, including explicit zero counters.

Reports include normalized definition hash, sample-set hash, compiler version,
verification kind, per-sample checks, observed capabilities and located issues.
Changes to configuration, samples or compiler semantics invalidate prior
evidence. `CanActivate` accepts only a current passing **offline** report; a
real-provider report cannot replace this prerequisite. The revision service creates reports
through the shared service, rather than trusting a client-supplied `passed`
field.

## Conversion combinations

`VerifyCombination` checks ingress request → upstream request, upstream
response → ingress response, and complete event sequences when both event
directions are implemented. The report binds both definition hashes.
Unsupported request capabilities remain explicit failures: a text-only target
does not receive a request with its tools removed.

Complete adapters permit independent reverse decoding of the translated
payload. A partial adapter instead relies on its positive expected-wire
fixtures for the unpaired direction. Reports provide finite fixture evidence;
they are not a mathematical proof of behavior for every possible input.
Runtime schemas, capability checks and event state validation remain required.
Registry and route binding must not treat a compiled definition alone as
verified, or infer successful online behavior from an HTTP 200 status.

## Regression checks

Test declared capability coverage, full tool histories, semantic deletion, negative-only suites, native preservation and stale-report activation. Replay checks include split arguments, late identities, missing terminals, cumulative rewrites, cancellation, bounded buffers and usage tails. Editor, Agent, sessions and jobs use the same verification service with their own lifecycle fixtures. Commands and evidence boundaries are in [validation](protocol-release-readiness.en.md).
