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

Windows uses private overlapped handles and explicit offsets. Linux and other
POSIX targets use `pread`/`pwrite`; macOS and Linux expose clone-on-write fast
paths through `fcopyfile(..., COPYFILE_CLONE)` and `FICLONE`. Windows attempts
ReFS block cloning
with `FSCTL_DUPLICATE_EXTENTS_TO_FILE`. Every capability miss falls back to
normal reconstruction.

## Dynamic planning

The creator chooses a file window size, then scores raw, zstd, and local-delta
candidates independently for every window under `balanced`, `apply-speed`, or
`patch-size`. Application distributes independent files first and canonical
8 MiB output groups second. Source chunk verification is shared atomically so
a chunk is hashed at most once.

If at least 90% of an equal-size output consists of SAME windows, application
attempts a copy-on-write clone and rewrites only changed windows. The installed
file is never modified before verification and commit.

## Reliability

Creation snapshots every input and rejects mutation during the snapshot.
Application writes or clones a same-directory temporary output, validates it,
then performs rollback-capable renames. `buffered` preserves logical atomicity
without forcing a storage flush; `durable` additionally flushes prepared files.
Cancellation propagates through a native atomic word.
