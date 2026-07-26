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
  version, failure, and success paths.
- SHA-256 hashing.
- Common-root and traversal-resistant `os.Root` handling, including target-root
  and component symbolic-link rejection.
- Unicode-normalized, case-insensitive path collision detection.
- Strict VIPR header parsing, malformed metadata, unknown fields, and rejection
  of special file-mode bits.
- Differential-range validation and unreferenced-data rejection.
- libzstd patch creation and application through cgo.
- Immediate termination before decompressed output can exceed its declared size.
- Immutable source, target, installed-file, and patch snapshots, including
  optimized private-snapshot parsing without redundant full-file rehashes.
- Multi-file forward and reverse workflows.
- Independent forward/reverse readiness, including identical source and target
  states, permission-independent matching, missing files, non-regular files,
  mixed states, and unknown content.
- Transaction preparation, replacement, cancellation, rollback, cleanup, and
  injected rename, remove, and Unix permission-preservation failures.
- GUI state locking and captured selections during active operations.
- File-level and byte-level typed progress callbacks with distinct prepared and
  committed stages.
- Creator disk estimates, selected work directories, and bounded parallel file
  processing.
- Fuzzing of VIPR decoding, patch opening, and secure path joining.

CI runs the complete test suite with the Go race detector. It also rebuilds the
native packages with AddressSanitizer and UndefinedBehaviorSanitizer, runs
`go vet`, and executes `govulncheck` to identify known vulnerabilities that are
reachable from the current Go call graph.

CI enforces at least 80% statement coverage across the non-GUI core packages.
GUI packages are compiled by CI on Linux, and all release targets compile the
complete GUI and CLI executables on their native runner or supported multilib
toolchain.

A literal 100% line-coverage target is intentionally not used because it would
encourage platform-specific error branches and GUI toolkit internals to be
mocked without improving behavioral confidence. New changes should instead add
tests for every new behavior and failure mode they introduce.
