$ErrorActionPreference = "Stop"

$Version = "1.5.7"
$Archive = "zstd-$Version.tar.gz"
$Url = "https://github.com/facebook/zstd/releases/download/v$Version/$Archive"
$ExpectedSha256 = "EB33E51F49A15E023950CD7825CA74A4A2B43DB8354825AC24FC1B7EE09E6FA3"
$Root = Split-Path -Parent $PSScriptRoot
$Destination = Join-Path $Root "third_party/zstd"
$CacheDirectory = Join-Path $Root "build/downloads"
$ArchivePath = Join-Path $CacheDirectory $Archive

if (Test-Path (Join-Path $Destination "lib/zstd.h")) {
    $HeaderPath = Join-Path $Destination "lib/zstd.h"
    $MajorLine = Select-String -Path $HeaderPath -Pattern '^#define ZSTD_VERSION_MAJOR\s+([0-9]+)' | Select-Object -First 1
    $MinorLine = Select-String -Path $HeaderPath -Pattern '^#define ZSTD_VERSION_MINOR\s+([0-9]+)' | Select-Object -First 1
    $ReleaseLine = Select-String -Path $HeaderPath -Pattern '^#define ZSTD_VERSION_RELEASE\s+([0-9]+)' | Select-Object -First 1
    $ActualVersion = if ($MajorLine -and $MinorLine -and $ReleaseLine) {
        "$($MajorLine.Matches[0].Groups[1].Value).$($MinorLine.Matches[0].Groups[1].Value).$($ReleaseLine.Matches[0].Groups[1].Value)"
    } else {
        "unknown"
    }
    if ($ActualVersion -eq $Version) {
        Write-Host "zstd $Version is already available."
        exit 0
    }
    Write-Host "Removing unexpected zstd source version $ActualVersion."
    Remove-Item -Recurse -Force $Destination
}

New-Item -ItemType Directory -Force -Path $CacheDirectory | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $Root "third_party") | Out-Null
if (-not (Test-Path $ArchivePath)) {
    Write-Host "Downloading $Url..."
    Invoke-WebRequest -Uri $Url -OutFile $ArchivePath
}

$ActualSha256 = (Get-FileHash -Algorithm SHA256 $ArchivePath).Hash
if ($ActualSha256 -ne $ExpectedSha256) {
    throw "SHA-256 mismatch for $ArchivePath. Expected $ExpectedSha256, got $ActualSha256."
}

$TemporaryDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("viper-patcher-zstd-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $TemporaryDirectory | Out-Null
try {
    tar.exe -xzf $ArchivePath -C $TemporaryDirectory
    if (Test-Path $Destination) {
        Remove-Item -Recurse -Force $Destination
    }
    Move-Item (Join-Path $TemporaryDirectory "zstd-$Version") $Destination
}
finally {
    Remove-Item -Recurse -Force $TemporaryDirectory -ErrorAction SilentlyContinue
}
Write-Host "Extracted zstd $Version to $Destination."
