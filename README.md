# Viper-Patcher

![Viper-Patcher logo](assets/logo.png)

[![CI](https://github.com/DarkCenobyte/viper-patcher/actions/workflows/ci.yml/badge.svg?branch=master&event=push)](https://github.com/DarkCenobyte/viper-patcher/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Platforms](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS-informational)](#platform-targets)
[![Latest release](https://img.shields.io/github/v/release/DarkCenobyte/viper-patcher?sort=semver)](https://github.com/DarkCenobyte/viper-patcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/DarkCenobyte/viper-patcher/total)](https://github.com/DarkCenobyte/viper-patcher/releases)
[![GitHub stars](https://img.shields.io/github/stars/DarkCenobyte/viper-patcher)](https://github.com/DarkCenobyte/viper-patcher/stargazers)
[![Open issues](https://img.shields.io/github/issues/DarkCenobyte/viper-patcher)](https://github.com/DarkCenobyte/viper-patcher/issues)

Viper-Patcher is a cross-platform differential patch creator and applicator for binary files and ordered groups of files. Designed for efficient patch creation and application across a range of workloads, it can package updates for multiple files into a single `.vipr` patch.

The project was developed with extensive assistance from GPT-5.6 Sol, with its implementation validated through automated tests, static analysis, security checks, and reproducible benchmarks.

It provides four executables:

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
  all GUI and CLI-only release archives once after a green `master` commit. A
  manual run may provide a release-version override without editing repository
  metadata.
- `release.yml` accepts pushed `v*` tags or a manual tag/ref request. It reuses a
  matching green CI artifact set for the exact commit, or dispatches an exact-
  commit CI rebuild when the requested version differs or rebuilding is forced.
- Tags such as `v1.0.0-alpha.1` and `v1.0.0-rc.2` are published automatically as
  prereleases; rerunning manually can replace the assets of an existing release.
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

## Benchmark

The following benchmark compares Viper-Patcher `1.0.0` with HDiffPatch 5.1.2, xdelta3 3.2.0, and Floating IPS 198 on Windows x64.

The reference Viper profiles are:

* **CLI-only:** compression level `3`, automatic workers;
* **Hybrid GUI+CLI:** default settings, forced into CLI mode with `--headless`.

The benchmark ran on an Intel Core i7-10700 with 16 logical processors and 31.92 GiB of RAM. Automatic Viper workers resolved to 16. Small, large, and huge cases used 15, 7, and 3 measured iterations respectively, after a warm-up. Patch application and SHA-256 verification were performed outside the measured creation interval, and every measured application was SHA-256 verified after the timer stopped.

Best values for each scenario and metric are shown in **bold**.

<details open>
<summary><strong>Complete reference comparison</strong></summary>

| Scenario                       | Tool             |    Create median |   Apply median |      Patch size |
| ------------------------------ | ---------------- | ---------------: | -------------: | --------------: |
| 1 × 100 KB, scattered changes  | Floating IPS 198 |    **14.820 ms** |  **15.130 ms** |           608 B |
| 1 × 100 KB, scattered changes  | HDiffPatch 5.1.2 |        24.997 ms |      19.714 ms |       **241 B** |
| 1 × 100 KB, scattered changes  | Viper CLI-only   |        66.170 ms |      37.079 ms |         1,006 B |
| 1 × 100 KB, scattered changes  | Viper hybrid     |       154.805 ms |     133.081 ms |         1,006 B |
| 1 × 100 KB, scattered changes  | xdelta3 3.2.0    |        21.953 ms |      20.318 ms |           638 B |
| 1 × 100 KB, unrelated data     | Floating IPS 198 |    **15.539 ms** |  **15.127 ms** |  **97.674 KiB** |
| 1 × 100 KB, unrelated data     | HDiffPatch 5.1.2 |        37.611 ms |      20.164 ms |      97.755 KiB |
| 1 × 100 KB, unrelated data     | Viper CLI-only   |        78.042 ms |      38.276 ms |      98.163 KiB |
| 1 × 100 KB, unrelated data     | Viper hybrid     |       171.475 ms |     136.358 ms |      98.163 KiB |
| 1 × 100 KB, unrelated data     | xdelta3 3.2.0    |        43.225 ms |      20.700 ms |      98.089 KiB |
| 10 × 100 KB, scattered changes | Floating IPS 198 |       163.893 ms |     164.683 ms |       5.938 KiB |
| 10 × 100 KB, scattered changes | HDiffPatch 5.1.2 |    **68.829 ms** |  **29.886 ms** |   **1.109 KiB** |
| 10 × 100 KB, scattered changes | Viper CLI-only   |       104.449 ms |      80.783 ms |       7.636 KiB |
| 10 × 100 KB, scattered changes | Viper hybrid     |       202.615 ms |     185.585 ms |       7.636 KiB |
| 10 × 100 KB, scattered changes | xdelta3 3.2.0    |       245.173 ms |     219.944 ms |       6.230 KiB |
| 10 × 100 KB, unrelated data    | Floating IPS 198 |       150.166 ms |     145.549 ms | **976.737 KiB** |
| 10 × 100 KB, unrelated data    | HDiffPatch 5.1.2 |   **123.446 ms** |  **27.582 ms** |     976.751 KiB |
| 10 × 100 KB, unrelated data    | Viper CLI-only   |       204.518 ms |      71.717 ms |     979.451 KiB |
| 10 × 100 KB, unrelated data    | Viper hybrid     |       320.977 ms |     167.974 ms |     979.451 KiB |
| 10 × 100 KB, unrelated data    | xdelta3 3.2.0    |       351.658 ms |     208.631 ms |     980.880 KiB |
| 1 × 50 MB, scattered changes   | Floating IPS 198 |      Unsupported |    Unsupported |     Unsupported |
| 1 × 50 MB, scattered changes   | HDiffPatch 5.1.2 |     1,005.911 ms |  **51.485 ms** |  **41.719 KiB** |
| 1 × 50 MB, scattered changes   | Viper CLI-only   |       334.731 ms |     104.551 ms |     179.655 KiB |
| 1 × 50 MB, scattered changes   | Viper hybrid     |       458.756 ms |     202.169 ms |     179.655 KiB |
| 1 × 50 MB, scattered changes   | xdelta3 3.2.0    |   **290.209 ms** |     111.275 ms |      49.874 KiB |
| 1 × 50 MB, unrelated data      | Floating IPS 198 |      Unsupported |    Unsupported |     Unsupported |
| 1 × 50 MB, unrelated data      | HDiffPatch 5.1.2 |     5,062.827 ms |  **41.689 ms** |  **47.684 MiB** |
| 1 × 50 MB, unrelated data      | Viper CLI-only   |   **739.091 ms** |      99.848 ms |      47.688 MiB |
| 1 × 50 MB, unrelated data      | Viper hybrid     |       857.037 ms |     175.551 ms |      47.688 MiB |
| 1 × 50 MB, unrelated data      | xdelta3 3.2.0    |    12,560.326 ms |     128.248 ms |      47.687 MiB |
| 1 × 500 MB, scattered changes  | Floating IPS 198 |      Unsupported |    Unsupported |     Unsupported |
| 1 × 500 MB, scattered changes  | HDiffPatch 5.1.2 |    11,108.184 ms | **359.899 ms** | **380.408 KiB** |
| 1 × 500 MB, scattered changes  | Viper CLI-only   | **1,857.603 ms** |   2,300.134 ms |       1.616 MiB |
| 1 × 500 MB, scattered changes  | Viper hybrid     |     1,915.930 ms |   1,549.893 ms |       1.616 MiB |
| 1 × 500 MB, scattered changes  | xdelta3 3.2.0    |     2,688.019 ms |     860.062 ms |     493.271 KiB |
| 1 × 500 MB, unrelated data     | Floating IPS 198 |      Unsupported |    Unsupported |     Unsupported |
| 1 × 500 MB, unrelated data     | HDiffPatch 5.1.2 |    31,777.584 ms | **367.315 ms** | **476.837 MiB** |
| 1 × 500 MB, unrelated data     | Viper CLI-only   |     5,398.076 ms |     390.145 ms |     476.861 MiB |
| 1 × 500 MB, unrelated data     | Viper hybrid     | **5,061.324 ms** |     436.736 ms |     476.861 MiB |
| 1 × 500 MB, unrelated data     | xdelta3 3.2.0    |   253,000.081 ms |   1,539.439 ms |     476.867 MiB |

</details>

*Floating IPS is marked unsupported for the 50 MB and 500 MB cases because the IPS format cannot represent these inputs.*

### Main observations

* **Patch creation is Viper-Patcher's strongest result on large inputs.** The CLI-only build creates the 50 MB unrelated patch in 739 ms and the 500 MB scattered patch in 1.86 s. On the 500 MB unrelated case, the hybrid and CLI-only builds complete in approximately 5.1–5.4 s, compared with 31.8 s for HDiffPatch and 253 s for xdelta3.
* **HDiffPatch remains the strongest reference for application speed and compact patches on large scattered changes.** Viper's 500 MB scattered patch is 1.616 MiB and applies in 1.55–2.30 s with automatic workers, while HDiffPatch produces a 380 KiB patch and applies it in 360 ms.
* **Viper is competitive when most or all data must be replaced.** On the 500 MB unrelated case, Viper CLI applies in 390 ms, close to HDiffPatch at 367 ms and substantially faster than xdelta3 at 1.54 s.
* **The CLI-only binaries avoid most of the hybrid launch overhead.** This is especially visible on 100 KB cases, where process startup represents a significant part of the total time.
* **Automatic parallelism is workload-dependent.** It improves creation throughput and multi-file application, but the complete tuning matrix shows that one worker can apply the 500 MB scattered case faster than the 16-worker automatic configuration.
* **Compression level 3 remains the most balanced default.** Level 9 produces almost no additional size reduction on the scattered datasets, while level 1 creates noticeably larger patches.

### Scope and reproducibility

These results use deterministic synthetic high-entropy inputs. They exercise scattered modifications, unrelated replacements, single-file workloads, and ordered multi-file workloads, but they do not replace measurements on real executables, archives, databases, or game assets.

The complete benchmark tooling, raw repetitions, tuning matrix, executable hashes, hardware metadata, and exact commands are available under [`benchmarks/windows-x64`](benchmarks/windows-x64/).

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
