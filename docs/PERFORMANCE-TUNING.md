# Performance tuning checkpoints

The final two commits in this series intentionally change the measured hot
paths and require a targeted rerun before default constants are adjusted.

## Source cache

Compare `one_256MB_scattered`, `one_256MB_shifted`, and `multi_mixed_64` with
workers 1/auto and windows auto/1M/8M.

Acceptance signals:

- scattered source read transfer approaches one source image rather than two;
- positional read operations fall by at least one order of magnitude;
- output digests and patch sizes remain unchanged;
- private memory remains below the configured operation budget;
- fallback behavior remains correct when the cache cannot reserve memory.

## CDC and sparse scan

Compare balanced, apply-speed, and patch-size creation profiles.

Acceptance signals:

- no material patch-size regression on scattered and shifted corpora;
- creator CPU time decreases on sparse and CDC-heavy inputs;
- application output remains byte-identical;
- reverse patches retain the same correctness guarantees.

The constants in `resource_budget_v4.go`, `operation_scheduler_v4.go`, and the
source-cache eligibility helper are tuning points, not format commitments.
