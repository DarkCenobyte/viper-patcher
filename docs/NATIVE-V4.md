# Native V4 maintenance

`internal/nativev4` is a deliberately small ABI. `native.h` is the public C
boundary; generated constants come from `internal/patchformat/v4_format.def`.
Run `go generate ./internal/patchformat` after changing that definition.

Static builds fetch the verified official BLAKE3 1.8.5 crate and produce
`build/blake3/lib/libblake3.a`. The archive uses runtime x86 SIMD dispatch or
NEON on ARM64. `blake3_backend.h` keeps the portable implementation as the
non-static fallback. Digest vectors must pass against both backends.

Native code must use bounded allocations derived from validated window or
canonical chunk sizes. Sessions own reusable workspaces and are never used by
two Go workers concurrently. A borrowed creator result remains valid only until
`vipr_window_result_release`; the worker must not build another window first.
Handles are borrowed from Go and duplicated on Windows. No native pointer is
retained by Go after the owning call, and no Go pointer is stored by C.

The source verification buffer may retain the most recently authenticated
canonical chunk as a per-session read cache. Any operation that overwrites that
buffer must invalidate the cache metadata first. Cache hits are optimization
only: BLAKE3 state transitions and output-group verification remain mandatory.
The direct SAME-group path is permitted only when the session owns the exact
verified bytes and the canonical source and target digests are identical. COPY
may consume a verified prefix and continue with positional I/O; byte counters
must include only bytes actually read from the source handle.

Fine source verification is represented at the ABI as parallel immutable
arrays of sorted band indexes and full 32-byte digests plus an atomic state
array. Native code must first prove that a delta window's complete declared
source span is represented. Only then may it replace canonical verification
with fine-band verification. Missing entries select the canonical fallback;
malformed metadata or a digest mismatch must never fall back silently.

The fine verifier follows `read -> BLAKE3-256 -> publish state -> consume`.
Whichever worker authenticates a band may retain those exact bytes in its
session verification buffer so the following COPY can avoid a second read.
Other workers may observe the shared valid state but must still read the bytes
they need to materialize output. All pointers passed through cgo remain valid
for the complete native call and are kept alive by the owning Go verification
object.

For clone-on-write output, referenced and output verification schedule SAME
windows as verification-only jobs. Native SAME handling does not rewrite the
cloned extent and may reuse the exact-source digest result as its window digest.

An initial session transferred to `SessionPool` is owned by the pool even when
construction fails; callers must clear their reference immediately after the
transfer.

Run normal tests with the race detector and the sanitizer workflow. Fuzz targets
cover the V4 index and instruction streams; integration tests cover
forward/reverse application, wrong sources, raw/zstd/zero/run windows,
transaction behavior, bounded creator ordering, session-pool concurrency, and
borrowed-result lifetime.
