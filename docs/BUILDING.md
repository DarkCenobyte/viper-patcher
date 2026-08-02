# Building

## Requirements

- Go 1.26.5.
- A C compiler supported by Go cgo.
- `make`, `curl`, and `tar` on Linux/macOS.
- MSYS2 MinGW toolchains on Windows.
- Linux desktop development headers required by Fyne for the hybrid GUI builds.

The repository builds four executables:

- `creator` and `patcher`: hybrid applications that start the GUI without
  arguments and use CLI mode when arguments are supplied;
- `creator-cli` and `patcher-cli`: GUI-free command-line applications that do
  not link Fyne, GLFW, GUI assets, or `internal/appmode`.

Release builds statically link the exact libzstd 1.5.7 and BLAKE3 sources
verified by `scripts/fetch-zstd.*` and `scripts/fetch-blake3.*`. The resulting
executables do not require libzstd to be installed on the user's system. GUI
builds include the `migrated_fynedo` tag, which marks the completed Fyne
threading migration. CLI-only builds intentionally use only
`vipr_static_zstd`.

## Linux

```sh
sudo apt-get install build-essential curl pkg-config libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev
go mod download
make build
```

`make build` produces all four executables in `dist/`.

## macOS arm64

Install Xcode command-line tools, then run:

```sh
go mod download
make build
```

The release workflow additionally packages the two hybrid executables as macOS
application bundles. The CLI-only archive contains ordinary `creator-cli` and
`patcher-cli` Mach-O executables.

## Windows x64 or x86

Install Go and MSYS2. Install the required compiler and make packages in an
MSYS2 terminal:

```sh
pacman -S --needed mingw-w64-x86_64-gcc mingw-w64-x86_64-make
pacman -S --needed mingw-w64-i686-gcc mingw-w64-i686-make
```

Then use PowerShell for x64:

```powershell
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

For x86, use `-Architecture x86`, `GOARCH=386`, and the `mingw32` compiler
directory. The Go host toolchain may remain x64; cgo targets x86 through the
i686 MinGW compiler.

The PowerShell build script detects a standard `C:\msys64` installation. For a
different location, pass the root explicitly or set `MSYS2_ROOT`:

```powershell
./scripts/build-zstd.ps1 -Architecture x64 -MSYS2Root "D:\path\to\msys64"
```

## CLI-only dependency boundary

The CLI-only commands import `internal/cli`, `internal/commandctx`, and the
shared patch engine directly. They must not import Fyne, GLFW, `assets`,
`internal/gui`, or `internal/appmode`. CI checks the complete dependency graph
and inspects produced binaries for GUI dynamic libraries on every release
platform.

## Creator temporary data

The creator stores immutable source and target snapshots plus generated
partials in a temporary work directory. The GUI estimates peak disk usage
before starting and lets the user select a work-directory parent in the
collapsed Settings section. Both `creator --headless` and `creator-cli` expose
the same creator-only behavior through:

```text
--work-directory <directory>
```

This setting does not alter the patch format and has no effect on the patcher.

Creator work uses the automatic process-aware worker target by default.
`--workers <count>` accepts `0` for automatic selection or an explicit value up
to the logical CPU count. It is not a strict goroutine ceiling: source
verification may run alongside decoding. Higher values increase CPU, memory,
and disk-I/O pressure.

## Native file dialogs

Windows uses PowerShell and .NET dialog APIs, while macOS uses `osascript`.
Linux uses `zenity` when installed and otherwise falls back to Fyne's built-in
file dialog. CLI-only executables contain none of this dialog code.

## System libzstd development mode

Without the `vipr_static_zstd` build tag, cgo uses `pkg-config libzstd`. Runtime
operations still require the linked version to be exactly 1.5.7. This mode is
convenient for Linux development but is not used for releases.

## Architecture notes

The same Go and C sources are compiled for each architecture. The native API
uses standalone zstd frames and does not map reference files. Bounded windows,
canonical 8 MiB application groups, positional I/O, and architecture-aware
runtime limits avoid mapping an entire source or patch. A 32-bit process still
has a much smaller virtual address space and should use conservative worker and
memory limits for very large files.
