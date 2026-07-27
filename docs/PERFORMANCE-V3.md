# VIPR format 3 performance design

Viper-Patcher 0.5.0 accepts only format 3 patches. File identities use the
domain-separated `blake3-tree-v1` construction over ordered fixed 8 MiB chunk
digests. The root commits total size, chunk size, chunk count, order, and every
chunk digest.

The streaming accumulator hashes the current BLAKE3 chunk incrementally and
retains only completed 32-byte digests. It therefore avoids the previous 8 MiB
buffer allocation per active accumulator.

Large standalone replacements use `zstd-chunked-replace`. Each 8 MiB output
chunk, except the final remainder, is an independent zstd frame with its own
BLAKE3 digest. Descriptor offsets and sizes must match those canonical chunk
boundaries, allowing frames to be decompressed, verified, and written with
`WriteAt` concurrently without creating an alternative file identity.

Sparse instructions are parsed in order and dispatched through a bounded queue
of per-chunk plans. Each source chunk is read once, replacements are overlaid in
memory, and the chunk is written once. Peak sparse planning memory is proportional
to the active/queued worker count rather than the complete changed-byte count of
a very large file.

Worker allocation treats `--workers` as a logical scheduling target. Intra-file
work is capped at eight workers and approximately one worker per 32 MiB. Source
verification can overlap output generation, so lightweight coordination and hash
tasks can temporarily exceed the target; the decoder pool and CPU-heavy chunk
work remain bounded.

Progress aggregation is performed in the core from weighted snapshot,
verification, compression, and application phases. The reported overall value
is monotone even when files finish out of order.

The CLI avoids a whole-file patch fingerprint pass when no inspected digest was
supplied. `OpenWithDigest` and the GUI use a physical BLAKE3-256 fingerprint to
detect replacement of a selected patch.

BLAKE3 is provided by `lukechampine.com/blake3` v1.4.1, an MIT-licensed Go
implementation with architecture-specific acceleration.
