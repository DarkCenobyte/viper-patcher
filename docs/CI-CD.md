# GitHub Actions CI/CD

Two workflows are active. Every external GitHub Action is referenced by its
full immutable commit identifier rather than a mutable tag.

## Continuous integration and release artifacts

`.github/workflows/ci.yml` runs on pushes to `master`, pull requests, and manual
requests with read-only repository permissions. Manual runs may set the optional
`release_version` input to build versioned release archives without modifying
`VERSION`, Fyne metadata, or the changelog. Its correctness gate:

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
toolchain. A manual CI run with `release_version` performs the same complete
correctness gate and package build, but injects the requested version through
linker and packaging metadata while leaving tracked files unchanged:

- hybrid `creator`/`patcher` archives for Windows amd64/386, Linux
  amd64/386/arm64, and macOS arm64;
- GUI-free `creator-cli`/`patcher-cli` archives for the same targets.

The archives are retained as workflow artifacts. Pull requests never build or
upload publishable release artifacts.

## Publication from an exact green commit

`.github/workflows/release.yml` runs for pushed `v*` tags and may also be started
manually with a release tag, source branch/tag, and optional forced rebuild. The
accepted tag forms are:

```text
vMAJOR.MINOR.PATCH
vMAJOR.MINOR.PATCH-suffix
```

The suffix may contain letters, digits, dots, and hyphens. Any suffix marks the
GitHub release as a prerelease, so `v1.0.0-alpha.1` and `v1.0.0-rc.2` do not
require changing the repository's `1.0.0` metadata.

The publication job:

1. resolves the exact commit selected by the pushed tag or manual `target_ref`;
2. searches successful `ci.yml` runs for that exact SHA and for artifacts named
   with the requested release version;
3. reuses those artifacts when available, unless `force_rebuild` was selected;
4. otherwise dispatches a manual `ci.yml` run for the same exact commit with the
   requested `release_version`, waits for the complete correctness and package
   pipeline, and rejects every non-successful conclusion;
5. downloads the twelve expected GUI and CLI-only archives from that exact CI
   run;
6. regenerates the SBOM, records both CI and publication run identities, and
   creates one checksum manifest covering every archive;
7. creates or revises one GitHub release, replacing existing assets when the
   same tag is rebuilt.

A manual release uses a temporary branch ref only while dispatching the exact
commit's CI build, preventing a moving source branch from changing the compiled
SHA. The temporary ref is removed after the CI run is resolved. Only the
publication job receives `actions: write` and `contents: write`; the dispatched
CI workflow remains read-only.

## Release procedures

### Normal stable release

1. Update `VERSION`, both Fyne metadata files, and `CHANGELOG.md` in the release
   commit.
2. Merge that commit into `master` and wait for the complete `CI` workflow.
3. Tag that exact commit and push the tag:

```sh
git tag -a v1.0.0 <validated-commit> -m "v1.0.0"
git push origin v1.0.0
```

The release workflow reuses the already-built `1.0.0` artifact set from that
exact successful CI run.

### Prerelease without changing tracked version files

Tag the desired commit with a SemVer prerelease suffix:

```sh
git tag -a v1.0.0-rc.1 <candidate-commit> -m "v1.0.0-rc.1"
git push origin v1.0.0-rc.1
```

When no exact-SHA CI artifact set exists for `1.0.0-rc.1`, the release workflow
dispatches `ci.yml` with that version override. The binaries, archive names, checksums, and provenance use
`1.0.0-rc.1`, while `VERSION` and the Fyne source files may remain `1.0.0`. The macOS bundle keeps
the numeric `1.0.0` short version required by platform bundle metadata; the
embedded Viper-Patcher build information still reports `1.0.0-rc.1`.

### Manual build, revision, or forced rebuild

Run the `Release` workflow manually and provide:

- `tag`: the final tag, such as `v1.0.0-rc.2`;
- `target_ref`: the source branch or existing tag;
- `force_rebuild`: select this to ignore an existing matching artifact set.

The workflow creates the final tag at the resolved commit when needed. Running
it again for the same tag can rebuild and replace release assets without moving
an existing tag. A tag that already points to another commit is rejected.

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
