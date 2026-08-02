# Supply-chain controls

Release inputs and automation are intentionally reproducible and reviewable:

- zstd and BLAKE3 versions are pinned in source;
- downloaded source archives are verified against hard-coded SHA-256 values
  before extraction;
- GitHub Actions are referenced by immutable commit identifiers;
- release archives and metadata receive GitHub artifact attestations signed
  through short-lived OIDC/Sigstore credentials;
- `VERSION`, both Fyne metadata files, and the dated changelog section must agree;
- the CLI-only dependency graph is checked for Fyne, GLFW, GUI assets,
  `internal/gui`, and `internal/appmode`;
- release binaries are inspected for a dynamic zstd dependency, and CLI-only
  binaries are additionally inspected for GUI dynamic libraries;
- release archives contain third-party notices and collected Go module licenses.

## Exact-commit build promotion

Normal stable releases reuse archives built after every CI correctness job
succeeds on the exact `master` commit. The release workflow selects artifacts by
both commit SHA and requested version, downloads that run's artifacts, and
publishes all GUI and CLI-only archives in one job.

Prereleases and manual revisions may request a version that differs from the
tracked `VERSION`. In that case, or when `force_rebuild` is selected, the release
workflow dispatches the complete `ci.yml` pipeline against the same immutable
commit with an explicit version override. Publication waits for that exact run
to succeed and never substitutes artifacts from another SHA. The override is
injected into binaries and package metadata without modifying tracked files.

This preserves exact-commit provenance while allowing `v1.0.0-alpha.1`,
`v1.0.0-rc.2`, and rebuilt release assets. Versioned workflow-artifact names
prevent a successful CI run for one version from being mistaken for another.

## Published metadata

The publication job emits:

- `SBOM.spdx.json` from the resolved Go module graph;
- `BUILD-PROVENANCE.json` containing repository, tag, requested version,
  commit, exact CI run, whether its artifacts were reused, publication workflow
  run, and attempt identifiers;
- `SHA256SUMS.txt` covering all twelve Windows, Linux, and macOS GUI/CLI archives
  plus the SBOM and provenance records;
- a signed GitHub release-provenance attestation that uses
  `BUILD-PROVENANCE.json` as its custom predicate and binds every archive, the
  SBOM, and the checksum manifest;
- a GitHub SBOM attestation binding all twelve release archives to
  `SBOM.spdx.json`.

The SBOM deliberately uses `NOASSERTION` for module licenses because release
archives carry the authoritative collected license texts. This avoids inventing
license classifications from module names.

## Release provenance attestation

`BUILD-PROVENANCE.json` is descriptive metadata when viewed on its own. The
release workflow also embeds that exact JSON document as the predicate of a
GitHub artifact attestation. Its predicate type is the stable URL of this
section. The signed statement binds the release archives, SBOM, and checksum
manifest to the repository, requested tag and version, exact source commit,
validated CI run, and publication run recorded by the predicate.

The attestation is signed through Sigstore, stored by GitHub, and verifiable
with `gh attestation verify`. It deliberately describes exact-commit promotion
rather than claiming that the publication job compiled binaries which were
actually built and tested by the referenced CI run.
