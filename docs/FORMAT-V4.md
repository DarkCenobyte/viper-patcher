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

The optional fine-verification index extension does not alter that identity.
It stores sparse, sorted source-band indexes and full BLAKE3-256 digests for
delta source spans whose canonical 8 MiB verification would cause meaningful
read amplification. Supported band sizes are 64 KiB, 256 KiB, and 1 MiB. The
creator selects one size independently for each source direction and omits the
table when dense coverage or index cost would erase the predicted benefit.

Fine digests are ordinary full BLAKE3-256 hashes of complete aligned bands;
they are not truncated chaining values and are not used to reconstruct the
canonical file root. The extension is covered by the existing index digest.
Legacy V4 patches have no extension and continue to use canonical verification.
Readers that do not support the feature reject its container flag rather than
misinterpreting the extended index.

SAME and COPY require no extra table because their window digest already
authenticates the exact bytes they copy. Delta windows use a fine table only
when every band intersecting their declared source span is present; otherwise
the applicator falls back to the canonical digest table.

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

When fine verification is available, `referenced` reads, authenticates, and
immediately consumes only the required bands. A missing optimization table is a
normal fallback condition; a present digest that does not match is always a
source mismatch. Reflink outputs verify unchanged SAME windows before commit.

Paths are canonical slash-separated relative paths. Absolute paths, traversal,
NULs, backslashes, duplicate paths, case/Unicode collisions, invalid ranges,
overflows, malformed opcodes, oversized indexes, and inconsistent digest tables
are rejected before commit.
