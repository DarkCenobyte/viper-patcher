# Architecture

## Executables

`cmd/creator` and `cmd/patcher` are intentionally thin. They select GUI or CLI
mode and delegate all business logic to internal packages.

- `internal/cli`: deterministic command-line parsing and terminal progress.
- `internal/gui`: Fyne desktop interfaces.
- `internal/patch`: creation, inspection, and transactional application.
- `internal/patchformat`: versioned `.vipr` container serialization.
- `internal/zstd`: the only cgo boundary and native streaming implementation.
- `internal/pathutil`: common-root calculation and traversal-safe path joining.
- `internal/hashutil`: SHA-256 file integrity.

The creator CLI accepts source and target files through repeatable flags. Its
output `.vipr` path is the single final positional argument, so every option
must precede that path. The patcher CLI keeps its target directory as its final
positional argument.

## Patch creation

For each ordered source/target pair, the creator:

1. Computes the path relative to the common parent of all source files.
2. Hashes both source and target with SHA-256.
3. Creates a zstd frame using the source as the reference prefix and the target
   as input.
4. Optionally creates a second frame in the opposite direction.
5. Writes a strict JSON header followed by all frame blobs.
6. Atomically replaces the requested output path.

Target names and locations do not define installation paths. Source-relative
paths are authoritative, as required by the format.

## Patch application

The patcher reads and validates the container, requires canonical portable
relative paths, rejects case-colliding entries, rejects symbolic links in every
existing path component, and hashes all existing files. It enables forward
application only when every source hash matches, and reverse application only
when every target hash matches and reverse data exists.

All generated outputs are written to same-directory temporary files and checked
against the expected size and SHA-256 hash. Only after every output passes are
files replaced. Original files are renamed to backups, and an error during the
commit phase triggers rollback of already replaced files.

## Native boundary

The reference file is memory-mapped while target and patch streams use bounded
buffers. This mirrors zstd's patch-from approach without loading target files or
patch frames completely into Go memory. The wrapper pins libzstd to exactly
1.5.7 and fails fast when a system-linked development build uses another
version. Differential intervals must be contiguous from the data offset to the
physical end of the container, preventing hidden or unreferenced payload data.
