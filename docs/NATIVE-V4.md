# Native V4 maintenance

`internal/nativev4` is a deliberately small ABI. `native.h` is the public C
boundary; generated constants come from `internal/patchformat/v4_format.def`.
Run `go generate ./internal/patchformat` after changing that definition.

Native code must use bounded allocations derived from validated window or
canonical chunk sizes. Handles are borrowed from Go and duplicated on Windows.
No native pointer is retained by Go after the owning call, and no Go pointer is
stored by C.

Run normal tests with the race detector and the sanitizer workflow. Fuzz targets
cover the V4 index; integration tests cover forward/reverse application, wrong
sources, raw/zstd/zero/run windows, and transaction behavior.
