# Architecture

## Executables

`cmd/creator` and `cmd/patcher` are thin entry points. CLI and GUI layers delegate
all patch logic to `internal/patch`; `internal/patchformat` owns the container and
`internal/zstd` is the only cgo boundary.

The patcher CLI applies directly through one application session instead of
performing a complete inspection pass followed by a second validation pass. The
GUI may still call `Inspect` to present readiness before the user starts an
operation.

## Patch creation

`patch.Create` uses these phases:

1. validate file pairs and derive source-relative installation paths;
2. copy source and target files into immutable creator snapshots while hashing
   them during the copy;
3. for equal-size files, try the cheap sequential sparse representation;
4. otherwise build a deterministic content-defined COPY/ADD candidate and use
   it when enough target bytes can be reused, falling back to `zstd-replace`;
5. compress independent file payloads through a bounded worker pool while
   preserving deterministic archive order;
6. assemble a version 2 container in a same-directory temporary file and commit
   it atomically.

Creator snapshots no longer perform a redundant second full content hash or an
`fsync` on disposable temporary files. Identity, size, modification time, and
path identity are checked after each copy.

## Patch application

Application is optimized for one useful read and one useful write wherever the
selected method permits it:

1. open the patch once, parse its header, and calculate its SHA-256 in the same
   sequential pass;
2. open each installed source through `os.Root` and keep the handle alive until
   commit;
3. prepare independent output files in parallel, with one reusable native zstd
   decoder per worker;
4. calculate output SHA-256 while bytes are produced instead of rereading the
   generated file;
5. commit prepared files sequentially through the rollback-capable transaction.

Patch payload reads are positional (`pread` on POSIX and overlapped `ReadFile` on
Windows), so parallel workers never share a file cursor. Source references are
mapped directly from already-open handles; neither the patch nor installed files
are copied into application snapshots.

### Method-specific behavior

- `zstd-sparse` reconstructs equal-size targets sequentially. Unchanged ranges
  are copied from the source, replacement ranges come from the bounded operation
  stream, and source plus target SHA-256 values are calculated in that pass.
- `zstd-copy-add` validates the source once, then executes bounds-checked COPY
  ranges and literal ADD data from a compressed operation stream. Content-defined
  chunks keep matches stable across insertions and deletions.
- `zstd-replace` validates the source once, then decodes a standalone frame while
  hashing output blocks in the native-to-Go callback.
- `zstd-patch-from` validates the source once, maps its open handle as the zstd
  prefix, and hashes generated blocks during decompression.

The fast path intentionally avoids disposable-file `fsync` calls and content
rehashing immediately before rename. The transaction still verifies source
identity, size, and modification time before replacement, renames originals to
backups, commits prepared files, and performs best-effort rollback if a later
rename fails. This is the only application mode; there is no slower paranoid
snapshot mode.

## Native boundary

Native code is split by responsibility:

- `native_internal.h`: shared declarations and platform abstractions;
- `native_common.c`: version checks, parameters, and error helpers;
- `native_io.c`: path access, handle duplication, mapping, and positional reads;
- `native_compress.c`: patch-from and standalone compression;
- `native_decompress.c`: reusable decoder contexts and bounded segment decoding.

Each decoder owns one `ZSTD_DCtx` and reusable 1 MiB input/output buffers. Output
blocks are synchronously exposed to Go for hashing and throttled progress. The
wrapper remains pinned to libzstd 1.5.7.

## Integrity and security boundary

All patch and file hashes remain SHA-256. Format ranges, decompressed sizes,
sparse and COPY/ADD instructions, portable paths, and symbolic-link traversal are validated.
`os.Root` keeps installation operations relative to one stable root handle.

The target directory is not treated as a privilege boundary. A hostile process
with write access can still race file contents between preparation and commit;
the fast design detects replacement, size, and modification-time changes but no
longer performs another full content hash at commit.
