# Contributing

1. Discuss substantial format, compatibility, or release-pipeline changes in an
   issue first.
2. Keep identifiers, filenames, directories, comments, documentation, commit
   messages, and user-facing strings in English.
3. Add tests for every behavior change. After 1.0.0, an incompatible public CLI,
   API, or `.vipr` format change requires a new major version, an explicit
   compatibility check, and migration notes. Compatible additions and fixes
   follow Semantic Versioning.
4. Keep cgo code small, defensive, and isolated under `internal/nativev4`.
5. Treat patch files, installation paths, external assets, release inputs, and
   downloaded build dependencies as untrusted data.
6. Preserve the GUI-free boundary of `cmd/creator-cli` and `cmd/patcher-cli`;
   neither command may depend on Fyne, GLFW, GUI assets, `internal/gui`, or
   `internal/appmode`.

## Local checks

Install the platform build dependencies described in
[`docs/BUILDING.md`](docs/BUILDING.md), then run:

```sh
make check
make build
```

`make check` downloads and verifies the pinned native dependencies when needed,
builds them once, runs the complete race-enabled test suite with the
`vipr_static_zstd,migrated_fynedo` tags, runs `go vet`, and checks formatting.
`make build` compiles `creator`, `patcher`, `creator-cli`, and `patcher-cli`.

Before submitting workflow or script changes, also validate the relevant shell,
PowerShell, and GitHub Actions syntax. Release changes must preserve:

- read-only permissions for test and build jobs;
- full commit-SHA pins for external actions;
- synchronized `VERSION`, Fyne metadata, and changelog entries;
- one build per `master` commit and publication from the successful CI run for
  the exact tagged SHA;
- a single publication job containing both GUI and CLI-only assets.

Contributions are accepted under the repository's MIT license.
