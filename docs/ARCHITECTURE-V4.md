# V4 architecture

Viper Patcher uses Go as the control plane and C as the data plane.

## Go control plane

Go owns path validation, stable file opening, immutable creator snapshots,
binary container validation, worker allocation, progress aggregation, temporary
outputs, rollback-capable multi-file commit, CLI/GUI integration, and policy.
It never interprets V4 instructions or implements BLAKE3.

## Native C data plane

One coarse cgo call builds a window or applies an output group. Native code owns
BLAKE3, zstd, positional source/patch reads, instruction decoding, output writes,
window/group verification, and shared source-chunk verification states. There
are no callbacks from a hot C loop into Go.

Static builds use the official BLAKE3 1.8.5 C implementation with runtime SIMD
dispatch (SSE2, SSE4.1, AVX2, AVX-512, or NEON as available). System-library
builds retain the portable C fallback. Both backends produce identical digests.

Each creator worker owns one persistent native session. Its source/target
buffers, CDC index, operation vectors, zstd compression context, and candidate
buffers are reused across windows. A bounded ordered pipeline writes each
selected payload directly from native memory and releases it before the worker
continues, so creator memory is proportional to worker count and window size,
not file size.

Application workers are also persistent sessions. Each owns reusable source
verification, compressed payload, expanded payload, and 8 MiB output-group
buffers plus a reusable zstd decompression context. Raw replacements are read
directly into their final group destination, and zstd replacements decompress
directly there. One application-wide cancellation word is shared by all native
calls instead of allocating a goroutine and C error buffer per group.

Windows uses private overlapped handles, one reusable event per session, and
explicit offsets. Linux and other POSIX targets use `pread`/`pwrite`; macOS and
Linux expose clone-on-write fast paths through `fcopyfile(..., COPYFILE_CLONE)`
and `FICLONE`. Windows attempts ReFS block cloning with
`FSCTL_DUPLICATE_EXTENTS_TO_FILE`. Every capability miss falls back to normal
reconstruction.

Linux output preallocation is limited to durable SSD/NVMe reconstruction.
Buffered, automatic, HDD, and clone-on-write paths only resize the file and
avoid an eager whole-file allocation.

## Dynamic planning

The creator chooses a file window size, then scores raw, zstd, and local-delta
candidates independently for every window under `balanced`, `apply-speed`, or
`patch-size`. Application distributes independent files first and canonical
8 MiB output groups second. Source chunk verification is shared atomically so
a chunk is hashed at most once.

The CDC Gear hash uses a fixed 256-entry table, and its source index is built in
one byte pass. The intentionally local source halo remains unchanged: globally
moved regions are an accepted patch-size tradeoff until benchmark data justifies
a broader index.

If at least 90% of an equal-size output consists of SAME windows, application
attempts a copy-on-write clone and rewrites only changed windows. The installed
file is never modified before verification and commit.

## Reliability

Creation snapshots every input and rejects mutation during the snapshot.
Application writes or clones a same-directory temporary output, validates it,
then performs rollback-capable renames. `buffered` preserves logical atomicity
without forcing a storage flush; `durable` additionally flushes prepared files.
Cancellation propagates through a native atomic word.
