# GitHub Actions CI/CD

Two workflows are active. Every external GitHub Action is referenced by its
full immutable commit identifier rather than a mutable tag.

## Continuous integration and release artifacts

`.github/workflows/ci.yml` runs on pushes to `master`, pull requests, and manual
requests with read-only repository permissions. Its correctness gate:

1. downloads and SHA-256 verifies the pinned zstd 1.5.7 and BLAKE3 sources;
2. checks formatting;
3. runs the complete test suite with the race detector;
4. runs native V4 integration tests under Clang ASan/UBSan;
5. runs `go vet`, Staticcheck, and `govulncheck`;
6. enforces at least 80% statement coverage across the non-GUI core, including
   `internal/commandctx`, `internal/nativev4`, and `internal/workerbudget`;
7. runs the supported core set on Linux 386 and Windows amd64/386;
8. verifies that `creator-cli` and `patcher-cli` have no GUI packages in their
   dependency graphs;
9. compiles all four executables.

After every correctness job succeeds on a `master` commit, the same CI run
validates `VERSION`, both Fyne metadata files, and the changelog. It then builds
all release archives once on their native runner or supported multilib
toolchain:

- hybrid `creator`/`patcher` archives for Windows amd64/386, Linux
  amd64/386/arm64, and macOS arm64;
- GUI-free `creator-cli`/`patcher-cli` archives for the same targets.

The archives are retained as workflow artifacts. Pull requests never build or
upload publishable release artifacts.

## Publication from an exact green commit

`.github/workflows/release.yml` runs only when a `v*` tag is pushed. It does not
compile the project again. The publication job:

1. verifies that the tag exactly matches `VERSION`, both `FyneApp.toml` files,
   and a dated changelog section;
2. locates a successful `ci.yml` push run whose `head_sha` is exactly the tagged
   commit and whose `head_branch` is `master`;
3. waits for that run when it is still active and rejects a completed failing
   run;
4. downloads the twelve expected GUI and CLI-only archives from that exact CI
   run;
5. regenerates the SBOM, records both CI and publication run identities, and
   creates one checksum manifest covering every archive;
6. publishes all assets in one GitHub Release job.

Only this final job receives `actions: read` and `contents: write`. Build and
test jobs remain read-only. The removed `release-cli.yml` workflow can no longer
race the main release or create a partially populated release.

## Release procedure

1. Update `VERSION`, both Fyne metadata files, and `CHANGELOG.md` in the release
   commit.
2. Merge that commit into `master`.
3. Wait for the complete `CI` workflow to succeed and retain its release
   artifacts.
4. Tag that exact commit and push the tag:

```sh
git tag -a v1.0.0 <validated-commit> -m "v1.0.0"
git push origin v1.0.0
```

Pushing the tag while CI is still running is supported: the publication job
waits for the exact commit's result. A tag on a commit without a successful
`master` push run is rejected. Release artifacts should be published before
their configured workflow-artifact retention period expires.

## Repository configuration

- Keep the default workflow token permission read-only.
- Protect `master` and require the `CI` workflow before merge.
- Restrict creation or update of `v*` tags to trusted maintainers.
- Do not permit a release workflow to build from a different ref than the tag.

## Future signing

Keep compilation and signing separate. Signing may be inserted after CI builds
and before archives are uploaded, or in a trusted promotion job that preserves
and records the unsigned artifact digest:

- Windows: sign all `.exe` files with Certum, `signtool`, or an OIDC-backed
  signing service;
- macOS: use `codesign`, notarize with `notarytool`, then staple tickets;
- Linux and archive manifests: use GPG or Sigstore.

Store certificates, passwords, and API credentials in GitHub encrypted secrets
or an OIDC-backed signing service. Never commit private keys.
