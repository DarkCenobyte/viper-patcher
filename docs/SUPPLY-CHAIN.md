# Supply-chain controls

Release inputs and automation are intentionally reproducible and reviewable:

- zstd and BLAKE3 versions are pinned in source;
- downloaded source archives are verified against hard-coded SHA-256 values
  before extraction;
- GitHub Actions are referenced by immutable commit identifiers;
- `VERSION`, both Fyne metadata files, and the dated changelog section must agree;
- the CLI-only dependency graph is checked for Fyne, GLFW, GUI assets,
  `internal/gui`, and `internal/appmode`;
- release binaries are inspected for a dynamic zstd dependency, and CLI-only
  binaries are additionally inspected for GUI dynamic libraries;
- release archives contain third-party notices and collected Go module licenses.

## Build once, publish the exact result

Release archives are built only after every CI correctness job succeeds on a
`master` commit. A tag workflow does not rebuild them. It selects a successful
`ci.yml` push run by the exact commit SHA, downloads that run's artifacts, and
publishes all GUI and CLI-only archives in one job.

This prevents a tag build from drifting because of runner images, package
repositories, mutable external state, or a concurrent release workflow. A tag
on a different or failing commit cannot reuse artifacts from another run.

## Published metadata

The publication job emits:

- `SBOM.spdx.json` from the resolved Go module graph;
- `BUILD-PROVENANCE.json` containing repository, tag, commit, exact CI run,
  publication workflow run, and attempt identifiers;
- `SHA256SUMS.txt` covering all twelve Windows, Linux, and macOS GUI/CLI archives
  plus the SBOM and provenance records.

The SBOM deliberately uses `NOASSERTION` for module licenses because release
archives carry the authoritative collected license texts. This avoids inventing
license classifications from module names.

The provenance JSON is descriptive release metadata, not a cryptographic SLSA
attestation. A future signing phase may add GitHub artifact attestations or
Sigstore without changing the build-once promotion model.
