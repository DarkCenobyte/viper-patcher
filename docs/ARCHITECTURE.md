# Architecture

## Executables

`cmd/creator` and `cmd/patcher` are thin entry points. CLI and GUI layers delegate
all patch logic to `internal/patch`; `internal/patchformat` owns the container and
`internal/zstd` is the only cgo boundary.

The patcher CLI applies directly through one application session instead of
performing a complete inspection pass followed by a second validation pass. The
GUI uses a bounded parallel `Inspect` pass to present readiness before the user
starts an operation, and derives the known post-commit direction state without
immediately hashing every generated file again.

## Patch creation

`patch.Create` uses these phases:

1. validate file pairs and derive source-relative installation paths;
2. copy source and target files into immutable creator snapshots while computing
   their BLAKE3 tree identities and retaining one 32-byte digest per fixed chunk;
3. for equal-size files, try the cheap sequential sparse representation;
4. otherwise build a deterministic content-defined COPY/ADD candidate and use
   it when enough target bytes can be reused, falling back to `zstd-replace` or
   `zstd-chunked-replace` for large standalone replacements;
5. resolve `0` to the process-aware `GOMAXPROCS` target, divide that target
   between files and large chunks, and preserve deterministic archive order;
6. assemble a format 3 container in a same-directory temporary file, replace
   the destination by rename, and synchronize the containing directory.

Creator snapshots do not perform a redundant second full content hash or an
`fsync` on disposable temporary files. Identity, size, modification time, and
path identity are checked after each copy. BLAKE3 chunks are hashed incrementally;
a snapshot retains only one 32-byte digest per 8 MiB chunk and never retains an
8 MiB copy solely for hashing.

## Patch application

Application is optimized for useful reads and writes wherever the selected
method permits it:

1. open the patch once and parse its strict format 3 header; calculate a physical
   BLAKE3 fingerprint only when an inspected digest must be checked;
2. open each installed source through `os.Root`, prepare and validate its output,
   then close the source before the multi-file commit begins;
3. split the worker target between independent files and large chunks, with an
   eagerly prepared decoder pool sized to the maximum concurrent decode demand;
   inspect each bounded zstd frame header before use and reserve its actual window
   against one process-wide decoder-memory budget;
4. compute BLAKE3 chunk digests while bytes are produced and assemble file roots
   without a final sequential reread. Digest tables stay as direct arrays on the
   normal path and spill to private temporary files only beyond 64 MiB on 64-bit
   targets or 16 MiB on 32-bit targets;
5. synchronize each prepared output, commit files sequentially through
   rollback-capable per-file renames, synchronize each affected parent directory
   once on Unix-like systems, and only then remove committed backups.

The worker option accepts `0` for the process-aware automatic `GOMAXPROCS` value,
or an explicit value from 1 through the logical CPU count. It is a scheduling
target, not a strict process-wide goroutine limit. Source verification may
overlap output generation, and small coordination goroutines may temporarily run
in addition to the requested workers. CPU-heavy compression and decompression
remain bounded by the allocated file/chunk workers and decoder pool.

Patch payload reads are positional (`pread` on POSIX and overlapped `ReadFile` on
Windows), so parallel workers never share a file cursor. Neither the patch nor
installed files are copied into application snapshots.

### Method-specific behavior

- `zstd-sparse` validates and streams the instruction sequence into a bounded
  producer/consumer queue. At most the queued and active 8 MiB chunk plans retain
  replacement bytes. Its existing parallel chunk path is unchanged for ordinary
  files; only an extreme BLAKE3 digest table spills after 64 MiB on 64-bit
  targets or 16 MiB on 32-bit targets, so logical file size is not capped and
  the common path does not gain another data pass.
- `zstd-copy-add` verifies the source in parallel while executing bounds-checked
  COPY ranges and literal ADD data from the compressed operation stream. Its
  source index is a compact BLAKE3-256-keyed sorted array with an exact per-index
  backing-memory budget. Concurrently live index arrays share an additional
  128 MiB creator-wide budget. Reservations are atomic, so one request never
  holds a partial allocation while waiting for the rest. Content-defined chunks
  keep matches stable across insertions and deletions.
- `zstd-replace` verifies the installed source with parallel BLAKE3 chunk reads
  concurrently with standalone zstd decompression and output hashing.
- `zstd-chunked-replace` validates its compact 56-byte descriptor table without
  retaining it, streams the table a second time through a bounded worker queue,
  decompresses independent frames concurrently, verifies each output chunk, and
  assembles the final BLAKE3 tree root in chunk order.

Progress is aggregated by weighted per-file phases in the core. Callbacks are
serialized and receive a monotone `Overall` value, so concurrent file events do
not make GUI or CLI progress move backwards.

The fast path avoids `fsync` on disposable creator snapshots and intermediate
compression files, as well as content rehashing immediately before rename.
Prepared application outputs are different: they become the installed files, so
Viper Patcher synchronizes each one after validation and before closing it. It then
verifies source identity, size, and modification time, renames originals to
backups, commits prepared files, and synchronizes each affected parent directory
before backup removal on Unix-like systems. Windows performs the output-file flush
but intentionally treats directory synchronization as a no-op because
`FlushFileBuffers` does not provide the same portable directory-entry durability
primitive. Backup deletion is not followed by a second directory sync, so a crash
may leave a stale hidden backup rather than adding another synchronous write to
the normal path. Best-effort rollback covers handled replacement failures. This
is not crash consistency: power loss or a kernel failure can interrupt a
multi-file commit and leave a partially applied installation. This
performance-oriented behavior is the only application mode.

## Native boundary

Native code is split by responsibility:

- `native_internal.h`: shared declarations and platform abstractions;
- `native_common.c`: version checks, compression parameters, and error helpers;
- `native_io.c`: UTF-8 file opening, handle duplication, and positional reads;
- `native_compress.c`: standalone zstd compression;
- `native_decompress.c`: reusable decoder contexts and bounded standalone frame
  decoding.

The native API no longer exposes reference files or zstd `patch-from` state.
Sparse and COPY/ADD reuse is represented explicitly by format 3 instruction
streams before standalone zstd compression.

Each decoder owns one `ZSTD_DCtx` and reusable 1 MiB input/output buffers. Before
acquisition, a positional read inspects only the bounded frame header. The actual
frame window is reserved atomically against one process-wide budget: 512 MiB on
64-bit targets and at least one architecture-specific maximum window on 32-bit
targets. Large declared output sizes with small windows therefore keep their
normal parallelism; only simultaneous large-window frames are throttled. Output blocks
are synchronously exposed to Go for hashing and throttled progress. The wrapper
remains pinned to libzstd 1.5.7.

## Integrity and security boundary

File identities use the domain-separated `blake3-tree-v1` construction and
selected patch files use standard BLAKE3-256 fingerprints. Format ranges,
decompressed sizes, canonical chunk descriptors, sparse and COPY/ADD
instructions, portable paths, and symbolic-link traversal are validated. Header
decoding stops before more than 262,144 file entries can be materialized; this is
above the number of fully valid entries representable by the existing 64 MiB
header bound. Transaction duplicate detection uses case-folded target and exact
temporary-path indexes, avoiding quadratic registration work. `os.Root` keeps
installation operations relative to one stable root handle.

The format stores content identities and sizes, not platform permission metadata.
A replacement preserves the permissions already present on the installed file.

The target directory is not treated as a privilege boundary. A hostile process
with write access can still race file contents between preparation and commit;
the fast design detects replacement, size, and modification-time changes but
does not perform another full content hash at commit.
