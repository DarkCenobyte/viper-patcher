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

Run normal tests with the race detector and the sanitizer workflow. Fuzz targets
cover the V4 index and instruction streams; integration tests cover
forward/reverse application, wrong sources, raw/zstd/zero/run windows,
transaction behavior, bounded creator ordering, session-pool concurrency, and
borrowed-result lifetime.
