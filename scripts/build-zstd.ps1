param(
    [ValidateSet("x64", "x86")]
    [string]$Architecture = "x64",

    [string]$MSYS2Root = $env:MSYS2_ROOT
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$Source = Join-Path $Root "third_party/zstd"
$Output = Join-Path $Root "build/zstd"

function Resolve-MSYS2Root {
    param(
        [string]$PreferredRoot
    )

    $Candidates = @(
        $PreferredRoot,
        $env:MSYS2_ROOT,
        "C:\msys64",
        "D:\msys64"
    ) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Select-Object -Unique

    foreach ($Candidate in $Candidates) {
        $ResolvedCandidate = [System.IO.Path]::GetFullPath($Candidate)
        if (Test-Path (Join-Path $ResolvedCandidate "usr/bin/bash.exe")) {
            return $ResolvedCandidate
        }
    }

    throw "MSYS2 was not found. Pass its installation directory with -MSYS2Root or set MSYS2_ROOT."
}

function Invoke-MinGWMake {
    param(
        [string[]]$Arguments
    )

    & $Make @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "MinGW make failed with exit code $LASTEXITCODE."
    }
}

if (-not (Test-Path (Join-Path $Source "lib/zstd.h"))) {
    throw "zstd source is missing. Run scripts/fetch-zstd.ps1 first."
}

$ResolvedMSYS2Root = Resolve-MSYS2Root -PreferredRoot $MSYS2Root
$ToolName = if ($Architecture -eq "x86") { "mingw32" } else { "mingw64" }
$ToolDirectory = Join-Path $ResolvedMSYS2Root "$ToolName/bin"
$MSYSDirectory = Join-Path $ResolvedMSYS2Root "usr/bin"
$Make = Join-Path $ToolDirectory "mingw32-make.exe"
$Compiler = Join-Path $ToolDirectory "gcc.exe"

if (-not (Test-Path $Make)) {
    $Package = if ($Architecture -eq "x86") { "mingw-w64-i686-make" } else { "mingw-w64-x86_64-make" }
    throw "MinGW make was not found at $Make. Install the $Package package."
}
if (-not (Test-Path $Compiler)) {
    $Package = if ($Architecture -eq "x86") { "mingw-w64-i686-gcc" } else { "mingw-w64-x86_64-gcc" }
    throw "MinGW GCC was not found at $Compiler. Install the $Package package."
}

$OldPath = $env:Path
$env:Path = "$ToolDirectory;$MSYSDirectory;$env:Path"
try {
    Invoke-MinGWMake -Arguments @("-C", (Join-Path $Source "lib"), "clean")
    Invoke-MinGWMake -Arguments @(
        "-C", (Join-Path $Source "lib"), "-j2", "libzstd.a",
        "CC=gcc",
        "ZSTD_LEGACY_SUPPORT=0",
        "ZSTD_MULTITHREAD_SUPPORT=0",
        "MOREFLAGS=-O3"
    )
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

# Both static native libraries are mandatory for vipr_static_zstd builds.
& (Join-Path $PSScriptRoot "fetch-blake3.ps1")
& (Join-Path $PSScriptRoot "build-blake3.ps1") -Architecture $Architecture -MSYS2Root $ResolvedMSYS2Root
