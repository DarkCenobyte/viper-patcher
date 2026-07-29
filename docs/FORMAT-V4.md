# VIPR format V4

VIPR V4 is the only supported patch format. It is a binary, window-oriented
container designed around application throughput and bounded native memory.

## Container

All integers are little-endian. A 16-byte prefix is followed by contiguous
window payloads, a binary index, and a fixed 64-byte footer. The footer stores
the index offset, index size, BLAKE3-256 digest, and feature flags. Readers seek
directly to the footer and never scan payloads to discover the index.

The binary index stores creator metadata, compression settings, ordered file
entries, canonical 8 MiB source and target digest tables, and forward/reverse
window descriptors. Payload ranges must be contiguous and consume the complete
data section without overlap, gaps, or trailing bytes.

## File identities

A file identity is `blake3-tree-v1`: BLAKE3-256 is computed for each ordered
8 MiB chunk, then a domain-separated root commits the logical file size, chunk
size, chunk count, order, and every digest. BLAKE3 exists only in the native C
data plane.

## Windows

A file has one recorded window size chosen by the creator or supplied with
`--window-size`. Supported sizes are 256 KiB, 512 KiB, 1 MiB, 2 MiB, 4 MiB,
and 8 MiB. The final window may be shorter. Each window independently selects:

- `SAME`: copy from the same source offset;
- `COPY`: copy one source range;
- `DELTA_RAW` or `DELTA_ZSTD`: compact COPY/ADD/RUN/ZERO instructions;
- `REPLACE_RAW` or `REPLACE_ZSTD`: independent replacement data;
- `ZERO` or `RUN`: payload-free or one-byte repeated output.

Compact opcodes include short same-offset copies, local and absolute source
copies, delta-coded source offsets, short and long literals, COPY+ADD pairs,
runs, and zeros. Every window carries a BLAKE3 output digest.

## Verification

`referenced` verifies every canonical source chunk before a window uses it and
verifies every produced window and 8 MiB output group. `strict` first verifies
the complete source tree. `output` omits source pre-verification but retains
window and output-group verification. The index digest and tree-root consistency
are always checked.

Paths are canonical slash-separated relative paths. Absolute paths, traversal,
NULs, backslashes, duplicate paths, case/Unicode collisions, invalid ranges,
overflows, malformed opcodes, oversized indexes, and inconsistent digest tables
are rejected before commit.
