$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$VersionSource = Join-Path $Root "internal/blake3version/version.go"
$VersionLine = Select-String -Path $VersionSource -Pattern '^\s*const Version = "([^"]+)"' | Select-Object -First 1
if (-not $VersionLine) { throw "Could not read the required BLAKE3 version from $VersionSource." }
$Version = $VersionLine.Matches[0].Groups[1].Value
$Archive = "blake3-$Version.crate"
$Url = "https://static.crates.io/crates/blake3/$Archive"
$ExpectedSha256 = "0AA83C34E62843D924F905E0F5C866EB1DD6545FC4D719E803D9BA6030371FCE"
$Destination = Join-Path $Root "third_party/blake3"
$CacheDirectory = Join-Path $Root "build/downloads"
$ArchivePath = Join-Path $CacheDirectory $Archive

$HeaderPath = Join-Path $Destination "c/blake3.h"
if (Test-Path $HeaderPath) {
    $VersionLine = Select-String -Path $HeaderPath -Pattern '^#define BLAKE3_VERSION_STRING "([^"]+)"' | Select-Object -First 1
    if ($VersionLine -and $VersionLine.Matches[0].Groups[1].Value -eq $Version) {
        Write-Host "BLAKE3 $Version is already available."
        exit 0
    }
    Remove-Item -Recurse -Force $Destination
}
New-Item -ItemType Directory -Force -Path $CacheDirectory | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $Root "third_party") | Out-Null
if (-not (Test-Path $ArchivePath)) {
    Invoke-WebRequest -Uri $Url -OutFile $ArchivePath
}
$ActualSha256 = (Get-FileHash -Algorithm SHA256 $ArchivePath).Hash
if ($ActualSha256 -ne $ExpectedSha256) {
    throw "SHA-256 mismatch for $ArchivePath. Expected $ExpectedSha256, got $ActualSha256."
}
$TemporaryDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("viper-patcher-blake3-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $TemporaryDirectory | Out-Null
try {
    tar.exe -xzf $ArchivePath -C $TemporaryDirectory
    if (Test-Path $Destination) { Remove-Item -Recurse -Force $Destination }
    Move-Item (Join-Path $TemporaryDirectory "blake3-$Version") $Destination
}
finally {
    Remove-Item -Recurse -Force $TemporaryDirectory -ErrorAction SilentlyContinue
}
Write-Host "Extracted BLAKE3 $Version to $Destination."
