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
- Ordered multi-file patches with explicit source/target file pairs.
- SHA-256 source and target integrity metadata.
- Optional reverse differential for every file.
- Strict, versioned, and bounds-checked `.vipr` container.
- Immutable creation and application snapshots.
- Traversal-resistant installation access with `os.Root`.
- Transactional file replacement with best-effort rollback and explicit post-commit warnings.
- File-level and byte-level progress reporting.
- Fyne GUI plus deterministic CLI behavior.
- MIT-licensed Go code.

## Important path behavior

The source side of each file pair defines its path inside the patch. Viper
Patcher computes the nearest common parent directory of all source files and
stores every source path relative to that directory.

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

- An **Add file pair** action that first selects one source file and then its
  associated target file.
- A two-column table in which every row permanently represents one
  source/target pair. Each column displays paths relative to its nearest common
  parent directory.
- A dedicated comment section.
- A collapsed **Settings** section containing compression levels 1 through 22,
  optional reverse-patch generation, creator-only work-directory selection, and
  bounded per-file parallelism. Parallelism defaults to one worker and cannot
  exceed the logical CPU count.
- An output directory and `.vipr` filename.
- A conservative peak temporary-disk estimate shown before creation starts.
- Progress by file and by processed bytes.

The selected output file is opened only by the patch creation core. Cancelling
or failing validation therefore does not truncate an existing patch.

Both GUIs display the embedded `assets/logo.png`. The patcher additionally uses
an external `assets/logo.png` when that exact regular PNG file is placed in an
`assets` directory beside the patcher executable; invalid files fall back to
the embedded logo.
File and directory buttons use native Windows and macOS dialogs. Linux uses the
system `zenity` command when available and otherwise falls back to Fyne's file
dialog.

## Creator CLI

```text
creator --headless \
  --file-pair old/bin/game.exe::new/bin/game.exe \
  --file-pair old/data/assets.bin::new/data/assets.bin \
  --compression-level 12 \
  --comment "Version 1.1 update" \
  --create-reverse \
  --parallel 2 \
  --work-directory /path/to/temporary-storage \
  update.vipr
```

Supported parameters:

```text
--file-pair <source>::<target> Required. Repeat once per associated file pair.
[--compression-level <level>]  Default: 3.
[--comment <text>]             Default: Created with Viper-Patcher.
[--create-reverse]             Default: false.
[--work-directory <directory>] Optional creator temporary-data parent.
[--parallel <count>]           Parallel file operations. Default: 1.
[--headless]                   Force CLI mode.
[--version]                    Show version information.
[--help]                       Show help.
<output.vipr>                  Required. Patch file output.
```

The `::` delimiter belongs to the option syntax. Source and target paths are
parsed together, so their association cannot become misaligned. The CLI accepts
libzstd's complete compression-level range, including negative fast levels and
ultra levels. The GUI deliberately presents the conventional 1–22 range.

## Patcher GUI

The patcher starts with instructions to select a `.vipr` patch. If exactly one
regular `.vipr` file is present beside the patcher executable, it is selected
automatically. After selection, the patch comment is displayed read-only.
Selecting a target directory triggers a complete preflight:

- Every required path must resolve to a regular file without traversing a
  symbolic link.
- File hash and size must match the source state to enable **Patch**.
- When reverse data exists, the same content checks must match the target state
  to enable **Reverse**.
- A file that validly matches both states enables both directions.
- Mixed, unknown, non-regular, or missing states disable the affected directions
  and report the first problem.
- Stored Unix mode metadata is advisory. Application preserves the installed
  file mode on Unix and ignores Unix permission bits on Windows, so the same
  patch remains usable across supported operating systems.

Patch and directory selections are locked while an operation is running. The
operation uses captured selections rather than mutable GUI state and rejects a
patch whose SHA-256 digest changed after the displayed preflight inspection. The
CLI binds inspection and application to the same digest as well.

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
- Decompression stops before writing beyond the size declared by the VIPR
  header.

Reference files are memory-mapped. Target, patch, and output data are streamed
through bounded buffers, allowing detailed progress for large files. Patched
outputs are written through handles created beneath one traversal-resistant
installation root, avoiding a close-and-reopen race on temporary paths. Native
platform I/O, compression, decompression, and common error handling are kept in
separate C translation units.

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
go build -tags vipr_static_zstd,migrated_fynedo -o dist/creator.exe ./cmd/creator
go build -tags vipr_static_zstd,migrated_fynedo -o dist/patcher.exe ./cmd/patcher
```

The PowerShell build script detects a standard `C:\msys64` installation. For
another location, pass `-MSYS2Root "D:\path\to\msys64"` or set the
`MSYS2_ROOT` environment variable.

See [Building](docs/BUILDING.md) for dependencies, x86 instructions, and
creator temporary-data options.

## Testing

```sh
make check
```

The repository includes unit, integration, race, fuzz, native sanitizer, and
vulnerability-reachability checks for path normalization, container parsing,
malformed metadata, CLI validation, zstd streaming, immutable snapshots,
transaction rollback, GUI state models, patch creation, forward application,
reverse application, preflight states, output replacement, and integrity
checks. CI enforces at least 80% statement coverage across the non-GUI core and
compiles the complete GUI executables. See [Testing strategy](docs/TESTING.md).

## GitHub Actions

- `ci.yml` checks formatting, runs race and sanitizer tests, vets the code,
  executes `govulncheck`, enforces coverage, and compiles on every push and pull
  request.
- `release.yml` validates one release version, builds unsigned cross-platform
  archives, and grants write access only to the publication job.
- External actions are referenced by immutable commit identifiers.

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
