# Legacy zstd wrapper

The historical `internal/zstd` package is no longer part of normal builds.
V4 uses `internal/nativev4` as its single native data plane for zstd, BLAKE3,
positional I/O, and window processing.

The old wrapper remains temporarily behind the explicit
`vipr_legacy_zstd` build tag so that its tests and implementation can be
consulted while the source-cache work is reviewed. It must not be imported by
production code. A later repository-history cleanup may delete the tagged
files without changing any supported API or patch format.

Normal commands must not set `vipr_legacy_zstd`.
