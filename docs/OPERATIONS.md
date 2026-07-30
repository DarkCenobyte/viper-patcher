# Operational guarantees and limits

This document consolidates the runtime policy introduced after v0.5.3.

## Index and 32-bit limits

The V4 wire format retains its 256 MiB format ceiling. Production decoding
applies architecture-aware runtime limits before allocating the index and then
accounts for the expanded Go representation.

The 32-bit profile accepts at most a 32 MiB wire index and a conservative
192 MiB decoded estimate. The 64-bit profile retains the format wire ceiling
with a 512 MiB decoded estimate. Aggregate files, windows, digests, and string
bytes are also bounded.

## Memory

`--memory-limit` accepts `auto` or a byte size such as `512M` or `2G`. A
reservation covers all native sessions planned for a file and is acquired in
one operation. Partial multi-unit acquisition is forbidden.

Automatic defaults are architecture- and operation-aware. A source cache is
optional: it is enabled only when the complete reservation succeeds and falls
back to positional I/O otherwise.

## Scheduling and I/O profiles

One scheduler coordinates leaf CPU, read, and write work across all files.
File coordinators do not hold scheduler tokens while waiting for nested work.

- `hdd` serializes read/write-heavy leaves;
- `ssd` permits moderate parallelism;
- `nvme` permits wider queues;
- `auto` uses conservative SSD-like I/O limits and the process worker budget.

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

## Metadata

See `METADATA.md`. V4 is content-only, preserves ordinary installed Unix
permissions, rejects privilege mode bits, and does not transport ownership,
ACLs, xattrs, capabilities, DACLs, ADS, timestamps, or hard-link topology.

## Benchmark gates

The source-cache and CDC/sparse commits are intentionally the last functional
commits. Use `PERFORMANCE-TUNING.md` for the targeted rerun matrix before
adjusting automatic limits or scheduler widths.
