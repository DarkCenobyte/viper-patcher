# Architecture

## Executables

`cmd/creator` and `cmd/patcher` are intentionally thin. They select GUI or CLI
mode and delegate all business logic to internal packages.

- `internal/cli`: deterministic command-line parsing and terminal progress.
- `internal/gui`: Fyne desktop interfaces and small synchronized state models.
- `internal/patch`: creation, inspection, immutable snapshots, and transactional
  application.
- `internal/patchformat`: versioned `.vipr` container serialization.
- `internal/zstd`: the only cgo boundary and native streaming implementation.
- `internal/pathutil`: common-root calculation and traversal-safe path joining.
- `internal/hashutil`: SHA-256 file integrity.
- `internal/progress`: typed operation stages and progress events.

The creator CLI accepts repeatable `--file-pair <source>::<target>` options. A
single `patch.FilePair` value carries each association from CLI or GUI parsing to
the creation core. The output `.vipr` path is the single final positional
argument, so every option must precede that path. The patcher CLI keeps its
target directory as its final positional argument.

## Patch creation

`patch.Create` is a short orchestration function. Its implementation is split
into explicit phases:

1. `createPlanFromOptions` validates every `FilePair`, resolves source paths, and
   derives the source-relative installation paths.
2. `snapshotCreationInputs` copies source and target files into immutable
   snapshots while checking identity, content, size, permissions, and
   replacement races.
3. `compressCreationInputs` hashes metadata from those snapshots and produces
   forward and optional reverse zstd frames.
4. `assemblePatch` writes the strict header and frame blobs into a same-directory
   temporary output.
5. `Transaction` replaces an existing regular output only after verifying that
   it has not changed since validation.

Target names and locations do not define installation paths. Source-relative
paths are authoritative, as required by the format. Changes made to original
files after snapshot creation cannot change the patch being assembled.

## Patch inspection

Inspection evaluates source and target states independently. `ValidationResult`
contains `CanApplyForward` and `CanApplyReverse`, plus typed missing-file and
file-issue details. A file may validly match both sides when source and target
content and permissions are identical. Mixed source/target directories,
permission-only differences, non-regular files, and unknown content are
reported separately.

## Patch application

The patcher parses the selected patch through one stable file handle and records
its SHA-256 digest. Application copies the patch into an immutable work snapshot
and, when started from the GUI, rejects a digest that differs from the inspected
selection. It then validates the container and installation paths, rejects
symbolic links in existing path components, and checks each regular file against
source and target hash, size, and portable permission metadata.

For the selected direction, every installed reference file is copied into an
immutable snapshot before native decompression. Generated outputs are written to
same-directory temporary files, constrained to the declared decompressed size,
and checked against expected size, SHA-256 hash, and permission bits.

A dedicated `Transaction` verifies each installed file again immediately before
replacement. Original files are renamed to backups, prepared outputs are moved
into place, and a later failure triggers a best-effort rollback. Replacement,
rollback, cleanup, close, rename, remove, and permission errors are preserved
with `errors.Join` rather than silently discarded. A process crash or power loss
can still interrupt the best-effort rollback because the transaction does not
use a persistent recovery journal.

The path snapshots and repeated identity checks substantially reduce time-of-
check/time-of-use windows. Fully eliminating every local filesystem race on all
supported operating systems would require platform-specific handle-relative
APIs and is outside the portable path abstraction.

## Native boundary

Native code is divided by responsibility:

- `native_internal.h`: shared declarations and platform abstractions.
- `native_common.c`: version checks, parameters, and error helpers.
- `native_io.c`: mapping, seek, read, write, and platform-specific file handling.
- `native_compress.c`: patch-from compression.
- `native_decompress.c`: bounded segment decompression.

The reference file is memory-mapped while target and patch streams use bounded
buffers. This mirrors zstd's patch-from approach without loading target files or
patch frames completely into Go memory. The wrapper pins libzstd to exactly
1.5.7 and fails fast when a system-linked development build uses another
version. Differential intervals must be contiguous from the data offset to the
physical end of the container, preventing hidden or unreferenced payload data.
The decompressor checks the declared output boundary before every write.
