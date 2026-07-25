# Changelog

All notable changes are documented in this file. The project follows Semantic
Versioning once the `.vipr` format is declared stable.

## [Unreleased]

### Added

- Atomic `FilePair` associations shared by the creator core, CLI, and GUI.
- Immutable snapshots for creation inputs, selected patches, and installed
  reference files.
- Dedicated file replacement `Transaction` with aggregated rollback and cleanup
  errors.
- Typed progress stages and synchronized GUI state models.
- Independent forward and reverse inspection capabilities with typed file
  issues.
- Fuzz tests, native sanitizer coverage, and `govulncheck` in continuous
  integration.

### Changed

- Split patch creation into planning, snapshotting, compression, assembly, and
  commit phases.
- Split the native wrapper into common, I/O, compression, and decompression
  translation units.
- Replaced parallel creator CLI file lists with repeatable
  `--file-pair <source>::<target>` options.
- Reworked the creator GUI to add one explicitly associated source/target pair
  at a time and to select output folder and filename without opening the
  destination early.
- Restricted release workflow write permissions to the publication job and
  validated manual version input before use.

### Fixed

- Stop decompression before writing beyond the output size declared by the
  patch.
- Reject non-portable and special file-mode bits at format parsing and mask
  permissions again before `chmod`.
- Detect file or patch replacement between validation and commit.
- Preserve rollback, cleanup, rename, remove, close, and permission errors.
- Prevent mutable GUI selections from changing an operation after it starts.
- Report both directions as applicable when source and target states are
  identical.

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
