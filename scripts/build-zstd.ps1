param(
    [ValidateSet("x64", "x86")]
    [string]$Architecture = "x64"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$Source = Join-Path $Root "third_party/zstd"
$Output = Join-Path $Root "build/zstd"

if (-not (Test-Path (Join-Path $Source "lib/zstd.h"))) {
    throw "zstd source is missing. Run scripts/fetch-zstd.ps1 first."
}

if ($Architecture -eq "x86") {
    $ToolDirectory = "C:\msys64\mingw32\bin"
}
else {
    $ToolDirectory = "C:\msys64\mingw64\bin"
}
$Make = Join-Path $ToolDirectory "mingw32-make.exe"
if (-not (Test-Path $Make)) {
    throw "MinGW make was not found at $Make. Install MSYS2 and the matching MinGW toolchain."
}

$OldPath = $env:Path
$env:Path = "$ToolDirectory;$env:Path"
try {
    & $Make -C (Join-Path $Source "lib") clean | Out-Null
    & $Make -C (Join-Path $Source "lib") -j2 libzstd.a ZSTD_LEGACY_SUPPORT=0 ZSTD_MULTITHREAD_SUPPORT=0 MOREFLAGS=-O3
    if ($LASTEXITCODE -ne 0) {
        throw "libzstd build failed with exit code $LASTEXITCODE."
    }
}
finally {
    $env:Path = $OldPath
}

if (Test-Path $Output) {
    Remove-Item -Recurse -Force $Output
}
New-Item -ItemType Directory -Force -Path (Join-Path $Output "include") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $Output "lib") | Out-Null
Copy-Item (Join-Path $Source "lib/zstd.h") (Join-Path $Output "include")
Copy-Item (Join-Path $Source "lib/zstd_errors.h") (Join-Path $Output "include")
Copy-Item (Join-Path $Source "lib/zdict.h") (Join-Path $Output "include")
Copy-Item (Join-Path $Source "lib/libzstd.a") (Join-Path $Output "lib/libzstd.a")
Write-Host "Built static libzstd at $(Join-Path $Output 'lib/libzstd.a')."
