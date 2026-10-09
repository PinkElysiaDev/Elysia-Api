# Cache usage contracts and focused verification

[中文](cache-usage-contracts.md) · [Live report](cache-validation-2026-10-04.en.md)

A cache hit, an observable counter, and faithful forwarding are separate claims. Missing is not zero. Accepted fields do not prove a hit or a TTL's time behavior.

| Protocol | Read counter | Creation counter | Total input |
| --- | --- | --- | --- |
| Chat | `usage.prompt_tokens_details.cached_tokens` | `usage.prompt_tokens_details.cache_write_tokens` | `usage.prompt_tokens`, already inclusive |
| Responses | `usage.input_tokens_details.cached_tokens` | `usage.input_tokens_details.cache_write_tokens` | `usage.input_tokens`, already inclusive |
| Anthropic | `usage.cache_read_input_tokens` | `usage.cache_creation_input_tokens` | Raw `input_tokens` + read + creation |
| Gemini | `usageMetadata.cachedContentTokenCount` | No equivalent generation counter | `usageMetadata.promptTokenCount` |

Chat/Responses retain legacy top-level `cache_creation_input_tokens` input support. Conflicting simultaneous creation counters fail explicitly, as do negative, noninteger, null or overflowing values. Absence and explicit zero remain distinct. Newly generated usage uses the nested field; unchanged compatible native responses retain their original form. Semantic edits/deletions reconcile recognized aliases so stale Raw cannot restore a removed count. A Gemini resource's creation token count measures resource size, not generation cache creation. When the target has no field for a creation count or detail, the converter diagnoses and omits that detail rather than hiding it or deleting counts to force the conversion through — see the next section.

## Cross-protocol cache-creation bucket projection

Every Anthropic response that wrote cache carries `cache_creation.ephemeral_5m_input_tokens` and `ephemeral_1h_input_tokens`, a provider TTL breakdown that is bookkeeping rather than a requested capability. No non-Anthropic target has a field for it.

- **Named compatibility projection**: the default compatible policy uses `usage_projection` to omit unrepresentable buckets while the **creation total still reaches `cache_write_tokens`** (Chat/Responses). Strict mode rejects the loss. With the rule disabled, the encoder rejects unprojected fields.
- **Visible omission**: the omission raises a `SeverityWarning` at `/usage/details/ephemeral_5m_input_tokens` (or `ephemeral_1h_input_tokens`) that is drained into the record's ConversionIssues beside a successful response, so it is an explicit omission rather than a silent drop.
- **Deterministic**: details are traversed in sorted key order, so the same input reports the same path every run; previously the rejected key depended on map iteration order and could differ between runs.
- **Same-protocol preservation**: an unmodified Anthropic→Anthropic roundtrip retains both buckets fully, with tools, system and message positions unmodified.
- **Gemini creation total**: Gemini reports cache reads only and has no creation counter; the creation total is likewise omitted with a warning at `/usage/cacheCreation`, while reads cross normally as `cachedContentTokenCount`.

Since `dev.22`, combination verification compares the projected semantics, without family-based omissions in the comparator. Dropping cache creation for Gemini first validates the original arithmetic and removes its dependent uncached subtotal from the client copy. `ProtocolUsage` retains the original counters; target stream replay cannot overwrite accounting.

**Open verification gap**: a nonzero cross-protocol cache read has not yet been obtained on the live site; local tests cannot substitute for it — see the [live report](cache-validation-2026-10-04.en.md).

## Declaring a provider alias

The [mapping patch](examples/cache-usage-alias.mapping.json) and [sample](examples/cache-usage-alias.sample.json) illustrate a **synthetic** contract, `usage.provider_metrics.write_tokens`. They are not complete definitions or an interpretation of moyuu billing fields. Use a real alias only after establishing its contract and observing evidence.

1. Copy the Chat definition under a new ID, retaining capabilities, operations, samples, directions and extensions.
2. Merge the patch's `decode_response.after` and `decode_event.after`. If an `after` already exists, explicitly compose transformations and test them instead of overwriting it.
3. Append the sample and add actual JSON, stream, zero, missing and conflicting-field cases. The example gives standard semantic counters priority, including explicit zero, and reads the alias only when standard creation is absent.
4. Verify, preview, save the draft, activate the exact revision, then check binding and forwarding.

Agent uses the same service: `elysia protocol draft`, `validate`, `verify`, `preview --direction decode_response --sample ...`, `save`, and `activate --id ... --hash ...`. Definition edits invalidate old reports. Compiler changes use existing startup reverification, without overwriting user edits with presets.

If an old sample expected `cache_write_tokens` as an unknown extension, update that field's assertion to `expected.usage.cacheCreation={"count":originalValue,"origin":"observed"}`, including zero. Keep the original inclusive input total. Adjust only the consumed field's old extension path, retaining unrelated extensions and every sample. Preview the complete semantic diff and reverify; do not remove failing samples to bypass version validation.

`module` decodes the base wire format. Within `after`, `input` is semantic content and `root` is the original response/frame. Event transformations operate on the current frame's events. Native replay requires both decoder and encoder compatibility, including referenced expression implementations. A shared family or ref name is insufficient. Other unmapped extensions still receive `unsupported_native`; deleting extensions is not a valid substitute for compatibility.

`TestCacheAliasEditorAgentAndForwardingShareContract` checks editor management APIs, Agent, preview, activation, JSON/SSE and SQLite together. This is an offline service integration test, not browser E2E or a live provider-alias claim.

## Bounded live experiments

Run `node scripts/verify-protocol.mjs live --suite=cache-gaps`. Configure models and credential environment-variable **names** per target; pass secret values only to the child process, never command arguments or files. The new ledger permits 96 calls in two hours, reserves four cleanup calls, and does not reset the old 256-call ledger. Failures, probes and resource operations count too.

`live --suite=cache-gaps --resume=<evidence directory>` resumes checkpoints bound to model, compiler and revision. Uncertain paid attempts are not replayed; warmed TTL groups retain their original schedule. `live --suite=cache-followup --parent=<directory>` waits for the parent, shares its deadline/ledger, and permits a single allowance of at most twenty reserved follow-up calls.

Anthropic TTL is a minimum lifetime refreshed by reads. A hit after a quiet period beyond TTL means expiry was not observed, not that the gateway failed. Gemini resources are checked against server `expireTime`; only explicitly enabled tests create/query/reference/expire/clean them, and only this run's resources are eligible for cleanup. Rejected resource endpoints stop that experiment without retrying as inline text.

Reports separate raw usage, billing extensions, selected contracts, client/SQLite snapshots, aggregate statistics, hashes and actual timing. Production capture defaults are unchanged. Backend/race, fuzz and local performance runs consume no real-call budget.
