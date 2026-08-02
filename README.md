# Viper-Patcher

![Viper-Patcher logo](assets/logo.png)

[![CI](https://github.com/DarkCenobyte/viper-patcher/actions/workflows/ci.yml/badge.svg?branch=master&event=push)](https://github.com/DarkCenobyte/viper-patcher/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Platforms](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS-informational)](#platform-targets)
[![Latest release](https://img.shields.io/github/v/release/DarkCenobyte/viper-patcher?sort=semver)](https://github.com/DarkCenobyte/viper-patcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/DarkCenobyte/viper-patcher/total)](https://github.com/DarkCenobyte/viper-patcher/releases)
[![GitHub stars](https://img.shields.io/github/stars/DarkCenobyte/viper-patcher)](https://github.com/DarkCenobyte/viper-patcher/stargazers)
[![Open issues](https://img.shields.io/github/issues/DarkCenobyte/viper-patcher)](https://github.com/DarkCenobyte/viper-patcher/issues)

Viper-Patcher is a cross-platform differential patch creator and applicator for
binary files and ordered groups of files. It provides four executables:

- `creator`: hybrid GUI/CLI patch creator;
- `patcher`: hybrid GUI/CLI patch applicator;
- `creator-cli`: GUI-free patch creator;
- `patcher-cli`: GUI-free patch applicator.

The hybrid applications start a desktop GUI when launched without arguments and
a graphical environment is available. Any command-line argument selects CLI
mode, and `--headless` explicitly forces it. When no graphical environment is
available, they print a warning and fall back to CLI mode.

The dedicated `*-cli` binaries always use CLI mode and intentionally exclude
Fyne, GLFW, embedded GUI assets, native-dialog code, and the hybrid launcher.
They use the same creator/applicator engines, verification rules, transaction
handling, options, progress reporting, and Ctrl+C/SIGTERM cancellation.

## Key properties

- Exact libzstd **1.5.7** and native BLAKE3 sources, statically linked in release builds.
- VIPR format version 4 with a binary trailing index and independent adaptive windows.
- Per-window SAME, COPY, compact delta, raw/zstd replacement, ZERO, and RUN strategies.
- Content-defined chunking that remains effective across insertions and deletions.
- Ordered multi-file patches with explicit source/target associations.
- BLAKE3-256 source, target, window, group, index, and patch integrity.
- Optional reverse differential for every file.
- Strict, bounds-checked format-4 container; every earlier format is rejected.
- Architecture-aware index and memory limits, including supported 32-bit targets.
- Immutable creator snapshots and handle-based application.
- Adaptive worker allocation across files and canonical 8 MiB output groups.
- Native positional I/O and clone-on-write fast paths where supported.
- Traversal-resistant installation access through `os.Root`.
- Rollback-capable handled-error replacement and interrupted-operation journals.
- No claim of crash-consistent multi-file transactions after power loss or kernel failure.
- File-level, byte-level, and monotonic overall progress reporting.
- MIT-licensed Go code.

## Important path behavior

The source side of each pair defines its path inside the patch. Viper-Patcher
computes the nearest common parent directory of all source files and stores each
source path relative to that directory.

For example:

```text
old/bin/game.exe
old/data/assets.bin
```

produces:

```text
bin/game.exe
data/assets.bin
```

Target files may have different names and live in unrelated directories during
creation. Their locations are not used when applying the patch.

## Creator GUI

The creator interface provides:

- an **Add file pair** action that selects one source and its associated target;
- a permanent two-column source/target table;
- a dedicated comment section;
- compression levels 1 through 22;
- optional reverse-patch generation;
- creator-only work-directory selection;
- automatic or explicit worker targets;
- an output directory and `.vipr` filename;
- a conservative peak temporary-disk estimate;
- file and byte progress.

The selected output file is opened only after validation by the patch creation
core, so cancellation or validation failure does not truncate an existing patch.

Both GUIs display the embedded `assets/logo.png`. The patcher may additionally
use an adjacent regular `assets/logo.png`; invalid files fall back to the
embedded logo. Windows and macOS use native dialogs. Linux uses `zenity` when
available and otherwise falls back to Fyne's dialog.

## Creator CLI

Both `creator --headless` and `creator-cli` accept the same syntax.

```text
creator-cli \
  --file-pair old/bin/game.exe::new/bin/game.exe \
  --file-pair old/data/assets.bin::new/data/assets.bin \
  --compression-level 12 \
  --comment "Version 1.1 update" \
  --create-reverse \
  --workers 2 \
  --memory-limit auto \
  --io-profile auto \
  --window-size auto \
  --optimize balanced \
  --work-directory /path/to/temporary-storage \
  update.vipr
```

Supported parameters:

```text
--file-pair <source>::<target> Repeat once per associated file pair.
--file-pairs-file <pairs.json> Alternative JSON array of {source,target} objects.
                              Use exactly one input form.
[--compression-level <level>]  Default: 3.
[--comment <text>]             Default: Created with Viper-Patcher.
[--create-reverse]             Default: false.
[--work-directory <directory>] Optional creator temporary-data parent.
[--workers <count>]            0 (automatic) or 1..logical CPU count. Default: 0.
[--memory-limit <size>]        auto or at least 128M. Default: auto.
[--io-profile <profile>]       auto|hdd|ssd|nvme. Default: auto.
[--window-size <size>]         auto|256K|512K|1M|2M|4M|8M. Default: auto.
[--optimize <profile>]         balanced|apply-speed|patch-size. Default: balanced.
[--headless]                   Accepted by the hybrid creator; harmless in creator-cli.
[--version]                    Show version information.
[--help]                       Show help.
<output.vipr>                  Required final positional argument.
```

The `::` delimiter is part of the option syntax, so a source and target cannot
become misaligned. The CLI accepts libzstd's full compression-level range,
including negative fast levels and ultra levels. The GUI deliberately presents
the conventional 1–22 range.

## Patcher GUI

The patcher starts by selecting a `.vipr` patch. If exactly one regular `.vipr`
file is present beside the executable, it is selected automatically. Selecting
a target directory triggers a complete preflight:

- every required path must resolve to a regular file without symbolic-link traversal;
- hashes and sizes must match the source state to enable **Patch**;
- reverse data and target-state matches enable **Reverse**;
- identical source/target states may enable both directions;
- mixed, unknown, missing, or non-regular states disable affected directions;
- ordinary installed Unix modes are preserved, while Windows ignores Unix mode semantics.

Selections are locked during an operation. The core rejects a patch whose
BLAKE3 fingerprint changed after GUI preflight and then uses the same
handle-based application path as the CLI.

## Patcher CLI

Both `patcher --headless` and `patcher-cli` accept the same syntax.

Forward application:

```text
patcher-cli --patch-file update.vipr /path/to/application
```

Reverse application:

```text
patcher-cli --patch-file update.vipr --reverse /path/to/application
```

Supported parameters:

```text
--patch-file <file.vipr>      Required.
<target-directory>            Required positional argument.
[--reverse]                   Default: false.
[--workers <count>]           0 (automatic) or 1..logical CPU count. Default: 0.
[--memory-limit <size>]       auto or at least 128M. Default: auto.
[--verify <mode>]             referenced|strict|output. Default: referenced.
[--durability <mode>]         buffered|durable. Default: buffered.
[--io-profile <profile>]      auto|hdd|ssd|nvme. Default: auto.
[--headless]                  Accepted by the hybrid patcher; harmless in patcher-cli.
[--version]                   Show version information.
[--help]                      Show help.
```

`--workers 0` uses the process-aware automatic policy. An explicit worker value
is a scheduling target, not a strict process-wide goroutine limit. The CLI
parses the patch without a redundant whole-file fingerprint pass and does not
run a second complete inspection before application.

## Format 4 implementation

Viper-Patcher does not invoke an external `zstd` executable. VIPR V4 stores
contiguous payloads followed by a binary trailing index and fixed footer, so
application seeks directly to authenticated metadata without scanning payloads.

Each file uses one bounded window size, while every window independently
selects SAME, COPY, compact delta, raw/zstd replacement, ZERO, or RUN. Go owns
policy, path safety, scheduling, temporary files, and rollback-capable commit.
Coarse cgo calls delegate BLAKE3, zstd, positional I/O, instruction decoding,
output writes, and window/group verification to the native C data plane.

Application distributes work across files and canonical 8 MiB output groups.
When an equal-size output contains at least 90% SAME windows, Viper-Patcher
attempts a copy-on-write clone and reconstructs only changed windows. Unsupported
filesystems transparently fall back to reconstruction. `referenced`, `strict`,
and `output` verification profiles control source validation without weakening
index, window, group, or output integrity checks.

Only format 4 patches are accepted. Earlier `.vipr` files remain usable through
their tagged historical releases. See [VIPR format V4](docs/FORMAT-V4.md),
[V4 architecture](docs/ARCHITECTURE-V4.md), and
[native V4 implementation](docs/NATIVE-V4.md).

## Building

The repository pins Go 1.26.5 and verified native dependencies.

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
go build -tags vipr_static_zstd -o dist/creator-cli.exe ./cmd/creator-cli
go build -tags vipr_static_zstd -o dist/patcher-cli.exe ./cmd/patcher-cli
```

See [Building](docs/BUILDING.md) for dependencies, x86 instructions, and
creator temporary-data options.

## Testing

```sh
make check
```

The repository includes unit, integration, race, fuzz, native sanitizer,
Staticcheck, vulnerability-reachability, path-safety, transaction, GUI-model,
and cross-architecture tests. CI enforces at least 80% statement coverage across
the non-GUI core, explicitly including command contexts, the native V4 wrapper,
and worker budgeting. See [Testing strategy](docs/TESTING.md).

## GitHub Actions

- `ci.yml` runs the correctness gate on pull requests and `master`, then builds
  all GUI and CLI-only release archives once after a green `master` commit.
- `release.yml` runs only for a `v*` tag, requires a successful CI push run for
  the exact tagged SHA, downloads those artifacts, and publishes one release.
- Tag publication never rebuilds binaries.
- External actions are referenced by immutable commit identifiers.

See [GitHub Actions CI/CD](docs/CI-CD.md) and
[Supply-chain controls](docs/SUPPLY-CHAIN.md).

## Platform targets

Every target receives a hybrid GUI archive and a separate CLI-only archive
containing both `creator-cli` and `patcher-cli`.

| Operating system | Architecture | Release archives |
|---|---|---|
| Windows | x86-64 | GUI + CLI-only |
| Windows | x86 | GUI + CLI-only |
| Linux | x86-64 | GUI + CLI-only |
| Linux | x86 | GUI + CLI-only |
| Linux | arm64 | GUI + CLI-only |
| macOS | arm64 | GUI + CLI-only |

## Documentation

- [V4 architecture](docs/ARCHITECTURE-V4.md)
- [VIPR format V4](docs/FORMAT-V4.md)
- [Native V4 implementation](docs/NATIVE-V4.md)
- [Operational guarantees](docs/OPERATIONS.md)
- [Building](docs/BUILDING.md)
- [Testing strategy](docs/TESTING.md)
- [GitHub Actions CI/CD](docs/CI-CD.md)
- [Supply-chain controls](docs/SUPPLY-CHAIN.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

## Licenses

Viper-Patcher is licensed under the [MIT License](LICENSE).

libzstd 1.5.7 is available upstream under the BSD 3-Clause license or GPLv2;
this project uses the BSD option. BLAKE3, Fyne, and all transitive Go modules
retain their own licenses. Release archives include zstd and BLAKE3 notices plus
collected top-level license and notice files for every resolved Go module.
