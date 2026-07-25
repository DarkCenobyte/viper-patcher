# Viper-Patcher

![Viper-Patcher logo](assets/logo.png)

[![CI](https://github.com/DarkCenobyte/viper-patcher/actions/workflows/ci.yml/badge.svg)](https://github.com/DarkCenobyte/viper-patcher/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Viper Patcher is a cross-platform differential patch creator and applicator for
binary files. It provides two executables:

- `creator`: builds versioned `.vipr` patches from ordered source/target pairs.
- `patcher`: validates an installation and applies a forward or reverse patch.

Both executables start a desktop GUI when launched without arguments and a
graphical environment is available. Any command-line argument selects CLI mode.
`--headless` explicitly forces CLI mode. When no graphical environment is
available, the application prints a warning and falls back to CLI mode.

## Key properties

- Exact libzstd **1.5.7** dependency, statically linked in release builds.
- Patch-from behavior implemented directly through libzstd's advanced API.
- Dynamic per-file compression parameters based on reference and target size.
- Ordered multi-file patches with source-relative paths.
- SHA-256 source and target integrity metadata.
- Optional reverse differential for every file.
- Strict, versioned, and bounds-checked `.vipr` container.
- Transactional application and rollback.
- File-level and byte-level progress reporting.
- Fyne GUI plus deterministic CLI behavior.
- MIT-licensed Go code.

## Important path behavior

The source file list defines paths inside the patch. Viper Patcher computes the
nearest common parent directory of all source files and stores every source path
relative to that directory.

For example:

```text
old/bin/game.exe
old/data/assets.bin
```

produces these patch paths:

```text
bin/game.exe
data/assets.bin
```

Target files may have different names and live in unrelated directories during
creation. Their locations are not used when applying the patch.

## Creator GUI

The creator interface provides:

- Multiple source files.
- Multiple target files in matching order.
- Compression levels 1 through 22.
- A patch comment.
- `Generate a reverse patch`.
- A `.vipr` save dialog.
- Progress by file and by processed bytes.

The source and target lists must contain exactly the same number of entries.

## Creator CLI

```text
creator --headless \
  --source-files old/bin/game.exe \
  --target-files new/bin/game.exe \
  --source-files old/data/assets.bin \
  --target-files new/data/assets.bin \
  --output update.vipr \
  --compression-level 12 \
  --comment "Version 1.1 update" \
  --create-reverse
```

Supported parameters:

```text
--source-files <file>          Required. Repeat once per source file.
--target-files <file>          Required. Repeat once per target file.
--output <file.vipr>           Required in CLI mode.
[--compression-level <level>]  Default: 3.
[--comment <text>]             Default: Created with Viper-Patcher.
[--create-reverse]             Default: false.
[--headless]                   Force CLI mode.
[--version]                    Show version information.
[--help]                       Show help.
```

The CLI accepts libzstd's complete compression-level range, including negative
fast levels and ultra levels. The GUI deliberately presents the conventional
1–22 range.

## Patcher GUI

The patcher starts with instructions to select a `.vipr` patch. After selection,
the patch comment is displayed read-only. Selecting a target directory triggers
a complete preflight:

- Every required file must exist.
- Every file must match all source hashes to enable **Patch**.
- When reverse data exists, every file must match all target hashes to enable
  **Reverse**.
- Mixed, unknown, or missing states disable both actions and report the first
  affected path plus the total problem count.

## Patcher CLI

Forward application:

```text
patcher --headless --patch-file update.vipr /path/to/application
```

Reverse application:

```text
patcher --headless --patch-file update.vipr --reverse /path/to/application
```

Supported parameters:

```text
--patch-file <file.vipr>      Required.
<target-directory>            Required positional argument.
[--reverse]                   Default: false.
[--headless]                  Force CLI mode.
[--version]                   Show version information.
[--help]                      Show help.
```

## Patch-from implementation

Viper Patcher does not invoke an external `zstd` executable. Its small cgo
wrapper applies the same core configuration used by zstd 1.5.7 patch-from mode:

- Compression parameters are derived from target and reference sizes.
- Window size is adjusted dynamically per file.
- Long-distance matching is enabled when required by the effective window.
- Dedicated dictionary search is enabled.
- The source file is attached with `ZSTD_CCtx_refPrefix`.
- Application attaches the current file with `ZSTD_DCtx_refPrefix`.
- Target content size and checksum are stored in each zstd frame.

Reference files are memory-mapped. Target, patch, and output data are streamed
through bounded buffers, allowing detailed progress for large files.

## Building

Go does not publish a separate LTS channel. This repository pins Go 1.26.5,
the latest supported stable patch release selected for this project, and
statically builds the verified zstd 1.5.7 source.

Linux/macOS:

```sh
go mod download
./scripts/fetch-zstd.sh
./scripts/build-zstd.sh
make build
```

Windows PowerShell:

```powershell
go mod download
./scripts/fetch-zstd.ps1
./scripts/build-zstd.ps1 -Architecture x64
$env:CGO_ENABLED = "1"
$env:GOARCH = "amd64"
$env:CC = "C:\msys64\mingw64\bin\gcc.exe"
go build -tags vipr_static_zstd -o dist/creator.exe ./cmd/creator
go build -tags vipr_static_zstd -o dist/patcher.exe ./cmd/patcher
```

See [Building](docs/BUILDING.md) for dependencies, x86 instructions, and module
path setup.

## Testing

```sh
make test
make vet
```

The repository includes unit and integration tests for path normalization,
container parsing, malformed metadata, CLI validation, zstd streaming,
progress, patch creation, forward application, reverse application, preflight
states, output replacement, and integrity checks. CI enforces at least 80%
statement coverage across the non-GUI core and compiles the complete GUI
executables. See [Testing strategy](docs/TESTING.md).

## GitHub Actions

- `ci.yml` checks formatting, tests, vets, and compiles on every push and pull
  request.
- `release.yml` creates unsigned cross-platform archives when a `v*` tag is
  pushed.

See [GitHub Actions CI/CD](docs/CI-CD.md) for initial repository configuration,
release tags, and future code-signing insertion points.

## Platform targets

| Operating system | Architecture | Release workflow |
|---|---|---|
| Windows | x86-64 | Yes |
| Windows | x86 | Yes |
| Linux | x86-64 | Yes |
| Linux | x86 | Yes |
| Linux | arm64 | Yes |
| macOS | arm64 | Yes |

A 32-bit process has a much smaller virtual address space. Because the reference
file is memory-mapped for patch-from matching, x86 builds should not be used for
very large reference files.

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [VIPR format](docs/FORMAT.md)
- [Building](docs/BUILDING.md)
- [Testing strategy](docs/TESTING.md)
- [GitHub Actions CI/CD](docs/CI-CD.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

## Licenses

Viper Patcher is licensed under the [MIT License](LICENSE).

libzstd 1.5.7 is an upstream third-party dependency available under the BSD
3-Clause license or GPLv2. This project uses the BSD licensing option. Fyne and
all transitive Go modules retain their own licenses. Release archives include
zstd's upstream license files plus automatically collected top-level license
and notice files for every resolved Go module.
