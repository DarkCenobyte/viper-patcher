# Changelog

All notable changes to Viper Patcher are documented in this file. The project
uses Semantic Versioning. Release sections from `0.2.0` onward follow the Git
tags currently present in the repository; `0.1.0` records the initial project
version before the first retained tag.

## [0.5.1] - 2026-07-29

### Fixed

- Enforced canonical fixed 8 MiB descriptor boundaries for chunked replacement.
- Bounded sparse application memory with a producer/consumer queue whose retained
  plans scale with the worker target instead of the complete file size.
- Aggregated concurrent file and byte progress into one monotonic overall value.
- Synchronized prepared application outputs before replacement, then synchronized
  each affected parent directory before removing committed backups on Unix-like
  systems. Windows keeps the file flush and intentionally skips directory flushes.
- Made COPY/ADD index-budget reservations atomic so concurrent multi-unit requests
  cannot deadlock after partially consuming the shared 128 MiB budget.
- Connected Creator and Patcher CLI operations to Ctrl+C and SIGTERM so in-flight
  work follows the existing cancellation and cleanup paths instead of terminating
  immediately.
- Enforced 64-bit native POSIX file offsets and added positional compression and
  decompression coverage above 2 GiB, including Linux and Windows 386 CI targets.
- Reported generic Patcher CLI failures as application failures instead of
  incorrectly labeling decompression, write, or transaction errors as validation
  failures.

### Changed

- Renamed the CLI `--parallel` option and public option fields to a `--workers`
  logical scheduling target, with matching GUI and documentation wording.
- Centralized automatic worker selection on process-aware `GOMAXPROCS`, made
  `--workers 0` consistent across both CLIs, and bounded concurrently live
  COPY/ADD index arrays with a creator-wide 128 MiB budget.
- Replaced reflective COPY/ADD index sorting with typed stable sorting while
  retaining BLAKE3-256 keys and deterministic candidate order.
- Skipped Gear hashing before the first possible COPY/ADD cut point and preferred
  duplicate source chunks that merge with the pending COPY instruction.
- Clarified that replacement rollback covers handled errors and does not provide
  crash-consistent multi-file transactions.
- Made BLAKE3 tree accumulation incremental so hashing no longer reserves one
  8 MiB pending buffer per active accumulator.
- Simplified the native zstd boundary to standalone compression and decompression.

### Removed

- Removed the remaining native patch-from reference mapping and dictionary-prefix
  branches, format permission fields, and compatibility wrappers used only by tests.

## [0.5.0] - 2026-07-28

### Added

- VIPR format 3 with BLAKE3 tree file identities and independent chunked zstd
  replacement frames for large files.
- Adaptive application parallelism shared between multiple files and chunks of a
  single large file.

### Changed

- Sparse application now reconstructs large blocks in memory before positional
  writes instead of issuing many small sequential reads and writes.
- Patch application skips the full physical patch fingerprint pass when no
  expected fingerprint was supplied by the caller.
- Current patch fingerprints, source identities, target identities, and chunk
  identities use BLAKE3.

### Removed

- Removed VIPR format 1 and format 2 decoding and validation compatibility.
- Removed SHA-256 file verification, `zstd-patch-from`, patch-from compression
  metadata, implicit missing-method normalization, and legacy `targetHint`
  acceptance.
- Removed legacy hash-selection wrappers and the superseded sequential sparse
  application implementation.

### Compatibility

- `.vipr` files using format 1 or 2 must be applied with Viper-Patcher 0.4.1 or
  an earlier compatible release.

## [0.4.1] - 2026-07-26

### Fixed

- Restored Windows x64 and x86 release builds with MinGW by using the Win32
  `MAXDWORD` constant when bounding positional `ReadFile` requests.

### Changed

- Rebuilt the changelog around the repository's actual release tags and aligned
  application, packaging, documentation, and issue-template version references
  with `0.4.1`.

## [0.4.0] - 2026-07-26

### Added

- VIPR format version 2 with automatic `zstd-sparse`, `zstd-copy-add`, and
  `zstd-replace` payload selection while retaining version 1 read compatibility.
- Content-defined chunking and compressed COPY/ADD instruction streams for
  insertions, deletions, and moved unchanged regions.
- Handle-based reusable native zstd decoders and positional patch reads that are
  safe for parallel workers.
- Parallel output preparation in the patcher with an optional CLI `--parallel`
  limit, followed by sequential rollback-capable commits.
- Graduated Creator warnings for compression levels 10-19 and stronger warnings
  for ultra levels 20-22.
- Visible installed-file verification progress and asynchronous GUI validation.
- Core coverage enforcement at 80%, plus expanded format-v2, native zstd,
  cancellation, method-selection, reverse, and parallel-application tests.

### Changed

- Structural patch decoding errors now return immediately instead of hashing the
  remaining payload first; valid patches are still decoded and SHA-256 hashed in
  a single sequential pass.
- COPY/ADD analysis now reads blocks instead of individual bytes and reuses chunk
  storage.
- Sparse and COPY/ADD candidates use exact monotonic bounds to stop work early
  when they can no longer satisfy the current selection policy.
- Small instruction streams are decoded once in memory, while large streams use
  a bounded pipe; temporary instruction files and rereads are no longer needed.
- Installed source files are opened directly, and generated output is hashed
  during decompression or reconstruction instead of being reread afterward.
- Hashing, sparse parsing, COPY/ADD parsing, and native instruction decoding now
  propagate cancellation more consistently.
- GUI validation cancels stale work, ignores superseded results, and keeps the UI
  responsive while installed files are hashed.
- Verification occupies the first 20% of each file's displayed progress and
  application occupies the remaining 80%.
- Creator estimates account for sparse and COPY/ADD candidates, and low-reuse
  files use standalone replacement frames instead of patch-from search overhead.

### Fixed

- Updated CI expectations for staged verification progress and immediate
  rejection of structurally invalid patch headers.
- Corrected build and test regressions found during the format-v2 and fast-path
  migration.
- Set the race-test locale to `C.UTF-8` so Fyne does not attempt to parse the
  non-language locale tag `C` on Ubuntu runners.

### Removed

- Application patch snapshots, installed-file snapshots, obsolete snapshot
  identity fields, and redundant full-file hash passes.
- The slow/paranoid application implementation; the handle-based fast path is
  now the sole application path.
- Temporary instruction-stream files, redundant `fsync` calls, obsolete native
  path wrappers, scalar byte-comparison helpers, and other confirmed dead code.

## [0.3.3] - 2026-07-26

### Removed

- Confirmed dead code across CLI, GUI model, hashing, patch application,
  transaction, path validation, patch-format, and native zstd helpers.
- Tests that only exercised removed helpers; shared patch test setup was
  consolidated into dedicated support code.

## [0.3.2] - 2026-07-26

### Added

- Release-time checks that reject Windows, Linux, or macOS binaries with a
  dynamic libzstd dependency.
- Distribution of libzstd's BSD 3-Clause license and a notice documenting the
  selected upstream licensing option.

### Changed

- Release builds make the intended static libzstd linkage explicit for every
  supported target.

## [0.3.1] - 2026-07-26

### Added

- Shared screen-aware window sizing for the Creator and Patcher interfaces.
- Creator layout helpers for collapsible settings, fixed-size content, and an
  adaptive file-pair table.

### Changed

- Both GUI windows now fit their content while remaining within the available
  display height.

### Fixed

- Follow-up Creator sizing constants and controller formatting required by the
  adaptive-window changes.

## [0.3.0] - 2026-07-26

### Added

- Traversal-resistant installation access through `os.Root`, including secure
  temporary-output creation and root-relative rollback-capable renames.
- Conservative creator disk-space estimates displayed before GUI operations.
- Optional creator work-directory selection without changing the VIPR format or
  patcher behavior.
- Bounded per-file creator parallelism, limited to available logical CPUs and
  defaulting to one worker.
- Unicode-normalized, case-insensitive collision detection for portable patch
  paths.
- Explicit committed-with-warning results for cleanup failures after successful
  file replacement.

### Changed

- Stored permission metadata is advisory: Unix preserves the installed file mode
  and Windows ignores Unix mode bits.
- Native decompression writes through an already-open output handle instead of
  closing and reopening a temporary path.
- Private patch snapshots are parsed without redundant full-file rehashes, and
  application no longer repeats the complete installed-file preflight after
  snapshot validation.
- File completion progress is emitted only after transaction commit; generated
  but uncommitted files use a separate prepared stage.
- Release actions are pinned to immutable commits with readable version comments.
- Dependabot updates are grouped by Fyne, `golang.org/x`, tests, official GitHub
  actions, and third-party actions.
- `make check` builds libzstd only once.

### Fixed

- Bound CLI application to the exact patch digest that was inspected.
- Rejected output paths colliding with source or target files, including hard
  links and case/Unicode-equivalent paths.
- Rejected empty creator output filenames.
- Preserved a parent terminal window when a Windows GUI process detaches from a
  shared console.
- Rejected external patcher logos with excessive dimensions or pixel counts.
- Reported successful commits with cleanup warnings as success rather than a
  false operation failure.

### Removed

- New patches no longer write the unused `targetHint` metadata field; the
  version 1 reader remains compatible with patches that contain it.

## [0.2.1] - 2026-07-25

### Changed

- Updated Go image, networking, and system dependencies to patched releases.

## [0.2.0] - 2026-07-25

### Added

- Native file and directory dialogs with a Fyne fallback.
- Embedded application branding, an optional adjacent patcher-logo override, and
  automatic selection of a single adjacent `.vipr` file.
- Creator file-pair table, dedicated comment card, and collapsed settings.
- Fyne single-goroutine migration and GUI-only Windows console detachment.

### Changed

- Increased the Creator window size and improved GUI progress presentation.
- Packaged macOS applications without duplicate standalone executables.

### Fixed

- Corrected the creator file-pair table's `NewGridWrap` construction before the
  `v0.2.0` tag was created.

## 0.1.0 - 2026-07-25

### Added

- Creator and Patcher applications with automatic GUI/CLI selection.
- Versioned `.vipr` multi-file patch container.
- Forward and optional reverse differentials using libzstd 1.5.7 patch-from
  semantics.
- SHA-256 preflight and post-generation integrity checks.
- Rollback-capable patch application for handled replacement failures.
- Fyne desktop interfaces and progress reporting.
- Cross-platform release workflows for Windows, Linux, and macOS targets.

[Unreleased]: https://github.com/DarkCenobyte/viper-patcher/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/DarkCenobyte/viper-patcher/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/DarkCenobyte/viper-patcher/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/DarkCenobyte/viper-patcher/compare/v0.3.3...v0.4.0
[0.3.3]: https://github.com/DarkCenobyte/viper-patcher/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/DarkCenobyte/viper-patcher/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/DarkCenobyte/viper-patcher/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/DarkCenobyte/viper-patcher/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/DarkCenobyte/viper-patcher/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/DarkCenobyte/viper-patcher/releases/tag/v0.2.0
