# Atomic file write migration

Tracking: [#50](https://github.com/PinkElysiaDev/Elysia-Api/issues/50). Commit branch: `refactor/atomic-file-writes`.

## Decision and scope

Use **`github.com/marwatk/atomicfile` v1.0.2** behind `backend/internal/atomicfile`. The library supplies exclusive temporary-file creation, cleanup and platform-specific replacement (`MoveFileExW` on Windows, `rename` on Unix-like systems). The local adapter applies the requested mode and calls `Sync` before the library commits the replacement because this library's `New` mode argument is not applied and its `Close` does not sync.

Selection review: `google/renameio/v2 v2.0.2` has a sync-aware Unix implementation but excludes Windows from its atomic API; `natefinch/atomic v1.0.1` provides Windows replacement but its complete write API inherits destination permissions instead of accepting the explicit mode needed here; `creachadair/atomicfile v0.4.2` does not expose the underlying file for the required sync and delegates replacement to `os.Rename`. The selected library exposes the pending file and a cross-platform replacement operation, allowing local permissions/sync policy without reimplementing staging or platform syscalls.

Migrated application-owned paths:

- configuration updates and first-run default configuration;
- generated database encryption-key persistence;
- model-catalog cache publication;
- validated protocol snapshot publication, using the adapter's `ReplaceFile` for the already-synced SQLite snapshot.

All callers use `internal/atomicfile.WriteFile` directly. The former `config.WriteFileAtomic` forwarding wrapper has been removed, so asset persistence no longer depends on the configuration package. Ordinary test fixture writes and SQLite database creation are not converted because they are not application state publication.

## Guarantees and boundaries

- Data is written to a unique file in the destination directory, synced, and only then replaces the destination.
- A write, mode, sync, close or replacement failure leaves the existing regular destination unchanged; temporary files are cleaned best-effort by the library.
- Explicit POSIX permissions are applied. On Windows, `os.Chmod` retains its platform-defined read-only-bit semantics.
- Replacing a symlink replaces the directory entry rather than following it on the tested platforms/library implementation.
- Concurrent readers see a complete old or new file, never a partially written file. Concurrent writers publish complete versions; last successful replacement wins.
- The protocol snapshot still validates and hashes the temporary SQLite database before publishing it. Its existing reference cleanup remains in place.

The adapter **does not claim parent-directory durability after power loss**. File contents are synced before rename, but no directory `fsync` is performed. A successful call means the replacement operation completed according to the operating system/library; it is not a journal or durability protocol.

Replacement of a directory target fails at publication and the library removes the staging file. It does not add locking or compare-and-swap semantics; callers that need conflict control retain their existing higher-level transactions. Windows may reject replacement if another process holds the destination open without compatible sharing flags. Windows was cross-compiled, not executed; the continuous-reader stress and symlink tests are POSIX-specific.

### Compatibility changes

Config updates already replaced the target entry. First-run config and generated-key writes previously used `os.WriteFile`, which followed symlinks and retained an existing file's permissions. Those paths now replace the entry and enforce `0600`; in particular, regenerating an empty permissive key file tightens its permissions. A nonempty existing key is still read without replacement. Bootstrap/key generation still assumes the existing single-owner startup model; atomic replacement does not introduce interprocess create-if-absent arbitration.

Catalog writes now report replacement failures through `lastError` (previously rename failures were ignored), and use a unique staging filename instead of `path + ".tmp"`. The catalog uses explicit `0644` for the completed file. Initial key/config and cache persistence now also sync contents before publication. These policy changes are documented in the PR; no database or public JSON shape is changed.

## Benchmark

Linux/amd64, Go 1.27.2, AMD Ryzen 7 8845H, `GOMAXPROCS=1`, three 200 ms samples. A temporary `before` helper reproduced the removed local implementation (same-directory temp file, write, file sync, chmod and rename); `after` used the shared adapter. Each iteration replaced the same destination and included temporary-file cleanup. The historical helper was removed after measurement; the retained benchmark runs the current adapter only. To reproduce the baseline, extract `config.WriteFileAtomic` from `36d997b`, rename it to `WriteFile` in a temporary file with `os`/`path/filepath` imports, and overlay that file onto `internal/atomicfile/write.go` for this package-only benchmark. The fixture and timing settings remain the same.

| Payload | Before ns/op | After ns/op | Before B/op | After B/op | Allocs before → after |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 256 B | 34,146 | 33,511 | 1,064 | 759 | 15 → 10 |
| 64 KiB | 47,252 | 53,309 | 1,079 | 759 | 15 → 10 |
| 1 MiB | 667,933 | 1,818,019 | 1,079 | 760 | 15 → 10 |

Values are representative local measurements and can vary substantially with filesystem cache and sync behavior. The migration's goal is centralized platform behavior and cleanup, not a speed claim. The adapter reduced allocation count in this run, while larger writes were slower; it keeps the required file sync and explicit mode handling. The 1 MiB after median is noisier than the other samples because each iteration includes a filesystem sync.

Reproduce from `backend/`:

```sh
GOMAXPROCS=1 go test -tags=sonic ./internal/atomicfile -run '^$' \
  -bench '^BenchmarkAtomicWrite$' -benchmem -benchtime=200ms -count=3
```

## Validation

```sh
go test -tags=sonic -race ./internal/atomicfile ./config ./storage ./server \
  -run '^Test(WriteFile|Config|AgentSettingsSave|EnsureConfig|Bootstrap|DBEncryption|Secret|ModelCatalogCache|Catalog|ProtocolSnapshot|SnapshotCleanup|RuntimeConfigAtomic)' \
  -count=1 -timeout=3m

GOOS=windows GOARCH=amd64 go test -tags=sonic ./internal/atomicfile \
  -run '^$' -c -o /tmp/opencode/atomicfile-windows.test.exe
```

Results: `internal/atomicfile` **1.015s**, `config` **1.070s**, `storage` **1.570s**, `server` **2.426s**, all PASS under race detection. Windows cross-compilation passed. The tests cover target/creation failures, temporary cleanup, explicit modes, symlink entry replacement, concurrent complete-version readers, model-catalog fixed-temp-file isolation and protocol snapshot paths. Disk-full/sync fault injection and Windows runtime behavior were not tested.

An additional key-regeneration regression passed under race detection (**1.043s**): an empty existing key file is replaced with a stable generated key and private permissions, subsequent reads return the same key, and failed persistence returns no key.

The full backend suite was not run.
