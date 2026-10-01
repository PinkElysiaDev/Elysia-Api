# Tool fidelity correction (C02)

The missing `tools[4].name` is a renderer defect. The Responses preset consumes
the shared `tools` shape and cannot restore fields already discarded by it.
Allowing every Raw object with a `type` would introduce cross-wire leakage.

## Reproductions and changes

`relay/TestToolFidelity*` was run before implementation on C01 (`d77aac7`).
The failing output is retained locally at
`../.cache/protocol-v2-c02-before.txt` (relative to the repository root).
The committed test fixtures provide repeatable evidence without credentials.

| Trigger | Before | Correction |
| --- | --- | --- |
| Responses hosted/custom definitions | Only `type` remained | Parser-owned source identity authorizes native preservation |
| Chat flat function | Semantic name empty | Nested and flat forms extract the same semantic fields |
| Anthropic hosted definition | Reclassified as function | Native type retained; unsupported cross-wire targets fail |
| Nameless function | Invalid upstream request | Request parsing and rendering reject with field diagnostic |
| Rename/clear Responses tools | Old Raw/original restored old tools | Semantic name overlays Raw; cleared list removes original tools |
| Custom Responses shape + Chat choice | Nested function selector leaked | Shared target choice renderer |
| Responses custom call response | Free text input lost | Free text input stays separate from JSON arguments |
| Custom response mapping | Item ID could replace call ID | Call ID takes precedence; explicit aliases retain priority |
| Responses-only history/output sent elsewhere | Silently absent | Explicit unsupported history/output error |
| Native Responses stream containing custom/hosted tools | Renderer omitted events | Parser-identified same-wire events preserved |

The outside report's claim that flat Chat functions always lose their name on
the Responses wire was too broad: the previous Raw shortcut could preserve that
particular wire, while the semantic name was still missing for other targets.
These C02 regressions were verified against C01, not separately against v1.4.0;
the cache audit's historical classification must not be generalized to tools.

## Verification and interim boundary

HTTP fixtures exercise native Responses, the unchanged Responses preset and an
arbitrary-ID copy, including definition configuration, free text call/result
history, response call identity/input and nonzero cached usage. Native event
fixtures check event order, payloads, completion and absence of duplicated usage
completion frames. Cross-wire unsupported tests require errors.

The legacy custom stream DSL expresses JSON function arguments only. Known
free text and hosted tool events now fail explicitly instead of being filtered
out and followed by a successful completion. Reusing the full native event
adapter in custom definitions is part of C05/C06; this commit does not claim
that the legacy DSL gained that capability. Generic custom mappings preserve
the declared free text call semantics, not arbitrary native extensions whose
origin they do not declare.

No real-provider success or universal protocol compatibility is inferred from
these local fixtures. Source identity here is a temporary private parser field;
C03/C04 replace that boundary with the versioned provenance and update model.
