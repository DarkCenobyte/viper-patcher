# Performance tuning checkpoints

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
