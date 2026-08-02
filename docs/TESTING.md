# Testing strategy

The repository combines unit tests, native integration tests, CLI end-to-end
tests, synchronized GUI-model tests, fuzz tests, native sanitizers, static
analysis, cross-architecture tests, and release-artifact validation.

## Local commands

```sh
make check
```

The test suite covers:

- GUI/CLI mode selection helpers.
- Shared command-line signal-context cancellation, parent propagation,
  idempotent shutdown, and notification cleanup through `internal/commandctx`.
- Hybrid and GUI-free CLI entry points, atomic creator file-pair parsing,
  required final positional arguments, help, version, failure, and success
  paths, including worker, memory, verification, durability, and I/O options.
- BLAKE3 streaming, chunked, and parallel hashing, accumulator finalization,
  agreement between streaming and positional-read implementations, overflow-safe
  chunk counting, and forced digest-table spill equivalence.
- Common-root and traversal-resistant `os.Root` handling, including target-root
  and component symbolic-link rejection.
- Unicode-normalized, case-insensitive path collision detection.
- Strict VIPR header parsing, malformed metadata, unknown fields, bounded
  file-entry decoding, signed file-size limits, method-specific invariants,
  rejection of old format versions, SHA-256 metadata, permission fields, and
  legacy `targetHint` fields.
- Differential-range validation and unreferenced-data rejection.
- Native V4 zstd compression and decompression, decoder-window bounds,
  positional I/O, source verification, session-resource limits, 32-bit offset
  handling, and apply-window/group round trips through cgo.
- Transfer of the setup session into the application pool, including ownership
  on constructor failure and exact session-budget accounting.
- Reuse of authenticated source chunks by SAME/COPY materialization and direct
  application of fully SAME canonical groups.
- Preferred verification ordering and partial cache reuse for COPY ranges that
  cross canonical chunk boundaries.
- Exact-window source verification for SAME/COPY and the optional fine-source
  digest extension for delta windows.
- Fine-band planning, native lookup, atomic verification, canonical fallback,
  mismatch rejection, and reuse of authenticated bytes.
- Reflink SAME-window verification without rewriting cloned extents.
- Compact transaction transitions, interrupted-tail replay, and recovery of
  legacy version-1 journal snapshots.
- Immediate termination before decompressed output can exceed its declared size.
- Canonical descriptor boundaries, sparse parsing and bounded application,
  COPY/ADD cut-point behavior, and deterministic source-candidate selection.
- Immutable creator snapshots and stable installed-file and patch identity
  checks without redundant full-file hashing.
- Multi-file forward and reverse workflows and independent readiness states.
- Replacement preparation, generated-file synchronization, cancellation,
  best-effort rollback, cleanup, parent-directory synchronization, and injected
  platform failure paths.
- GUI state locking, captured selections, serialized progress callbacks, and
  monotone weighted overall progress.
- Creator disk estimates, work directories, worker targets, operation-wide
  memory budgeting, scheduler behavior, source-cache policy, and transaction
  duplicate detection.
- Fuzzing of VIPR decoding, patch opening, native instruction handling, and
  secure path joining.

## Continuous integration

CI runs the complete suite with the Go race detector. A dedicated job rebuilds
the native packages with Clang AddressSanitizer and UndefinedBehaviorSanitizer
and enables leak detection. CI also runs `go vet`, Staticcheck, and
`govulncheck`.

At least 80% statement coverage is required across the non-GUI core packages.
The measured set explicitly includes `internal/commandctx`,
`internal/nativev4`, and `internal/workerbudget` in addition to the application,
CLI, hashing, patch, format, path, progress, and version packages.

The supported core set runs on Linux 386 and Windows amd64/386. Linux amd64
compiles all four executables. CI verifies that both CLI-only dependency graphs
exclude GUI packages.

After all correctness jobs succeed on a `master` push, native platform jobs
build both the hybrid and CLI-only release archives. A CI run is not successful
unless every expected archive was built and uploaded. The tag workflow later
promotes only artifacts from a successful push run for the exact commit; it does
not rebuild binaries.

A literal 100% line-coverage target is intentionally not used because it would
encourage platform-specific error branches and GUI toolkit internals to be
mocked without improving behavioral confidence. New changes should instead add
tests for every new behavior and failure mode they introduce.
