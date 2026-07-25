# Testing strategy

The repository combines unit tests, native integration tests, CLI end-to-end
tests, and CI compilation of both GUI executables.

## Local commands

```sh
make test
make vet
```

The test suite covers:

- GUI/CLI mode selection helpers.
- Creator and patcher command parsing, including their required final positional arguments, help, version, failure, and success paths.
- SHA-256 hashing.
- Common-root and safe path handling, including symbolic-link rejection.
- Strict VIPR header parsing and malformed metadata.
- Differential-range validation and unreferenced-data rejection.
- libzstd patch creation and application through cgo.
- Multi-file forward and reverse workflows.
- Missing, mismatched, mixed, and already-patched preflight states.
- Transactional output preparation, replacement, and cancellation paths.
- File-level and byte-level progress callbacks.

CI enforces at least 80% statement coverage across the non-GUI core packages.
The current measured core coverage is above that threshold. GUI packages are
compiled by CI on Linux, and all release targets compile the complete GUI and
CLI executables on their native runner or supported multilib toolchain.

A literal 100% line-coverage target is intentionally not used because it would
encourage platform-specific error branches and GUI toolkit internals to be
mocked without improving behavioral confidence. New changes should instead add
tests for every new behavior and failure mode they introduce.
