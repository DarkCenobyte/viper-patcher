# Building

## Requirements

- Go 1.26.5.
- A C compiler supported by Go cgo.
- `make`, `curl`, and `tar` on Linux/macOS.
- MSYS2 MinGW toolchains on Windows.
- Linux desktop development headers required by Fyne.

The release build statically links the exact libzstd 1.5.7 source downloaded and
SHA-256 verified by `scripts/fetch-zstd.*`. The resulting executables do not
require libzstd to be installed on the user's system. GUI builds include the
`migrated_fynedo` tag, which marks the completed Fyne threading migration.

## Linux

```sh
sudo apt-get install build-essential curl pkg-config libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev
go mod download
make build
```

## macOS arm64

Install Xcode command-line tools, then run:

```sh
go mod download
make build
```

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
```

For x86, use `-Architecture x86`, `GOARCH=386`, and the `mingw32` compiler
directory. The Go host toolchain may remain x64; cgo targets x86 through the
i686 MinGW compiler.

The PowerShell build script detects a standard `C:\msys64` installation. For a
different location, pass the root explicitly or set `MSYS2_ROOT`:

```powershell
./scripts/build-zstd.ps1 -Architecture x64 -MSYS2Root "D:\path\to\msys64"
```

## Creator temporary data

The creator stores immutable source and target snapshots plus generated
partials in a temporary work directory. The GUI estimates peak disk usage before
starting and lets the user select a work-directory parent in the collapsed
Settings section. The CLI exposes the same creator-only behavior through:

```text
--work-directory <directory>
```

This setting does not alter the patch format and has no effect on the patcher.

Creator work uses one logical worker by default. `--workers <count>` sets the
scheduling target shared between independent files and large chunks, up to the
logical CPU count. It is not a strict goroutine ceiling: source verification may
run alongside decoding. Higher values increase CPU, memory, and disk-I/O pressure.

## Native file dialogs

Windows uses PowerShell and .NET dialog APIs, while macOS uses `osascript`.
Linux uses `zenity` when installed and otherwise falls back to Fyne's built-in
file dialog. No additional Go module or cgo dependency is required.

## System libzstd development mode

Without the `vipr_static_zstd` build tag, cgo uses `pkg-config libzstd`. Runtime
operations still require the linked version to be exactly 1.5.7. This mode is
convenient for Linux development but is not used for releases.

## Architecture notes

The same Go and C sources are compiled for each architecture. The native API uses
standalone zstd frames and does not map reference files. Chunked replacement and
sparse application keep work units bounded, but 32-bit builds still have a much
smaller address space and should use conservative worker targets for very large files.
