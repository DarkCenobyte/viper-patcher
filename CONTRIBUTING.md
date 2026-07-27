# Contributing

1. Discuss substantial format or compatibility changes in an issue first.
2. Keep identifiers, filenames, directories, comments, documentation, commit
   messages, and user-facing strings in English.
3. Add tests for every behavior change. A deliberate `.vipr` compatibility break
   requires a new application minor version, a format-version check, and an
   explicit migration note.
4. Keep cgo code small, defensive, and isolated under `internal/zstd`.
5. Treat patch files, installation paths, external assets, and release inputs as
   untrusted data.

## Local checks

Install the platform build dependencies described in
[`docs/BUILDING.md`](docs/BUILDING.md), then run:

```sh
make check
make build
```

`make check` downloads and verifies libzstd 1.5.7 when needed, builds it once,
runs the complete race-enabled test suite with the
`vipr_static_zstd,migrated_fynedo` tags, runs `go vet`, and checks formatting.

Before submitting workflow or script changes, also validate the relevant shell,
PowerShell, and GitHub Actions syntax. Release changes must preserve read-only
permissions for build jobs and pin every external action to a full commit SHA.

Contributions are accepted under the repository's MIT license.
