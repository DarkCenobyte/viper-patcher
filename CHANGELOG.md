# Changelog

All notable changes are documented in this file. The project follows Semantic
Versioning once the `.vipr` format is declared stable.

## [Unreleased]

## [0.3.4] - 2026-07-26

### Added

- VIPR format version 2 with automatic `zstd-sparse`, `zstd-copy-add`, and
  `zstd-replace` payload selection.
- Content-defined chunking and compressed COPY/ADD streams for fast application
  across insertions, deletions, and moved unchanged regions.
- Handle-based reusable native zstd decoders and positional patch reads safe for
  parallel workers.
- Automatic parallel output preparation in the patcher, with an optional CLI
  `--parallel` limit.
- Version 1 read compatibility and dedicated v2 method-selection, reverse, and
  parallel-application tests.

### Changed

- Patch files are parsed and SHA-256 hashed in one sequential pass.
- Installed source files are opened directly instead of copied to application
  snapshots.
- Generated output is SHA-256 hashed during decompression or sparse
  reconstruction instead of being reread afterward.
- Patch application prepares independent files in parallel and commits them
  sequentially through the existing rollback transaction.
- Temporary application files no longer use redundant `fsync` calls.
- Creator snapshots no longer rehash their source after copying or synchronize
  disposable snapshot files.
- Files with little reusable source content use standalone replacement frames
  instead of paying patch-from reference-search overhead.
- Creator disk-space estimates account for sparse and COPY/ADD instruction candidates.

### Removed

- Application patch snapshots, installed-file snapshots, redundant full-file
  hash passes, and the dead helpers used only by those paths.
- The slow/paranoid application path; the handle-based fast path is now the only
  implementation.

## [0.3.0] - 2026-07-26

### Added

- Traversal-resistant installation access through `os.Root`, including secure
  temporary-output creation and root-relative transactional renames.
- Conservative creator disk-space estimates displayed before GUI operations.
- Optional creator work-directory selection without changing the VIPR format or
  patcher behavior.
- Bounded per-file creator parallelism, limited to the available logical CPUs
  and disabled by default through a default value of one worker.
- Unicode-normalized, case-insensitive collision detection for portable patch
  paths.
- Explicit committed-with-warning results for cleanup failures that occur after
  successful file replacement.

### Changed

- Stored permission metadata is advisory. Unix application preserves the
  installed file mode and Windows ignores Unix mode bits, keeping patches
  portable across Windows, Linux, and macOS.
- Native decompression writes through an already-open output handle instead of
  closing and reopening a temporary path.
- Private patch snapshots are parsed without redundant full-file rehashes, and
  application no longer repeats the complete installed-file preflight after
  snapshot validation.
- File completion progress is emitted only after the transaction commits;
  generated but uncommitted files use a separate prepared stage.
- Release actions are pinned to immutable commits while retaining readable
  version comments.
- Dependabot updates are grouped by Fyne, `golang.org/x`, tests, official GitHub
  actions, and third-party actions.
- `make check` builds libzstd only once.

### Fixed

- Bind CLI application to the exact patch digest that was inspected.
- Reject output paths that collide with a source or target file, including hard
  links and case/Unicode-equivalent paths.
- Reject empty creator output filenames.
- Preserve a parent terminal window when a Windows GUI process detaches from a
  shared console.
- Reject external patcher logos with excessive width, height, or pixel count.
- Report successful commits with cleanup warnings as success instead of a false
  operation failure.

### Removed

- New patches no longer write the unused `targetHint` metadata field. The
  version 1 reader retains compatibility with existing patches that contain it.

## [0.2.1] - 2026-07-25

### Fixed

- Corrected the creator file-pair table layout.
- Updated Go image, networking, and system dependencies to patched releases.

## [0.2.0] - 2026-07-25

### Added

- Native file and directory dialogs with a Fyne fallback.
- Embedded application branding, optional adjacent patcher logo override, and
  automatic selection of one adjacent `.vipr` file.
- Creator file-pair table, dedicated comment card, and collapsed settings.
- Fyne single-goroutine migration and GUI-only Windows console detachment.

### Changed

- Increased the creator window size and improved GUI progress presentation.
- Packaged macOS applications without duplicate standalone executables.

## [0.1.0] - 2026-07-25

### Added

- Creator and patcher applications with automatic GUI/CLI selection.
- Versioned `.vipr` multi-file patch container.
- Forward and optional reverse differentials using libzstd 1.5.7 patch-from
  semantics.
- SHA-256 preflight and post-generation integrity checks.
- Transactional patch application with rollback.
- Fyne desktop interfaces and progress reporting.
- Cross-platform release workflows for Windows, Linux, and macOS targets.
