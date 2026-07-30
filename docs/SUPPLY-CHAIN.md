# Supply-chain controls

Release inputs and automation are intentionally reproducible and reviewable:

- zstd and BLAKE3 versions are pinned in source;
- downloaded archives are verified against hard-coded SHA-256 values before
  extraction;
- GitHub Actions are referenced by immutable commit identifiers;
- release archives contain third-party notices and collected Go module
  licenses;
- the publish job emits `SBOM.spdx.json` from the resolved Go module graph;
- the publish job emits `BUILD-PROVENANCE.json` with the tag, commit, workflow,
  run, repository, and runner identity;
- `SHA256SUMS.txt` covers every platform archive plus the SBOM and provenance
  records.

The SBOM deliberately uses `NOASSERTION` for module licenses because release
archives already carry the authoritative collected license texts. This avoids
inventing license classifications from module names.
