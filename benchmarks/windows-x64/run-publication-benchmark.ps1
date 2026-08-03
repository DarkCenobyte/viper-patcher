[CmdletBinding()]
param(
    [string]$ViperTag = "v1.0.0-rc.1",
    [int]$SmallIterations = 15,
    [int]$LargeIterations = 7,
    [int]$HugeIterations = 3,
    [switch]$Skip500MB,
    [switch]$NoWarmup,
    [switch]$KeepDatasets,
    [string]$RunLabel = "publication",
    [string]$GitHubToken = $env:GITHUB_TOKEN
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ($PSVersionTable.PSVersion.Major -lt 7) {
    throw "PowerShell 7 or newer is required. Run this script with pwsh.exe."
}

$downloadScript = Join-Path $PSScriptRoot "download-tools.ps1"
$benchmarkScript = Join-Path $PSScriptRoot "run-benchmark.ps1"

Write-Host "Preparing exact release tools for $ViperTag..."
& $downloadScript `
    -ViperSource Release `
    -ViperTag $ViperTag `
    -GitHubToken $GitHubToken `
    -Force

$benchmarkParameters = @{
    SmallIterations = $SmallIterations
    LargeIterations = $LargeIterations
    HugeIterations = $HugeIterations
    RunLabel = $RunLabel
}
if (-not $Skip500MB) { $benchmarkParameters["Include500MB"] = $true }
if ($NoWarmup) { $benchmarkParameters["NoWarmup"] = $true }
if ($KeepDatasets) { $benchmarkParameters["KeepDatasets"] = $true }

Write-Host "Starting the publication benchmark..."
& $benchmarkScript @benchmarkParameters
