# Performance tuning checkpoints

## Application hot-path series based on `124b4b0`

The first post-v0.6.2 series deliberately changes no V4 wire bytes. Benchmark
each patch separately and cumulatively:

1. reuse the setup session in the native pool and remove the extra per-file
   session reservation;
2. reuse the just-authenticated canonical source chunk for local SAME/COPY
   materialization;
3. write fully SAME canonical groups directly from that verified buffer and
   reuse its canonical digest instead of hashing a copied group again;
4. retain the source chunk containing the first local COPY and reuse cached
   prefixes for cross-chunk windows;
5. append compact transaction transitions instead of serializing every file
   entry after each rename.

The expected mechanical signals are:

- one fewer native session per active non-empty file;
- up to one source read instead of two for a referenced canonical chunk whose
  consumer remains inside that chunk;
- one BLAKE3 pass and no group-buffer copy for eligible fully SAME groups;
- fewer reread bytes for COPY windows crossing a canonical chunk boundary;
- journal bytes proportional to file count rather than the square of file
  count.

These are not sufficient acceptance criteria. Keep a patch only when measured
wall time improves or remains neutral on all of:

- one 100 KiB file, related and unrelated;
- ten 100 KiB files, related and unrelated;
- the 64-file mixed workload;
- 50 MiB, 256 MiB scattered/shifted, and 500 MiB workloads;
- workers 1 and automatic;
- automatic memory and a 128 MiB limit;
- buffered and durable commits.

Record application time, source/patch bytes read, output bytes written, native
session count, direct-SAME group count, private memory, working set, page
faults, journal bytes, flush time, and commit time. Reject the series if it
changes patch bytes, canonical digests, reverse output, transaction recovery,
or the bounded 32-bit fallback.

Run the correctness gate before every benchmark campaign:

```sh
make check
CGO_ENABLED=1 go test -count=1 -tags vipr_static_zstd,migrated_fynedo \
  ./internal/nativev4 ./internal/patch
```

## v0.6.0 benchmark result

The final Windows amd64 campaign completed 580 successful benchmark rows. The
full-source cache reduced the 256 MiB scattered workload from about 8,484
positional reads and 512 MiB transferred to about 164 reads and 256 MiB.
However, eagerly reading and authenticating the complete image before parallel
application increased wall time by roughly 44-48% against v0.5.3 and raised
private memory above 440 MiB.

v0.6.1 therefore keeps the complete-image cache only for explicit `hdd` mode.
`auto`, `ssd`, and `nvme` use positional I/O until a bounded parallel span cache
is implemented.

The same campaign showed that the old automatic scheduler created more native
sessions than its two-writer limit could run. Automatic scheduling now permits
up to eight readers and four writers, and pool construction is capped by the
tightest CPU/read/write capacity before native sessions are allocated.

## v0.6.1 targeted rerun

Compare `one_256MB_scattered`, `one_256MB_shifted`,
`one_320MB_scattered_fallback`, and `multi_mixed_64` with:

- workers automatic;
- I/O profiles auto and nvme;
- memory limits auto and 128M;
- five measured iterations after one warm-up.

Acceptance signals:

- recover or exceed the v0.5.3 application baseline;
- preserve the v0.6.0 multi-file throughput;
- keep automatic application private memory below roughly 320 MiB;
- preserve patch bytes, output digests, reverse correctness, and transaction
  recovery;
- retain the creator CPU gains from the CDC and sparse optimizations.

The scheduler widths, session reservations, and cache eligibility helper are
runtime tuning points and do not change the V4 format.
