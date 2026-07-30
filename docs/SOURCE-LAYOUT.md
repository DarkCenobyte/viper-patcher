# Source layout

V4 is the only supported patch format, so implementation filenames no longer
repeat the `_v4` suffix. The wire version remains explicit in constants,
generated descriptors, magic values, and format documentation.

The patch package is separated by responsibility:

- `apply.go` and `create.go`: operation orchestration;
- `open.go`: stable patch opening and integrity setup;
- `filesystem.go`: replacement commit and rollback;
- `transaction_journal.go`: crash recovery;
- `resource_budget.go`: atomic memory reservations;
- `operation_scheduler.go`: CPU and I/O leaf scheduling;
- `metadata_policy_*.go`: platform metadata rules;
- `apply_progress.go`: weighted monotonic progress.

The format package keeps wire encoding in `format.go` and runtime allocation
policy in `runtime_limits.go`. Native V4 names remain versioned because they
define an ABI and wire descriptor layout rather than only Go source grouping.
