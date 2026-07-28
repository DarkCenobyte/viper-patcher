# Testing strategy

The repository combines unit tests, native integration tests, CLI end-to-end
tests, synchronized GUI-model tests, fuzz tests, native sanitizers, and CI
compilation of both GUI executables.

## Local commands

```sh
make check
```

The test suite covers:

- GUI/CLI mode selection helpers.
- Atomic creator file-pair parsing, required final positional arguments, help,
  version, failure, and success paths, including the `--workers` option.
- BLAKE3 streaming, chunked, and parallel hashing, accumulator finalization, and
  agreement between streaming and positional-read implementations.
- Common-root and traversal-resistant `os.Root` handling, including target-root
  and component symbolic-link rejection.
- Unicode-normalized, case-insensitive path collision detection.
- Strict VIPR header parsing, malformed metadata, unknown fields, signed file-size
  limits, method-specific invariants, rejection of old format versions, SHA-256
  metadata, permission fields, and legacy `targetHint` fields.
- Differential-range validation and unreferenced-data rejection.
- Standalone libzstd compression and bounded segment decompression through cgo.
- Immediate termination before decompressed output can exceed its declared size.
- Canonical chunked-replace descriptor boundaries and per-frame BLAKE3 checks.
- Sparse parser cancellation, malformed streams, bounded producer/consumer
  application, and multi-chunk round trips.
- Immutable creator source/target snapshots and stable installed-file and patch
  identity checks, including optimized parsing without redundant creator-side
  full-file rehashes.
- Multi-file forward and reverse workflows.
- Independent forward/reverse readiness, including identical source and target
  states, permission-independent matching, missing files, non-regular files,
  mixed states, and unknown content.
- Replacement preparation, generated-file synchronization, cancellation,
  best-effort rollback, cleanup, parent-directory synchronization before backup
  removal, and injected rename, remove, synchronization, and Unix
  permission-preservation failures.
- GUI state locking and captured selections during active operations.
- Serialized byte-level progress callbacks, monotone weighted overall progress,
  and distinct prepared and committed stages.
- Creator disk estimates, selected work directories, centralized automatic worker
  targets, `--workers 0`, and atomic multi-unit COPY/ADD index-memory budgeting.
- Fuzzing of VIPR decoding, patch opening, and secure path joining.

CI runs the complete test suite with the Go race detector. It also rebuilds the
native packages with AddressSanitizer and UndefinedBehaviorSanitizer, runs
`go vet` and Staticcheck, and executes `govulncheck` to identify known
vulnerabilities that are reachable from the current Go call graph.

CI enforces at least 80% statement coverage across the non-GUI core packages.
GUI packages are compiled by CI on Linux, and all release targets compile the
complete GUI and CLI executables on their native runner or supported multilib
toolchain.

A literal 100% line-coverage target is intentionally not used because it would
encourage platform-specific error branches and GUI toolkit internals to be
mocked without improving behavioral confidence. New changes should instead add
tests for every new behavior and failure mode they introduce.
