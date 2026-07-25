# GitHub Actions CI/CD

Two workflows are included. Every external GitHub Action is referenced by its
full commit identifier rather than a mutable tag.

## Continuous integration

`.github/workflows/ci.yml` runs on pushes and pull requests with read-only
repository permissions. It:

1. Installs desktop build dependencies.
2. Downloads and verifies zstd 1.5.7.
3. Builds the static library.
4. Checks formatting.
5. Runs the complete test suite with the race detector.
6. Runs native patch and zstd tests with AddressSanitizer and
   UndefinedBehaviorSanitizer.
7. Runs `go vet`.
8. Runs `govulncheck`.
9. Enforces at least 80% statement coverage across core packages.
10. Compiles both executables, including their Fyne GUI packages.

## Releases

`.github/workflows/release.yml` runs for tags matching `v*` and can also be
started manually. It builds these unsigned archives:

- Windows x64
- Windows x86
- Linux x64
- Linux x86
- Linux arm64
- macOS arm64

Each archive contains `creator`, `patcher`, the MIT license, the README,
libzstd's upstream license files, and collected license/notice files for every
resolved Go module. A final job generates `SHA256SUMS.txt` and publishes a
GitHub Release.

A dedicated read-only job resolves the release version once. Manual input is
passed through an environment variable, validated against the accepted version
syntax, and exported as job outputs. Build scripts consume only those validated
outputs; untrusted expression text is never inserted directly into Bash or
PowerShell source.

All build jobs remain read-only. Only the final publication job receives
`contents: write`, after every platform artifact has been produced successfully.

## Initial repository setup

1. Replace the placeholder module path and commit `go.sum` after `go mod tidy`.
2. Push the repository to GitHub.
3. Open **Settings → Actions → General** and allow GitHub Actions.
4. Keep the default workflow token permissions read-only. The publication job
   declares its narrower `contents: write` requirement directly.
5. Push normally to run CI.
6. Create and push a release tag:

```sh
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

## Future signing

Keep compilation and signing separate. Add signing after each platform build
and before license collection/package creation:

- Windows: sign both `.exe` files with `signtool` or Azure Trusted Signing.
- macOS: use `codesign`, notarize with `notarytool`, then staple tickets.
- Linux: sign release archives and `SHA256SUMS.txt` with GPG or Sigstore.

Store certificates, passwords, and API credentials in GitHub encrypted secrets
or an OIDC-backed signing service. Never commit private keys.
