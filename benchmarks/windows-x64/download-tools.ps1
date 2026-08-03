[CmdletBinding()]
param(
    [string]$Destination = (Join-Path $PSScriptRoot "tools"),
    [ValidateSet("Release", "WorkflowArtifact", "LocalArtifact")]
    [string]$ViperSource = "Release",
    [string]$ViperTag = "v1.0.0-rc.1",
    [string]$ViperRef = "master",
    [string]$ViperBuildVersion = "",
    [string]$ViperArtifactPath = "",
    [string]$GitHubToken = $env:GITHUB_TOKEN,
    [switch]$Force
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$DownloaderVersion = "1.2.3"
$ViperRepository = "DarkCenobyte/viper-patcher"
$ViperWorkflow = "ci.yml"
Write-Host "Viper-Patcher Windows x64 benchmark downloader v$DownloaderVersion"

if ($PSVersionTable.PSVersion.Major -lt 7) {
    throw "PowerShell 7 or newer is required. Run this script with pwsh.exe."
}
if ($ViperSource -eq "LocalArtifact" -and [string]::IsNullOrWhiteSpace($ViperArtifactPath)) {
    throw "-ViperArtifactPath is required when -ViperSource LocalArtifact is selected."
}
if (-not [string]::IsNullOrWhiteSpace($ViperBuildVersion) -and
    $ViperBuildVersion -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$') {
    throw "Invalid -ViperBuildVersion '$ViperBuildVersion'. Expected MAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH-suffix."
}

$headers = @{
    "User-Agent" = "viper-patcher-windows-x64-benchmark"
    "Accept" = "application/vnd.github+json"
    "X-GitHub-Api-Version" = "2022-11-28"
}
if (-not [string]::IsNullOrWhiteSpace($GitHubToken)) {
    $headers["Authorization"] = "Bearer $GitHubToken"
}

$destinationPath = [IO.Path]::GetFullPath($Destination)
$downloadDir = Join-Path $destinationPath "downloads"
$stageDir = Join-Path $destinationPath "staging"
$binDir = Join-Path $destinationPath "bin"

if ($Force -and (Test-Path -LiteralPath $destinationPath)) {
    Remove-Item -LiteralPath $destinationPath -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $downloadDir, $stageDir, $binDir | Out-Null

function Invoke-GitHubJson {
    param([Parameter(Mandatory)] [string] $Uri)
    return Invoke-RestMethod -Uri $Uri -Headers $headers -Method Get
}

function Get-GitHubFileText {
    param(
        [Parameter(Mandatory)] [string] $Repository,
        [Parameter(Mandatory)] [string] $Path,
        [Parameter(Mandatory)] [string] $Ref
    )
    $encodedPath = ($Path -split "/" | ForEach-Object { [Uri]::EscapeDataString($_) }) -join "/"
    $encodedRef = [Uri]::EscapeDataString($Ref)
    $file = Invoke-GitHubJson "https://api.github.com/repos/$Repository/contents/$encodedPath?ref=$encodedRef"
    if ($file.encoding -ne "base64") {
        throw "Unexpected GitHub content encoding for $Path at ${Ref}: $($file.encoding)"
    }
    $bytes = [Convert]::FromBase64String(($file.content -replace "\s", ""))
    return [Text.Encoding]::UTF8.GetString($bytes)
}

function Select-ReleaseAsset {
    param(
        [Parameter(Mandatory)] $Release,
        [Parameter(Mandatory)] [string[]] $Patterns,
        [Parameter(Mandatory)] [string] $ToolName
    )

    foreach ($pattern in $Patterns) {
        $matches = @($Release.assets | Where-Object { $_.name -match $pattern })
        if ($matches.Count -eq 1) {
            return $matches[0]
        }
        if ($matches.Count -gt 1) {
            return @($matches | Sort-Object `
                @{ Expression = { if ($_.name -match '(?i)(x64|amd64|x86_64|win64)') { 0 } else { 1 } } }, `
                @{ Expression = { $_.name.Length } }, name)[0]
        }
    }

    $available = ($Release.assets | ForEach-Object { "  - $($_.name)" }) -join [Environment]::NewLine
    throw "No suitable Windows asset found for $ToolName. Available assets:`n$available"
}

function Expand-ToolAsset {
    param(
        [Parameter(Mandatory)] [string] $AssetPath,
        [Parameter(Mandatory)] [string] $ToolStage
    )

    if (Test-Path -LiteralPath $ToolStage) {
        Remove-Item -LiteralPath $ToolStage -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $ToolStage | Out-Null

    $extension = [IO.Path]::GetExtension($AssetPath).ToLowerInvariant()
    switch ($extension) {
        ".zip" { Expand-Archive -LiteralPath $AssetPath -DestinationPath $ToolStage -Force }
        ".exe" { Copy-Item -LiteralPath $AssetPath -Destination (Join-Path $ToolStage ([IO.Path]::GetFileName($AssetPath))) }
        default { throw "Unsupported release asset type: $AssetPath" }
    }
}

function Get-PEArchitecture {
    param([Parameter(Mandatory)] [string] $Path)

    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        $reader = [IO.BinaryReader]::new($stream)
        try {
            if ($reader.ReadUInt16() -ne 0x5A4D) { return "NotPE" }
            $stream.Position = 0x3C
            $peOffset = $reader.ReadInt32()
            if ($peOffset -lt 0 -or $peOffset -gt ($stream.Length - 6)) { return "InvalidPE" }
            $stream.Position = $peOffset
            if ($reader.ReadUInt32() -ne 0x00004550) { return "InvalidPE" }
            $machine = $reader.ReadUInt16()
            switch ($machine) {
                0x8664 { return "AMD64" }
                0x014C { return "I386" }
                0xAA64 { return "ARM64" }
                default { return ("PE-0x{0:X4}" -f $machine) }
            }
        } finally {
            $reader.Dispose()
        }
    } finally {
        $stream.Dispose()
    }
}

function Find-PreferredExecutable {
    param(
        [Parameter(Mandatory)] [string] $SearchRoot,
        [Parameter(Mandatory)] [string[]] $ExactNames,
        [Parameter(Mandatory)] [string] $FallbackPattern,
        [string] $RequiredArchitecture = ""
    )

    $files = @(Get-ChildItem -LiteralPath $SearchRoot -Recurse -File)
    $candidates = [Collections.Generic.List[IO.FileInfo]]::new()
    foreach ($name in $ExactNames) {
        foreach ($file in @($files | Where-Object { $_.Name -ieq $name } | Sort-Object FullName)) {
            if (-not $candidates.Contains($file)) { $candidates.Add($file) }
        }
    }
    foreach ($file in @($files | Where-Object { $_.Name -match $FallbackPattern } | Sort-Object FullName)) {
        if (-not $candidates.Contains($file)) { $candidates.Add($file) }
    }
    if ($candidates.Count -eq 0) {
        throw "Executable not found under $SearchRoot. Expected: $($ExactNames -join ', ')"
    }

    $ranked = @($candidates | ForEach-Object {
        $architecture = Get-PEArchitecture $_.FullName
        [pscustomobject]@{
            File = $_
            Architecture = $architecture
            Rank = if ($architecture -eq "AMD64") { 0 } elseif ($architecture -eq "I386") { 1 } else { 2 }
        }
    } | Sort-Object Rank, @{ Expression = { $_.File.FullName } })

    if (-not [string]::IsNullOrWhiteSpace($RequiredArchitecture)) {
        $required = @($ranked | Where-Object { $_.Architecture -eq $RequiredArchitecture })
        if ($required.Count -eq 0) {
            $available = ($ranked | ForEach-Object { "  - $($_.File.FullName) [$($_.Architecture)]" }) -join [Environment]::NewLine
            throw "No $RequiredArchitecture executable found under $SearchRoot.`n$available"
        }
        return $required[0]
    }

    return $ranked[0]
}

function Install-NormalizedExecutable {
    param(
        [Parameter(Mandatory)] $Candidate,
        [Parameter(Mandatory)] [string] $DestinationName
    )
    $target = Join-Path $binDir $DestinationName
    Copy-Item -LiteralPath $Candidate.File.FullName -Destination $target -Force
    return [pscustomobject]@{
        File = Get-Item -LiteralPath $target
        Source = $Candidate.File.FullName
        Architecture = $Candidate.Architecture
    }
}

function Download-ReleaseAsset {
    param(
        [Parameter(Mandatory)] $Asset,
        [Parameter(Mandatory)] [string] $OutputPath
    )
    Invoke-WebRequest -Uri $Asset.browser_download_url -Headers $headers -OutFile $OutputPath
}

function Get-ViperArchiveVersion {
    param([Parameter(Mandatory)] [string] $SearchRoot)

    $guiVersions = @(Get-ChildItem -LiteralPath $SearchRoot -Recurse -File | ForEach-Object {
        if ($_.Name -match '^viper-patcher_(.+)_windows_amd64\.zip$') { $Matches[1] }
    } | Sort-Object -Unique)
    $cliVersions = @(Get-ChildItem -LiteralPath $SearchRoot -Recurse -File | ForEach-Object {
        if ($_.Name -match '^viper-patcher-cli_(.+)_windows_amd64\.zip$') { $Matches[1] }
    } | Sort-Object -Unique)

    $common = @($guiVersions | Where-Object { $_ -in $cliVersions })
    if ($common.Count -ne 1) {
        $available = (Get-ChildItem -LiteralPath $SearchRoot -Recurse -File | ForEach-Object { "  - $($_.Name)" }) -join [Environment]::NewLine
        throw "Could not infer one common Viper Windows amd64 version from the hybrid and CLI-only archives.`n$available"
    }
    return [string]$common[0]
}

function Confirm-ReleaseChecksums {
    param(
        [Parameter(Mandatory)] [string] $ManifestPath,
        [Parameter(Mandatory)] [IO.FileInfo[]] $Files
    )

    $expected = @{}
    foreach ($line in Get-Content -LiteralPath $ManifestPath) {
        if ($line -match '^([0-9a-fA-F]{64})\s+\*?(.+)$') {
            $name = [IO.Path]::GetFileName($Matches[2].Trim())
            $expected[$name] = $Matches[1].ToLowerInvariant()
        }
    }
    foreach ($file in $Files) {
        if (-not $expected.ContainsKey($file.Name)) {
            throw "SHA256SUMS.txt does not contain '$($file.Name)'."
        }
        $actual = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $expected[$file.Name]) {
            throw "SHA-256 mismatch for '$($file.Name)': expected $($expected[$file.Name]), got $actual."
        }
    }
}

function Resolve-ViperWorkflowArtifact {
    param([Parameter(Mandatory)] [string] $OutputStage)

    $encodedRef = [Uri]::EscapeDataString($ViperRef)
    $commit = Invoke-GitHubJson "https://api.github.com/repos/$ViperRepository/commits/$encodedRef"
    $commitSha = [string]$commit.sha
    $repositoryVersion = (Get-GitHubFileText -Repository $ViperRepository -Path "VERSION" -Ref $commitSha).Trim()
    $version = if ([string]::IsNullOrWhiteSpace($ViperBuildVersion)) { $repositoryVersion } else { $ViperBuildVersion }
    if ($version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$') {
        throw "Invalid Viper build version '$version'."
    }
    $artifactName = "release-$version-windows-amd64"

    $runsUri = "https://api.github.com/repos/$ViperRepository/actions/workflows/$ViperWorkflow/runs?head_sha=$commitSha&status=success&per_page=100"
    $runs = Invoke-GitHubJson $runsUri
    $run = $null
    $artifact = $null
    foreach ($candidateRun in @($runs.workflow_runs | Where-Object {
        $_.head_sha -eq $commitSha -and $_.status -eq "completed" -and $_.conclusion -eq "success"
    } | Sort-Object run_number -Descending)) {
        $artifactListing = Invoke-GitHubJson "https://api.github.com/repos/$ViperRepository/actions/runs/$($candidateRun.id)/artifacts?per_page=100"
        $matches = @($artifactListing.artifacts | Where-Object { $_.name -eq $artifactName -and -not $_.expired })
        if ($matches.Count -eq 1) {
            $run = $candidateRun
            $artifact = $matches[0]
            break
        }
    }
    if ($null -eq $run -or $null -eq $artifact) {
        throw "No successful $ViperWorkflow run for exact commit $commitSha contains the non-expired artifact '$artifactName'. Use -ViperSource Release for a published tag, or provide a downloaded artifact with -ViperSource LocalArtifact."
    }

    New-Item -ItemType Directory -Force -Path $OutputStage | Out-Null
    $downloaded = $false
    $gh = Get-Command gh -ErrorAction SilentlyContinue
    if ($null -ne $gh) {
        & $gh.Source auth status --hostname github.com *> $null
        if ($LASTEXITCODE -eq 0) {
            & $gh.Source run download ([string]$run.id) --repo $ViperRepository --name $artifactName --dir $OutputStage
            if ($LASTEXITCODE -ne 0) { throw "gh failed to download Actions artifact $artifactName from run $($run.id)." }
            $downloaded = $true
        }
    }

    if (-not $downloaded) {
        if ([string]::IsNullOrWhiteSpace($GitHubToken)) {
            throw "Downloading GitHub Actions artifacts requires authentication. Authenticate GitHub CLI, set GITHUB_TOKEN, or manually download '$artifactName' from CI run $($run.id) and use -ViperSource LocalArtifact."
        }
        $artifactZip = Join-Path $downloadDir "viper-ci-$commitSha-$version.zip"
        Invoke-WebRequest -Uri $artifact.archive_download_url -Headers $headers -OutFile $artifactZip -MaximumRedirection 10
        Expand-Archive -LiteralPath $artifactZip -DestinationPath $OutputStage -Force
    }

    return [pscustomobject]@{
        Version = $version
        RepositoryVersion = $repositoryVersion
        Commit = $commitSha
        RunId = [long]$run.id
        RunNumber = [long]$run.run_number
        ArtifactId = [long]$artifact.id
        ArtifactName = [string]$artifact.name
        ArtifactCreatedAt = [string]$artifact.created_at
        Source = "workflow_artifact"
        ReleaseId = $null
        ReleasePrerelease = $null
        ChecksumsVerified = $false
    }
}

function Resolve-ViperLocalArtifact {
    param([Parameter(Mandatory)] [string] $OutputStage)

    $artifactPath = [IO.Path]::GetFullPath($ViperArtifactPath)
    if (-not (Test-Path -LiteralPath $artifactPath -PathType Leaf)) {
        throw "Viper artifact ZIP not found: $artifactPath"
    }
    New-Item -ItemType Directory -Force -Path $OutputStage | Out-Null
    Expand-Archive -LiteralPath $artifactPath -DestinationPath $OutputStage -Force

    $inferredVersion = Get-ViperArchiveVersion -SearchRoot $OutputStage
    $version = if ([string]::IsNullOrWhiteSpace($ViperBuildVersion)) { $inferredVersion } else { $ViperBuildVersion }
    if ($version -ne $inferredVersion) {
        throw "Local artifact contains version '$inferredVersion', not requested version '$version'."
    }
    return [pscustomobject]@{
        Version = $version
        RepositoryVersion = $null
        Commit = $null
        RunId = $null
        RunNumber = $null
        ArtifactId = $null
        ArtifactName = [IO.Path]::GetFileName($artifactPath)
        ArtifactCreatedAt = (Get-Item -LiteralPath $artifactPath).LastWriteTimeUtc.ToString("o")
        Source = "local_artifact"
        LocalArtifactSha256 = (Get-FileHash -LiteralPath $artifactPath -Algorithm SHA256).Hash.ToLowerInvariant()
        ReleaseId = $null
        ReleasePrerelease = $null
        ChecksumsVerified = $false
    }
}

function Resolve-ViperRelease {
    param([Parameter(Mandatory)] [string] $OutputStage)

    $release = Invoke-GitHubJson "https://api.github.com/repos/$ViperRepository/releases/tags/$ViperTag"
    $tagCommit = Invoke-GitHubJson "https://api.github.com/repos/$ViperRepository/commits/$([Uri]::EscapeDataString($ViperTag))"
    $version = $ViperTag -replace '^v', ''
    if ($version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$') {
        throw "Invalid Viper release tag '$ViperTag'."
    }
    if (-not [string]::IsNullOrWhiteSpace($ViperBuildVersion) -and $ViperBuildVersion -ne $version) {
        throw "-ViperBuildVersion '$ViperBuildVersion' does not match release tag version '$version'."
    }

    $expected = @(
        "viper-patcher_${version}_windows_amd64.zip",
        "viper-patcher-cli_${version}_windows_amd64.zip"
    )
    New-Item -ItemType Directory -Force -Path $OutputStage | Out-Null
    $downloadedArchives = [Collections.Generic.List[IO.FileInfo]]::new()
    foreach ($name in $expected) {
        $matches = @($release.assets | Where-Object { $_.name -eq $name })
        if ($matches.Count -ne 1) {
            $available = ($release.assets | ForEach-Object { "  - $($_.name)" }) -join [Environment]::NewLine
            throw "Release $ViperTag does not contain exactly one '$name' asset.`n$available"
        }
        $outputPath = Join-Path $OutputStage $name
        Download-ReleaseAsset -Asset $matches[0] -OutputPath $outputPath
        $downloadedArchives.Add((Get-Item -LiteralPath $outputPath))
    }

    $checksumsVerified = $false
    $checksumAssets = @($release.assets | Where-Object { $_.name -eq "SHA256SUMS.txt" })
    if ($checksumAssets.Count -eq 1) {
        $manifestPath = Join-Path $OutputStage "SHA256SUMS.txt"
        Download-ReleaseAsset -Asset $checksumAssets[0] -OutputPath $manifestPath
        Confirm-ReleaseChecksums -ManifestPath $manifestPath -Files @($downloadedArchives)
        $checksumsVerified = $true
    } else {
        Write-Warning "Release $ViperTag has no unique SHA256SUMS.txt asset; local archive hashes will still be recorded."
    }

    return [pscustomobject]@{
        Version = $version
        RepositoryVersion = $null
        Commit = [string]$tagCommit.sha
        RunId = $null
        RunNumber = $null
        ArtifactId = $null
        ArtifactName = $ViperTag
        ArtifactCreatedAt = [string]$release.published_at
        Source = "release"
        ReleaseId = [long]$release.id
        ReleasePrerelease = [bool]$release.prerelease
        ChecksumsVerified = $checksumsVerified
    }
}

$viperArtifactStage = Join-Path $stageDir "viper-artifact"
switch ($ViperSource) {
    "WorkflowArtifact" { $viperMetadata = Resolve-ViperWorkflowArtifact -OutputStage $viperArtifactStage }
    "LocalArtifact" { $viperMetadata = Resolve-ViperLocalArtifact -OutputStage $viperArtifactStage }
    "Release" { $viperMetadata = Resolve-ViperRelease -OutputStage $viperArtifactStage }
}

$expectedGuiArchiveName = "viper-patcher_$($viperMetadata.Version)_windows_amd64.zip"
$expectedCliArchiveName = "viper-patcher-cli_$($viperMetadata.Version)_windows_amd64.zip"
$guiArchives = @(Get-ChildItem -LiteralPath $viperArtifactStage -Recurse -File | Where-Object { $_.Name -eq $expectedGuiArchiveName })
$cliArchives = @(Get-ChildItem -LiteralPath $viperArtifactStage -Recurse -File | Where-Object { $_.Name -eq $expectedCliArchiveName })
if ($guiArchives.Count -ne 1 -or $cliArchives.Count -ne 1) {
    $available = (Get-ChildItem -LiteralPath $viperArtifactStage -Recurse -File | ForEach-Object { "  - $($_.FullName)" }) -join [Environment]::NewLine
    throw "Expected one hybrid archive '$expectedGuiArchiveName' and one CLI-only archive '$expectedCliArchiveName'.`n$available"
}

$viperGuiStage = Join-Path $stageDir "viper-gui"
$viperCliStage = Join-Path $stageDir "viper-cli"
Expand-ToolAsset -AssetPath $guiArchives[0].FullName -ToolStage $viperGuiStage
Expand-ToolAsset -AssetPath $cliArchives[0].FullName -ToolStage $viperCliStage

$releaseSpecs = @(
    [pscustomobject]@{
        Name = "flips"
        Repository = "Alcaro/Flips"
        Tag = "v198"
        Patterns = @(
            '(?i)^flips.*windows.*(x64|amd64|x86_64|64).*\.(zip|exe)$',
            '(?i)^flips.*(x64|amd64|x86_64|64).*(windows|win).*\.(zip|exe)$',
            '(?i)^flips.*windows.*\.(zip|exe)$',
            '(?i)^flips.*win.*\.(zip|exe)$'
        )
    },
    [pscustomobject]@{
        Name = "xdelta"
        Repository = "jmacd/xdelta"
        Tag = "v3.2.0"
        Patterns = @(
            '(?i)^xdelta.*(windows|win).*(x64|amd64|x86_64|64).*\.zip$',
            '(?i)^xdelta.*(x64|amd64|x86_64|64).*(windows|win).*\.zip$',
            '(?i)^xdelta.*(windows|win).*\.zip$',
            '(?i)^xdelta3.*(x64|amd64|x86_64|64).*\.zip$'
        )
    },
    [pscustomobject]@{
        Name = "hdiffpatch"
        Repository = "sisong/HDiffPatch"
        Tag = "v5.1.2"
        Patterns = @(
            '(?i)^hdiffpatch.*(windows|win).*(x64|amd64|x86_64|64).*\.zip$',
            '(?i)^hdiffpatch.*(x64|amd64|x86_64|64).*(windows|win).*\.zip$',
            '(?i)^hdiffpatch.*win64.*\.zip$',
            '(?i)^hdiffpatch.*(windows|win).*\.zip$'
        )
    }
)

$releaseLockEntries = [Collections.Generic.List[object]]::new()
foreach ($spec in $releaseSpecs) {
    Write-Host "Resolving $($spec.Repository) $($spec.Tag)..."
    $release = Invoke-GitHubJson "https://api.github.com/repos/$($spec.Repository)/releases/tags/$($spec.Tag)"
    $asset = Select-ReleaseAsset -Release $release -Patterns $spec.Patterns -ToolName $spec.Name
    $assetPath = Join-Path $downloadDir $asset.name
    Write-Host "Downloading $($asset.name)..."
    Download-ReleaseAsset -Asset $asset -OutputPath $assetPath
    $toolStage = Join-Path $stageDir $spec.Name
    Expand-ToolAsset -AssetPath $assetPath -ToolStage $toolStage
    $releaseLockEntries.Add([pscustomobject]@{
        tool = $spec.Name
        repository = $spec.Repository
        tag = $spec.Tag
        release_id = $release.id
        release_published_at = $release.published_at
        asset_name = $asset.name
        asset_size = $asset.size
        asset_sha256 = (Get-FileHash -LiteralPath $assetPath -Algorithm SHA256).Hash.ToLowerInvariant()
        asset_url = $asset.browser_download_url
    })
}

$installed = [Collections.Generic.List[object]]::new()
$installed.Add((Install-NormalizedExecutable -Candidate (Find-PreferredExecutable $viperGuiStage @("creator.exe") '(?i)^creator\.exe$' "AMD64") -DestinationName "creator-hybrid.exe"))
$installed.Add((Install-NormalizedExecutable -Candidate (Find-PreferredExecutable $viperGuiStage @("patcher.exe") '(?i)^patcher\.exe$' "AMD64") -DestinationName "patcher-hybrid.exe"))
$installed.Add((Install-NormalizedExecutable -Candidate (Find-PreferredExecutable $viperCliStage @("creator-cli.exe") '(?i)^creator-cli\.exe$' "AMD64") -DestinationName "creator-cli.exe"))
$installed.Add((Install-NormalizedExecutable -Candidate (Find-PreferredExecutable $viperCliStage @("patcher-cli.exe") '(?i)^patcher-cli\.exe$' "AMD64") -DestinationName "patcher-cli.exe"))

$viperBinaryMetadata = [Collections.Generic.List[object]]::new()
foreach ($binaryName in @("creator-hybrid.exe", "patcher-hybrid.exe", "creator-cli.exe", "patcher-cli.exe")) {
    $binaryPath = Join-Path $binDir $binaryName
    $versionOutput = (& $binaryPath --version 2>&1 | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $versionOutput -notmatch '(?m)\bviper-patcher\s+(creator|patcher)\s+([^\s]+)\s+\(([0-9a-fA-F]{7,40}),') {
        throw "Could not parse Viper version metadata from ${binaryName}: '$versionOutput'"
    }
    $viperBinaryMetadata.Add([pscustomobject]@{
        name = $binaryName
        role = $Matches[1]
        version = $Matches[2]
        commit = $Matches[3].ToLowerInvariant()
        output = $versionOutput
    })
}
$binaryVersions = @($viperBinaryMetadata.version | Sort-Object -Unique)
$binaryCommits = @($viperBinaryMetadata.commit | Sort-Object -Unique)
if ($binaryVersions.Count -ne 1 -or $binaryVersions[0] -ne $viperMetadata.Version) {
    throw "The four Viper binaries do not all report expected version '$($viperMetadata.Version)': $($binaryVersions -join ', ')."
}
if ($binaryCommits.Count -ne 1) {
    throw "The four Viper binaries do not report one common commit: $($binaryCommits -join ', ')."
}
$binaryCommit = [string]$binaryCommits[0]
if (-not [string]::IsNullOrWhiteSpace([string]$viperMetadata.Commit)) {
    $expectedCommit = ([string]$viperMetadata.Commit).ToLowerInvariant()
    if (-not ($expectedCommit.StartsWith($binaryCommit) -or $binaryCommit.StartsWith($expectedCommit))) {
        throw "Viper binaries report commit '$binaryCommit', but the selected source resolves to '$expectedCommit'."
    }
}
$viperMetadata.Commit = $binaryCommit

$flipsStage = Join-Path $stageDir "flips"
$xdeltaStage = Join-Path $stageDir "xdelta"
$hdiffStage = Join-Path $stageDir "hdiffpatch"
$installed.Add((Install-NormalizedExecutable -Candidate (Find-PreferredExecutable $flipsStage @("flips.exe", "flips-windows.exe") '(?i)^flips.*\.exe$') -DestinationName "flips.exe"))
$installed.Add((Install-NormalizedExecutable -Candidate (Find-PreferredExecutable $xdeltaStage @("xdelta3.exe") '(?i)^xdelta3.*\.exe$') -DestinationName "xdelta3.exe"))
$installed.Add((Install-NormalizedExecutable -Candidate (Find-PreferredExecutable $hdiffStage @("hdiffz.exe") '(?i)^hdiffz.*\.exe$') -DestinationName "hdiffz.exe"))
$installed.Add((Install-NormalizedExecutable -Candidate (Find-PreferredExecutable $hdiffStage @("hpatchz.exe") '(?i)^hpatchz.*\.exe$') -DestinationName "hpatchz.exe"))

# Preserve adjacent runtime DLLs. If duplicate names exist, prefer the one beside
# an AMD64 executable, then the shortest path. This is recorded by hash below.
$dllCandidates = @(Get-ChildItem -LiteralPath $stageDir -Recurse -File -Filter *.dll | Sort-Object FullName)
foreach ($group in ($dllCandidates | Group-Object Name)) {
    $selected = @($group.Group | Sort-Object @{ Expression = {
        $siblings = @(Get-ChildItem -LiteralPath $_.DirectoryName -File -Filter *.exe -ErrorAction SilentlyContinue)
        if (@($siblings | Where-Object { (Get-PEArchitecture $_.FullName) -eq "AMD64" }).Count -gt 0) { 0 } else { 1 }
    } }, @{ Expression = { $_.FullName.Length } }, FullName)[0]
    Copy-Item -LiteralPath $selected.FullName -Destination (Join-Path $binDir $selected.Name) -Force
}

foreach ($item in $installed) {
    if ($item.Architecture -ne "AMD64") {
        Write-Warning "$($item.File.Name) is $($item.Architecture), because no AMD64 candidate was found in the selected upstream asset."
    }
}

$binaryEntries = @($installed | ForEach-Object {
    [pscustomobject]@{
        name = $_.File.Name
        bytes = $_.File.Length
        architecture = $_.Architecture
        source_path = $_.Source
        sha256 = (Get-FileHash -LiteralPath $_.File.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    }
})
$dllEntries = @(Get-ChildItem -LiteralPath $binDir -File -Filter *.dll | ForEach-Object {
    [pscustomobject]@{
        name = $_.Name
        bytes = $_.Length
        sha256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    }
})

$viperArchiveEntries = @($guiArchives[0], $cliArchives[0]) | ForEach-Object {
    [pscustomobject]@{
        name = $_.Name
        bytes = $_.Length
        sha256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

$lock = [ordered]@{
    generated_at_utc = [DateTime]::UtcNow.ToString("o")
    downloader_version = $DownloaderVersion
    viper = [ordered]@{
        repository = $ViperRepository
        source = $viperMetadata.Source
        requested_ref = if ($ViperSource -eq "WorkflowArtifact") { $ViperRef } else { $null }
        requested_tag = if ($ViperSource -eq "Release") { $ViperTag } else { $null }
        version = $viperMetadata.Version
        repository_version = $viperMetadata.RepositoryVersion
        commit = $viperMetadata.Commit
        workflow = $ViperWorkflow
        ci_run_id = $viperMetadata.RunId
        ci_run_number = $viperMetadata.RunNumber
        artifact_id = $viperMetadata.ArtifactId
        artifact_name = $viperMetadata.ArtifactName
        artifact_created_at = $viperMetadata.ArtifactCreatedAt
        release_id = $viperMetadata.ReleaseId
        release_prerelease = $viperMetadata.ReleasePrerelease
        release_checksums_verified = $viperMetadata.ChecksumsVerified
        binary_version_metadata = @($viperBinaryMetadata)
        archives = $viperArchiveEntries
    }
    releases = @($releaseLockEntries)
    normalized_binaries = $binaryEntries
    runtime_dlls = $dllEntries
}
$lock | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath (Join-Path $destinationPath "tools-lock.json") -Encoding utf8

Write-Host ""
Write-Host "Installed benchmark binaries in: $binDir"
foreach ($item in $installed) {
    $hash = (Get-FileHash -LiteralPath $item.File.FullName -Algorithm SHA256).Hash.Substring(0, 16)
    Write-Host ("  {0,-20} {1,-6} {2,12:N0} bytes  SHA256 {3}..." -f $item.File.Name, $item.Architecture, $item.File.Length, $hash)
}
Write-Host ""
Write-Host "Viper source: $($viperMetadata.Source), version $($viperMetadata.Version), commit $($viperMetadata.Commit)"
if ($null -ne $viperMetadata.RunId) {
    Write-Host "CI run: $($viperMetadata.RunId), artifact: $($viperMetadata.ArtifactName)"
}
Write-Host "The exact upstream assets, architectures, and SHA-256 hashes are recorded in tools\tools-lock.json."
