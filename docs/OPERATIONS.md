# Operational guarantees and limits

This document consolidates the runtime and release policy used by Viper-Patcher
1.0.0.

## Index and 32-bit limits

The V4 wire format retains its 256 MiB format ceiling. Production decoding
applies architecture-aware runtime limits before allocating the index and then
accounts for the expanded Go representation.

The 32-bit profile accepts at most a 32 MiB wire index and a conservative
192 MiB decoded estimate. The 64-bit profile retains the format wire ceiling
with a 512 MiB decoded estimate. Aggregate files, windows, digests, strings, and
fine-verification tables are also bounded.

## Memory

`--memory-limit` accepts `auto` or a byte size such as `512M` or `2G`. A
reservation covers all native sessions planned for a file and is acquired in
one operation. Partial multi-unit acquisition is forbidden.

Automatic defaults are architecture- and operation-aware. The complete-image
source cache is enabled only for explicit `hdd` mode and only when its complete
reservation succeeds; all other profiles use bounded positional I/O. Optional
cache allocation failure falls back to positional I/O rather than failing the
application.

## Scheduling and I/O profiles

One scheduler coordinates leaf CPU, read, and write work across all files. File
coordinators do not hold scheduler tokens while waiting for nested work. Native
session pools are capped by the tightest scheduler resource and operation-wide
memory budget before allocation.

- `hdd` serializes read/write-heavy leaves;
- `ssd` permits up to four readers and two writers;
- `nvme` permits up to eight readers and four writers;
- `auto` uses the same eight-reader/four-writer ceiling as `nvme`, bounded by
  the process worker budget.

Both hybrid CLI mode and the dedicated `creator-cli`/`patcher-cli` executables
use exactly the same scheduler, memory, verification, and transaction policies.

## Creator snapshots

Snapshots first attempt a native copy-on-write clone. Unsupported filesystems
fall back to the stable 1 MiB copy. Snapshot pairs may run concurrently within
the scheduler and memory budget.

## Interrupted application

Before replacement renames, the patcher writes a bounded transaction journal
inside the installation root. A later application recovers journals before
preparing new outputs.

- incomplete journals roll back retained backups;
- committed journals finish cleanup;
- journal paths are validated as local target/temp/backup triples;
- recovery is idempotent;
- `durable` additionally synchronizes journal and root-directory state;
- `buffered` remains best effort across sudden power loss.

Handled failures are rollback-capable, but Viper-Patcher does not claim a
crash-consistent multi-file transaction after power loss or kernel failure.

## Metadata

See `METADATA.md`. V4 is content-only, preserves ordinary installed Unix
permissions, rejects privilege mode bits, and does not transport ownership,
ACLs, xattrs, capabilities, DACLs, ADS, timestamps, or hard-link topology.

## Release promotion

CI builds the GUI and CLI-only archives once after all correctness jobs pass on
a `master` commit. A `v*` tag may publish only the artifacts retained by a
successful CI push run for that exact SHA. Tag publication performs no binary
rebuild and produces one unified release with checksums, SBOM, and provenance.

## Benchmark gates

Runtime tuning changes should be evaluated with the workloads and counters in
`PERFORMANCE-TUNING.md`. Correctness, output identities, reverse application,
transaction recovery, bounded 32-bit behavior, and patch-format compatibility
remain hard gates. Performance results do not replace the complete CI gate used
for release promotion.
