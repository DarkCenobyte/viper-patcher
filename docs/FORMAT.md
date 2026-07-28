# VIPR file format

All integer fields outside JSON use little-endian byte order.

| Offset | Size | Description |
|---:|---:|---|
| 0 | 8 | Magic: `56 49 50 52 0D 0A 1A 01` |
| 8 | 8 | Unsigned JSON header length |
| 16 | variable | UTF-8 JSON header |
| data offset | variable | Concatenated differential payloads |

Payload offsets in the JSON header are relative to the first byte after the JSON
header. Readers reject unknown JSON fields, invalid hashes, unsupported or
inconsistent method metadata, file sizes outside signed 64-bit range, missing
payloads, out-of-bounds ranges, overlaps, gaps, trailing unreferenced data,
duplicate or case/Unicode-colliding paths, inconsistent reverse metadata, and
more than 262,144 file entries. The entry cap is a defensive decode bound above
the number of fully valid entries that fit in the existing 64 MiB header.

## Supported version

Viper-Patcher 0.5.0 accepts only `formatVersion = 3`, `compression.algorithm =
"hybrid"`, `compression.mode = "hybrid-v3"`, and `hashAlgorithm =
"blake3-tree-v1"`. Format versions 1 and 2, the earlier `hybrid-v2` mode,
legacy SHA-256 metadata, permission fields, and `zstd-patch-from` methods are
rejected before payload processing.

File identities are domain-separated BLAKE3 roots over ordered fixed 8 MiB chunk
digests. The root commits the total size, chunk size, chunk count, order, and
every chunk digest.

Each direction of each file declares one method:

- `zstd-sparse`: a compressed sequential replacement instruction stream for
  equal-size files with relatively few changed bytes;
- `zstd-copy-add`: a compressed stream of arbitrary COPY and ADD operations,
  selected with deterministic content-defined chunks;
- `zstd-replace`: a standalone zstd frame containing the complete output state;
- `zstd-chunked-replace`: a descriptor table followed by independent standalone
  zstd frames for large non-empty replacements.

Sparse and COPY/ADD methods require a bounded non-zero expanded instruction
length. Replace methods must not declare one. Chunked replacement must target a
non-empty file and must not declare an expanded instruction length.

## Chunked replacement payload

A chunked replacement starts with `56 43 52 50 0D 0A 1A 01`, followed by a
little-endian chunk count and one descriptor per chunk containing:

1. output offset;
2. decompressed output size;
3. compressed frame length;
4. a 32-byte BLAKE3 chunk digest.

Frames are concatenated after the descriptor table. Descriptor `i` must begin at
`i * 8 MiB`; every non-final descriptor must be exactly 8 MiB and the final one
must equal the remaining output size. The count must be `ceil(outputSize / 8
MiB)`. Frames and descriptors must consume the payload exactly, without gaps or
trailing bytes. These canonical boundaries are the same boundaries used by the
file identity tree. Readers validate the descriptor table without materializing
an array proportional to its untrusted count, then stream it through a bounded
worker queue.

## Sparse stream

Sparse streams begin with `56 53 50 52 0D 0A 1A 01`, followed by records:

1. unsigned varint count of unchanged source bytes to copy;
2. unsigned varint replacement length;
3. replacement bytes.

A zero gap followed by a zero length terminates the stream. The remaining source
tail is copied unchanged. Every operation is checked against the declared file
size. Application validates records sequentially and dispatches bounded 8 MiB
plans through a finite worker queue, without retaining the complete replacement
stream in memory. BLAKE3 digest tables remain direct arrays on the normal path
and spill to private temporary storage only if a table exceeds 64 MiB on 64-bit
targets or 16 MiB on 32-bit targets. This does not impose a source/target size
ratio or logical file-size cap.

## COPY/ADD stream

COPY/ADD streams begin with `56 43 41 44 0D 0A 1A 01`. Records start with one
opcode byte:

- `0`: end of stream;
- `1`: COPY, followed by unsigned varint source offset and length;
- `2`: ADD, followed by unsigned varint length and literal bytes.

COPY ranges must remain inside the declared source size. ADD records and the
combined output must remain inside the declared target size. The end opcode is
accepted only when the exact target size has been produced and no trailing data
remains.

The creator first tests the cheaper sparse representation for equal-size files.
When sparse is not suitable, it builds a COPY/ADD candidate using deterministic
content-defined chunks averaging 16 KiB. COPY/ADD is selected only when enough
source content is reused and the instruction stream remains below a conservative
bound. Otherwise the creator uses replacement, with chunked replacement selected
for sufficiently large outputs.

## Common header fields

- format version and UTC creation timestamp;
- creator name, version, commit, and build date;
- user comment;
- hash algorithm (`blake3-tree-v1`);
- compression family, linked libzstd version, mode, and level;
- reverse availability;
- ordered file entries containing source-relative path, source/target hashes,
  sizes, methods, expanded instruction lengths when applicable, and payload
  ranges.

Readiness is based on content identity and size. Replacements preserve the local
permission bits already present on the installed file; permissions are not part
of the portable patch metadata.

No path may be absolute, non-canonical, contain backslashes or drive syntax,
escape the selected installation root, or traverse a symbolic link. Components
ending in a dot or space are rejected for cross-platform portability.
