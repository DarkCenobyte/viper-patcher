param(
    [ValidateSet("x64", "x86")]
    [string]$Architecture = "x64",
    [string]$MSYS2Root = $env:MSYS2_ROOT
)
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$Source = Join-Path $Root "third_party/blake3/c"
$Output = Join-Path $Root "build/blake3"
$Objects = Join-Path $Output "obj"

function Resolve-MSYS2Root([string]$PreferredRoot) {
    $Candidates = @($PreferredRoot, $env:MSYS2_ROOT, "C:\msys64", "D:\msys64") |
        Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Select-Object -Unique
    foreach ($Candidate in $Candidates) {
        $Resolved = [System.IO.Path]::GetFullPath($Candidate)
        if (Test-Path (Join-Path $Resolved "usr/bin/bash.exe")) { return $Resolved }
    }
    throw "MSYS2 was not found."
}
function Invoke-Tool([string]$Tool, [string[]]$Arguments) {
    & $Tool @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Tool failed with exit code $LASTEXITCODE." }
}
if (-not (Test-Path (Join-Path $Source "blake3.h"))) {
    throw "BLAKE3 source is missing. Run scripts/fetch-blake3.ps1 first."
}
$ResolvedMSYS2Root = Resolve-MSYS2Root $MSYS2Root
$ToolName = if ($Architecture -eq "x86") { "mingw32" } else { "mingw64" }
$ToolDirectory = Join-Path $ResolvedMSYS2Root "$ToolName/bin"
$Compiler = Join-Path $ToolDirectory "gcc.exe"
$Archiver = Join-Path $ToolDirectory "ar.exe"
$Ranlib = Join-Path $ToolDirectory "ranlib.exe"
foreach ($Tool in @($Compiler, $Archiver)) {
    if (-not (Test-Path $Tool)) { throw "Required MinGW tool was not found: $Tool" }
}
if (Test-Path $Output) { Remove-Item -Recurse -Force $Output }
New-Item -ItemType Directory -Force -Path $Objects | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $Output "include") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $Output "lib") | Out-Null
$Common = @("-O3", "-std=c11", "-DIS_X86", "-I$Source")
$Specs = @(
    @{ Source = "blake3.c"; Flags = @() },
    @{ Source = "blake3_dispatch.c"; Flags = @() },
    @{ Source = "blake3_portable.c"; Flags = @() }
)
if ($Architecture -eq "x64") {
    $Specs += @(
        @{ Source = "blake3_sse2_x86-64_windows_gnu.S"; Flags = @() },
        @{ Source = "blake3_sse41_x86-64_windows_gnu.S"; Flags = @() },
        @{ Source = "blake3_avx2_x86-64_windows_gnu.S"; Flags = @() },
        @{ Source = "blake3_avx512_x86-64_windows_gnu.S"; Flags = @("-mavx512f", "-mavx512vl") }
    )
}
else {
    $Specs += @(
        @{ Source = "blake3_sse2.c"; Flags = @("-fno-lto", "-msse2") },
        @{ Source = "blake3_sse41.c"; Flags = @("-fno-lto", "-msse4.1") },
        @{ Source = "blake3_avx2.c"; Flags = @("-fno-lto", "-mavx2") },
        @{ Source = "blake3_avx512.c"; Flags = @("-fno-lto", "-mavx512f", "-mavx512vl", "-fno-asynchronous-unwind-tables") }
    )
}
$ObjectPaths = @()
foreach ($Spec in $Specs) {
    $Object = Join-Path $Objects ([System.IO.Path]::GetFileNameWithoutExtension($Spec.Source) + ".o")
    Invoke-Tool $Compiler ($Common + $Spec.Flags + @("-c", (Join-Path $Source $Spec.Source), "-o", $Object))
    $ObjectPaths += $Object
}
$Library = Join-Path $Output "lib/libblake3.a"
Invoke-Tool $Archiver (@("rcs", $Library) + $ObjectPaths)
if (Test-Path $Ranlib) { Invoke-Tool $Ranlib @($Library) }
Copy-Item (Join-Path $Source "blake3.h") (Join-Path $Output "include")
Write-Host "Built official SIMD-dispatched BLAKE3 at $Library."
