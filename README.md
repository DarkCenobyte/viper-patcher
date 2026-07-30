# Viper-Patcher

![Viper-Patcher logo](assets/logo.png)

[![CI](https://github.com/DarkCenobyte/viper-patcher/actions/workflows/ci.yml/badge.svg?branch=master&event=push)](https://github.com/DarkCenobyte/viper-patcher/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Platforms](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS-informational)](#platform-targets)
[![Latest release](https://img.shields.io/github/v/release/DarkCenobyte/viper-patcher?sort=semver)](https://github.com/DarkCenobyte/viper-patcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/DarkCenobyte/viper-patcher/total)](https://github.com/DarkCenobyte/viper-patcher/releases)
[![GitHub stars](https://img.shields.io/github/stars/DarkCenobyte/viper-patcher)](https://github.com/DarkCenobyte/viper-patcher/stargazers)
[![Open issues](https://img.shields.io/github/issues/DarkCenobyte/viper-patcher)](https://github.com/DarkCenobyte/viper-patcher/issues)

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
- VIPR format version 4 with a binary trailing index and independent adaptive windows.
- Per-window SAME, COPY, compact delta, raw/zstd replacement, ZERO, and RUN strategies.
- Content-defined chunking keeps COPY/ADD patches effective across insertions and deletions.
- Ordered multi-file patches with explicit source/target file pairs.
- A single native C BLAKE3 implementation for source, target, index, and patch identities.
- Optional reverse differential for every file.
- Strict, bounds-checked `.vipr` format 4 container; every earlier version is rejected.
- Immutable creator snapshots and handle-based fast application.
- Adaptive worker allocation across files and 8 MiB output groups, with native positional
  I/O and clone-on-write fast paths where the operating system supports them.
- Traversal-resistant installation access with `os.Root`.
- Rollback-capable file replacement for handled errors, with generated-file
  syncs and one parent-directory sync before backup cleanup on Unix-like systems.
- No claim of crash-consistent multi-file transactions after power loss or kernel failure.
- File-level, byte-level, and monotonic overall progress reporting.
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
  a worker target. It defaults to **Auto**, follows the current process CPU limit
  through `GOMAXPROCS`, and cannot exceed the logical CPU count; overlapping
  verification and decoding may use helper goroutines.
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
  --workers 2 \
  --window-size auto \
  --optimize balanced \
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
[--workers <count>]            0 (automatic) or 1..logical CPU count. Default: 0.
[--window-size <size>]         auto|256K|512K|1M|2M|4M|8M. Default: auto.
[--optimize <profile>]         balanced|apply-speed|patch-size. Default: balanced.
[--headless]                   Force CLI mode.
[--version]                    Show version information.
[--help]                       Show help.
<output.vipr>                  Required. Patch file output.
```

The `::` delimiter belongs to the option syntax. Source and target paths are
parsed together, so their association cannot become misaligned. `--workers 0`
selects the current process-aware `GOMAXPROCS` value, capped by the logical CPU
count; explicit values from 1 through that logical CPU count are accepted. The
CLI accepts libzstd's complete compression-level range, including negative fast
levels and ultra levels. The GUI deliberately presents the conventional 1–22
range.

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
- Patch metadata is content-only and stores no Unix permission fields. Application
  preserves the installed file mode on Unix and ignores Unix permission semantics
  on Windows, so the same patch remains usable across supported operating systems.

Patch and directory selections are locked while an operation is running. The
operation uses captured selections and rejects a patch whose BLAKE3 fingerprint
changed after the displayed GUI preflight. The application core then prepares
files through the same handle-based fast path used by the CLI. Preflight hashing
uses a bounded parallel worker plan, and a successful operation updates the known
forward/reverse readiness without an immediate redundant full output rescan.

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
[--workers <count>]           0 (automatic) or 1..logical CPU count. Default: 0.
[--verify <mode>]             referenced|strict|output. Default: referenced.
[--durability <mode>]         buffered|durable. Default: buffered.
[--io-profile <profile>]      auto|hdd|ssd|nvme. Default: auto.
[--headless]                  Force CLI mode.
[--version]                   Show version information.
[--help]                      Show help.
```

`--workers 0` uses the same process-aware automatic policy as the creator.
Explicit values from 1 through the logical CPU count are accepted. The value is
a scheduling target rather than a strict process-wide goroutine limit.

The CLI parses the patch without a redundant whole-file fingerprint pass and does
not run a separate full-file inspection before application.

## Format 4 implementation

Viper Patcher does not invoke an external `zstd` executable. VIPR V4 stores
contiguous payloads followed by a binary trailing index and a fixed footer, so
application can seek directly to metadata without scanning payloads.

Each file uses one bounded window size, while every window independently selects
SAME, COPY, compact delta, raw/zstd replacement, ZERO, or RUN. Go owns policy,
path safety, scheduling, temporary files, and rollback-capable commit. Coarse
cgo calls delegate BLAKE3, zstd, positional I/O, instruction decoding, output
writes, and window/group verification to the native C data plane.

Application distributes work across independent files and canonical 8 MiB output
groups. When an equal-size output contains at least 90% SAME windows, Viper
attempts a copy-on-write clone and reconstructs only changed windows; unsupported
filesystems transparently fall back to normal reconstruction. `referenced`,
`strict`, and `output` verification profiles control source validation without
weakening index, window, group, or output integrity checks.

Only format 4 patches are accepted. Earlier `.vipr` files remain usable through
their tagged historical releases. See [VIPR format V4](docs/FORMAT-V4.md),
[V4 architecture](docs/ARCHITECTURE-V4.md), and
[native V4 implementation](docs/NATIVE-V4.md).

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

The repository includes unit, integration, race, fuzz, native sanitizer,
Staticcheck, and vulnerability-reachability checks for path normalization,
container parsing, format 4 validation, rejection of earlier formats, malformed
binary indexes, window descriptors, and native instruction streams, CLI validation,
zstd streaming, parallel positional reads, transaction rollback, GUI state models,
patch creation, forward application, reverse application, preflight states, output
replacement, and BLAKE3 integrity. CI enforces at least 80% statement coverage
across the non-GUI core and compiles the complete GUI executables. See
[Testing strategy](docs/TESTING.md).

## GitHub Actions

- `ci.yml` checks formatting, runs race and sanitizer tests, vets the code,
  executes Staticcheck and `govulncheck`, enforces coverage, and compiles on every
  push and pull request.
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

A 32-bit process has a much smaller virtual address space. V4 keeps work bounded
through independently encoded windows, canonical 8 MiB application groups,
positional I/O, and native memory limits; it never maps an entire source or patch.

## Documentation

- [V4 architecture](docs/ARCHITECTURE-V4.md)
- [VIPR format V4](docs/FORMAT-V4.md)
- [Native V4 implementation](docs/NATIVE-V4.md)
- [Legacy zstd wrapper status](docs/LEGACY-ZSTD.md)
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
