# VIPR file format

All integer fields outside JSON use little-endian byte order.

| Offset | Size | Description |
|---:|---:|---|
| 0 | 8 | Magic: `56 49 50 52 0D 0A 1A 01` |
| 8 | 8 | Unsigned JSON header length |
| 16 | variable | UTF-8 JSON header |
| data offset | variable | Concatenated differential payloads |

Payload offsets in the JSON header are relative to the first byte after the JSON
header. Readers reject unknown JSON fields, invalid hashes, missing payloads,
out-of-bounds ranges, overlaps, gaps, trailing unreferenced data, duplicate or
case/Unicode-colliding paths, and inconsistent reverse metadata.

## Version 2

Version 2 is written by Viper-Patcher 0.3.4 and uses `compression.mode =
"hybrid-v2"`. Each direction of each file declares one method:

- `zstd-sparse`: a compressed sequential replacement stream for equal-size files
  with relatively few changed bytes;
- `zstd-copy-add`: a compressed stream of arbitrary COPY and ADD operations,
  selected with deterministic content-defined chunks so matching regions remain
  discoverable after insertions and deletions;
- `zstd-replace`: a standalone zstd frame containing the complete target state,
  selected when too little source content can be reused;
- `zstd-patch-from`: a zstd patch-from frame retained for version 1 compatibility
  and valid version 2 containers, although the 0.3.4 creator prefers the faster
  methods above.

`forwardExpandedLength` and `reverseExpandedLength` are required for sparse and
COPY/ADD payloads. They bound decompression before an operation stream is
interpreted.

### Sparse stream

Sparse streams begin with `56 53 50 52 0D 0A 1A 01`, followed by records:

1. unsigned varint count of unchanged source bytes to copy;
2. unsigned varint replacement length;
3. replacement bytes.

A zero gap followed by a zero length terminates the stream. The remaining source
tail is copied unchanged. Every operation is checked against the declared file
size.

### COPY/ADD stream

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
content-defined chunks averaging 16 KiB. COPY/ADD is selected only when at least
one eighth of the target is reused and its uncompressed instruction stream stays
below a conservative bound. Otherwise the creator uses `zstd-replace`.

## Version 1 compatibility

Version 1 remains readable. Missing per-direction method fields normalize to
`zstd-patch-from`; its compression metadata remains `algorithm = "zstd"` and
`mode = "patch-from"`. Version 1 readers cannot read version 2 patches.

Readers still accept the legacy optional `targetHint` field but discard it.
Current creators never write it because installation paths are defined solely by
the source-relative `path` field.

## Common header fields

- format version and UTC creation timestamp;
- creator name, version, commit, and build date;
- user comment;
- hash algorithm (`sha256`);
- compression family, linked libzstd version, mode, and level;
- reverse availability;
- ordered file entries containing source-relative path, source/target hashes,
  sizes, advisory permission metadata, methods, and payload ranges.

`sourceMode` and `targetMode` retain portable Unix permission bits. Readiness is
based on content identity, Unix replacements preserve the installed file's local
mode, and Windows ignores Unix permission metadata.

No path may be absolute, non-canonical, contain backslashes or drive syntax,
escape the selected installation root, or traverse a symbolic link. Components
ending in a dot or space are rejected for cross-platform portability.
