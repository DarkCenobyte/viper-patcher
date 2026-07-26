# VIPR file format version 1

All integer fields outside JSON use little-endian byte order.

| Offset | Size | Description |
|---:|---:|---|
| 0 | 8 | Magic: `56 49 50 52 0D 0A 1A 01` |
| 8 | 8 | Unsigned JSON header length |
| 16 | variable | UTF-8 JSON header |
| data offset | variable | Concatenated zstd differential frames |

Blob offsets in the JSON header are relative to the first byte after the JSON
header. Readers reject unknown JSON fields, unsupported format versions,
invalid hashes, missing frames, out-of-bounds ranges, overlaps, gaps, trailing
unreferenced data, duplicate, Unicode-equivalent, or case-colliding paths, and
inconsistent reverse metadata.

## Header fields

- Format version and UTC creation timestamp.
- Creator name, version, commit, and build date.
- User comment.
- Hash algorithm (`sha256`).
- Compression algorithm (`zstd`), linked library version, patch mode, and level.
- Reverse availability.
- Ordered file entries containing source-relative path, source/target hashes,
  sizes, legacy mode metadata, and forward/reverse frame ranges.

`sourceMode` and `targetMode` retain the portable Unix permission bits recorded
by version 1 creators. Readers still reject set-user-ID, set-group-ID, sticky,
file-type, and every other unknown bit as a defensive format boundary. Current
application logic treats both fields as advisory: readiness depends only on
content identity, Unix replacements preserve the installed file's local mode,
and Windows ignores Unix permission metadata. This policy keeps one VIPR patch
portable across supported operating systems and filesystems.

Version 1 readers still accept the legacy optional `targetHint` field for
backward compatibility, but current creators do not write it because
installation paths are defined exclusively by the source-relative `path` field.

## Compatibility rules

Version 1 readers must reject versions they do not understand. Future additive
changes require a new format version because version 1 uses strict JSON decoding.
No path may be absolute, non-canonical, contain backslashes or drive syntax,
escape the selected installation root, or traverse a symbolic link. Components
ending in a dot or space are rejected for cross-platform portability.
