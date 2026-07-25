# Building

## Requirements

- Go 1.26.5. Go has a rolling support policy rather than a separate LTS channel.
- A C compiler supported by Go cgo.
- `make`, `curl`, and `tar` on Linux/macOS.
- MSYS2 MinGW toolchains on Windows.
- Linux desktop development headers required by Fyne.

The release build statically links the exact libzstd 1.5.7 source downloaded by
`scripts/fetch-zstd.*`. The resulting executables do not require libzstd to be
installed on the user's system. GUI builds include the `migrated_fynedo` tag;
this marks the completed Fyne single-goroutine migration and disables the legacy
threading compatibility warning.

## Linux

```sh
sudo apt-get install build-essential curl pkg-config libgl1-mesa-dev xorg-dev
go mod download
make build
```

## macOS arm64

Install Xcode command-line tools, then:

```sh
go mod download
make build
```

## Windows x64 or x86

Install Go and MSYS2. Install the required compiler in an MSYS2 terminal:

```sh
pacman -S --needed mingw-w64-x86_64-gcc mingw-w64-x86_64-make
pacman -S --needed mingw-w64-i686-gcc mingw-w64-i686-make
```

Then use PowerShell:

```powershell
./scripts/fetch-zstd.ps1
./scripts/build-zstd.ps1 -Architecture x64
$env:CGO_ENABLED = "1"
$env:GOARCH = "amd64"
$env:CC = "C:\msys64\mingw64\bin\gcc.exe"
go build -tags vipr_static_zstd,migrated_fynedo -o dist/creator.exe ./cmd/creator
go build -tags vipr_static_zstd,migrated_fynedo -o dist/patcher.exe ./cmd/patcher
```

Use `-Architecture x86`, `GOARCH=386`, and the `mingw32` compiler directory for
32-bit Windows.

The PowerShell build script detects a standard `C:\msys64` installation. If
MSYS2 is installed elsewhere, pass its root explicitly:

```powershell
./scripts/build-zstd.ps1 -Architecture x64 -MSYS2Root "D:\path\to\msys64"
```

The same value can be provided through the `MSYS2_ROOT` environment variable.

## Native file dialogs

Windows uses the system PowerShell and .NET dialog APIs, while macOS uses
`osascript`. Linux uses `zenity` when it is installed and automatically falls
back to Fyne's built-in file dialog otherwise. No additional Go module or cgo
dependency is required for these integrations.

## System libzstd development mode

Without the `vipr_static_zstd` build tag, cgo uses `pkg-config libzstd`. Runtime
operations still require the linked version to be exactly 1.5.7. This mode is
convenient for Linux development but is not used for releases.

## Module path

Before publishing, replace `github.com/yourusername/viper-patcher` in `go.mod`
and Go source imports with the final repository path. The neutral GUI
application IDs may remain unchanged. Then run `go mod tidy` and commit the
updated `go.sum`.

## Architecture notes

The same Go and C sources are compiled natively for each architecture. Upstream
libzstd's build system selects architecture-specific compiler optimizations.
Reference files are memory-mapped; 32-bit builds are therefore constrained by
available virtual address space and are unsuitable for very large reference
files even though the streaming target and output paths are bounded-memory.
