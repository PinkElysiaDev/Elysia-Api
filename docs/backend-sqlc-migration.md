# Incremental sqlc migration

Tracking: [#50](https://github.com/PinkElysiaDev/Elysia-Api/issues/50). Commit: `7cb2ebc`.

## Scope

This is the first small `sqlc` slice, not a conversion of all storage code. It adds **sqlc v1.31.1** generation configuration and generated SQLite queries for:

- `ListGroups`
- `ListGroupMembers`
- `GetSetting`
- `SetSetting`

`ListGroups` now uses the generated queries inside one read-only transaction and builds the existing API model in a small adapter. The schema/query files live under `backend/storage/sql/`; the schema is embedded into runtime schema creation so the sqlc input is checked in one place rather than silently drifting from the application schema.

The generated package is internal and does not expose `sqlc` types through the storage API. Existing encryption, boolean conversion, empty-array behavior, disabled-source retention, unresolved member retention, and ordering remain owned by the storage adapter.

## Why this slice

`ListGroups` previously issued one membership query per group. The generated fixed queries provide a meaningful bounded migration target while removing the N+1 pattern. Dynamic filtering, transaction-specific writes, encrypted fields and JSON transformation remain hand-adapted until their query shapes and ownership boundaries justify separate slices.

This PR does not claim that every SQL statement should be generated immediately. It establishes reproducible generation and a runtime-schema compatibility test.

## Compatibility

No public API or schema change. The query uses a read-only transaction, so group metadata and member rows come from one SQLite snapshot. Member rows are still returned even when their source is disabled or the model no longer exists; editing an unchanged group therefore does not silently delete references. Empty `Models` remains `[]`, never `null`.

## Benchmark

Linux/amd64, Go 1.27.2, AMD Ryzen 7 8845H, `GOMAXPROCS=1`, three 200 ms samples. Fixture: each benchmark database contains groups with four ordered members; the operation result is the existing `ListGroups` API shape.

| Groups | Before ns/op | After ns/op | Before B/op | After B/op | Before allocs/op | After allocs/op |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 24,848 | 26,980 | 2,752 | 4,117 | 100 | 122 |
| 100 | 1,289,510 | 478,666 | 177,178 | 211,693 | 6,245 | 4,693 |
| 1,000 | 13,032,301 | 4,675,246 | 1,675,480 | 2,230,247 | 62,048 | 46,104 |

The 1,000-group case is approximately **2.8× faster** and removes roughly 16,000 allocation objects per call, while allocating about 0.55 MiB more. The one-group case regresses due to transaction/generated-query setup. This is a focused query-shape measurement, not an end-to-end storage or server throughput claim.

## Generation

The checked-in files were generated with the official release binary:

```sh
sqlc version # expected: v1.31.1
sqlc generate
```

The release archive SHA-256 was verified before use:

`497ae4fcdfa64c5b0c311ffe4c2bd991e43991e82e5367792ed78bc2dca27354`

The same generated output was obtained twice. CI/developer tooling should pin the same sqlc version rather than regenerate with an unspecified latest version.

## Validation

From `backend/`:

```sh
go test -tags=sonic -race ./storage ./server \
  -run '^Test(SQLC|ListGroups|Group|CreateGroup|AddGroup|RemoveGroup|DeleteGroup|SourceModelsGroupsTokensAndUsage|ModelGroup|UpsertGroup|RuntimeConfig|AdminListGroups)' \
  -count=1 -timeout=3m

GOMAXPROCS=1 go test -tags=sonic ./storage -run '^$' \
  -bench '^BenchmarkListGroups$' -benchmem -benchtime=200ms -count=3
```

Both focused package groups passed under race detection: `storage` **1.834s**, `server` **5.981s**. The generated-query/schema compatibility test exercises settings upsert/get, missing settings, group reads and member reads against the runtime SQLite schema.

## Next sqlc slices

Future candidates should remain separate and measure their own adapter costs: fixed protocol history reads, agent session/message reads, and fixed usage-log queries. Dynamic SQL, encrypted row mapping and aggregate filters should not be generated merely for consistency; they need an explicit query-shape and error/ownership review.
