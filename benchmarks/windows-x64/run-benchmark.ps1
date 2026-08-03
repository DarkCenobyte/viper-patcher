[CmdletBinding()]
param(
    [string]$ToolsDir = (Join-Path $PSScriptRoot "tools\bin"),
    [string]$OutputRoot = (Join-Path $PSScriptRoot "runs"),
    [int]$SmallIterations = 7,
    [int]$LargeIterations = 5,
    [int]$HugeIterations = 2,
    [switch]$Include500MB,
    [switch]$NoWarmup,
    [switch]$KeepDatasets,
    [string]$RunLabel = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$BenchmarkScriptVersion = "1.2.3"
$RequiredHDiffVersion = "5.1.2"
Write-Host "Viper-Patcher Windows x64 benchmark v$BenchmarkScriptVersion"

if ($PSVersionTable.PSVersion.Major -lt 7) {
    throw "PowerShell 7 or newer is required. Run this script with pwsh.exe."
}
if ($SmallIterations -lt 1 -or $LargeIterations -lt 1 -or $HugeIterations -lt 1) {
    throw "All iteration counts must be at least 1."
}

$tools = [ordered]@{
    viper_hybrid_creator = Join-Path $ToolsDir "creator-hybrid.exe"
    viper_hybrid_patcher = Join-Path $ToolsDir "patcher-hybrid.exe"
    viper_cli_creator = Join-Path $ToolsDir "creator-cli.exe"
    viper_cli_patcher = Join-Path $ToolsDir "patcher-cli.exe"
    flips = Join-Path $ToolsDir "flips.exe"
    xdelta = Join-Path $ToolsDir "xdelta3.exe"
    hdiff = Join-Path $ToolsDir "hdiffz.exe"
    hpatch = Join-Path $ToolsDir "hpatchz.exe"
}
foreach ($entry in $tools.GetEnumerator()) {
    if (-not (Test-Path -LiteralPath $entry.Value -PathType Leaf)) {
        throw "Missing binary '$($entry.Key)': $($entry.Value). Run .\download-tools.ps1 -Force first."
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
            switch ($reader.ReadUInt16()) {
                0x8664 { return "AMD64" }
                0x014C { return "I386" }
                0xAA64 { return "ARM64" }
                default { return "OtherPE" }
            }
        } finally {
            $reader.Dispose()
        }
    } finally {
        $stream.Dispose()
    }
}

$toolArchitectures = [ordered]@{}
foreach ($entry in $tools.GetEnumerator()) {
    $architecture = Get-PEArchitecture $entry.Value
    $toolArchitectures[$entry.Key] = $architecture
    if ($entry.Key -like "viper_*") {
        if ($architecture -ne "AMD64") {
            throw "Viper benchmark binaries must be Windows AMD64, but '$($entry.Value)' is $architecture."
        }
    } elseif ($architecture -ne "AMD64") {
        Write-Warning "$($entry.Key) is $architecture. The downloader only accepts this when no AMD64 candidate exists in the selected upstream asset."
    }
}

$toolsRoot = Split-Path -Parent $ToolsDir
$toolsLockPath = Join-Path $toolsRoot "tools-lock.json"
if (-not (Test-Path -LiteralPath $toolsLockPath -PathType Leaf)) {
    throw "Missing tools lock: $toolsLockPath. Run .\download-tools.ps1 -Force first."
}
$toolsLock = Get-Content -LiteralPath $toolsLockPath -Raw | ConvertFrom-Json
if ($null -eq $toolsLock.PSObject.Properties["viper"] -or
    [string]::IsNullOrWhiteSpace([string]$toolsLock.viper.version)) {
    throw "tools-lock.json does not contain a Viper version."
}
$RequiredViperVersion = [string]$toolsLock.viper.version
if ($RequiredViperVersion -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$') {
    throw "Invalid Viper version in tools-lock.json: '$RequiredViperVersion'."
}
$RequiredViperCommit = [string]$toolsLock.viper.commit

function Assert-ViperVersion {
    param(
        [Parameter(Mandatory)] [string] $Path,
        [Parameter(Mandatory)] [ValidateSet("creator", "patcher")] [string] $Role,
        [Parameter(Mandatory)] [string] $Label
    )
    $output = (& $Path --version 2>&1 | Out-String).Trim()
    $exitCode = $LASTEXITCODE
    $requiredPattern = "(?m)\bviper-patcher " + [Regex]::Escape($Role) + "\s+" + [Regex]::Escape($RequiredViperVersion) + "(?=\s|$)"
    if ($exitCode -ne 0 -or $output -notmatch $requiredPattern) {
        throw "Viper-Patcher $RequiredViperVersion is required, but $Label reported: '$output'. Run .\download-tools.ps1 -Force."
    }
    return $output
}

$viperVersionOutputs = [ordered]@{
    hybrid_creator = Assert-ViperVersion -Path $tools.viper_hybrid_creator -Role "creator" -Label "creator-hybrid.exe"
    hybrid_patcher = Assert-ViperVersion -Path $tools.viper_hybrid_patcher -Role "patcher" -Label "patcher-hybrid.exe"
    cli_creator = Assert-ViperVersion -Path $tools.viper_cli_creator -Role "creator" -Label "creator-cli.exe"
    cli_patcher = Assert-ViperVersion -Path $tools.viper_cli_patcher -Role "patcher" -Label "patcher-cli.exe"
}
Write-Host "Viper hybrid creator detected: $($viperVersionOutputs.hybrid_creator)"
Write-Host "Viper hybrid patcher detected: $($viperVersionOutputs.hybrid_patcher)"
Write-Host "Viper CLI-only creator detected: $($viperVersionOutputs.cli_creator)"
Write-Host "Viper CLI-only patcher detected: $($viperVersionOutputs.cli_patcher)"

$hasViperLock = $true
if ([string]$toolsLock.viper.version -ne $RequiredViperVersion) {
    throw "tools-lock.json contains inconsistent Viper version metadata."
}
if (-not [string]::IsNullOrWhiteSpace($RequiredViperCommit)) {
    foreach ($versionOutput in $viperVersionOutputs.Values) {
        if ($versionOutput -notmatch [Regex]::Escape($RequiredViperCommit)) {
            throw "Viper binary version output does not contain the locked commit ${RequiredViperCommit}: '$versionOutput'."
        }
    }
}

$hdiffVersionOutput = (& $tools.hdiff -v 2>&1 | Out-String).Trim()
$hdiffVersionExitCode = $LASTEXITCODE
$hdiffRequiredPattern = "v" + [Regex]::Escape($RequiredHDiffVersion) + "\b"
if ($hdiffVersionExitCode -ne 0 -or $hdiffVersionOutput -notmatch $hdiffRequiredPattern) {
    throw "HDiffPatch $RequiredHDiffVersion is required, but the installed hdiffz reported: '$hdiffVersionOutput'. Run .\download-tools.ps1 -Force."
}
Write-Host "HDiffPatch detected: $hdiffVersionOutput"

$runStamp = Get-Date -Format "yyyyMMdd-HHmmss"
$normalizedRunLabel = ($RunLabel -replace '[^0-9A-Za-z._-]+', '-').Trim('-')
$runDirectoryName = if ([string]::IsNullOrWhiteSpace($normalizedRunLabel)) { $runStamp } else { "$runStamp-$normalizedRunLabel" }
$runRoot = Join-Path ([IO.Path]::GetFullPath($OutputRoot)) $runDirectoryName
$dataRoot = Join-Path $runRoot "datasets"
$workRoot = Join-Path $runRoot "work"
$patchRoot = Join-Path $runRoot "patches"
$resultsPath = Join-Path $runRoot "results.csv"
$summaryPath = Join-Path $runRoot "summary.csv"
$fairComparisonPath = Join-Path $runRoot "fair-comparison.csv"
$reportPath = Join-Path $runRoot "report.md"
$readmeSnippetPath = Join-Path $runRoot "README-snippet.md"
$systemPath = Join-Path $runRoot "system.json"
$expectedHashesPath = Join-Path $runRoot "expected-hashes.json"
$runCommandPath = Join-Path $runRoot "run-command.txt"
New-Item -ItemType Directory -Force -Path $runRoot, $dataRoot, $workRoot, $patchRoot | Out-Null
Copy-Item -LiteralPath $toolsLockPath -Destination (Join-Path $runRoot "tools-lock.json") -Force

$logicalCpu = [Environment]::ProcessorCount
$gomaxprocsOverride = 0
$hasGomaxprocsOverride = [int]::TryParse([string]$env:GOMAXPROCS, [ref]$gomaxprocsOverride) -and $gomaxprocsOverride -gt 0
$automaticWorkersEstimate = if ($hasGomaxprocsOverride) {
    [Math]::Max(1, [Math]::Min($logicalCpu, $gomaxprocsOverride))
} else {
    $logicalCpu
}
$automaticWorkersDescription = if ($hasGomaxprocsOverride) {
    "--workers 0 asks Go for runtime.GOMAXPROCS(0); GOMAXPROCS=$gomaxprocsOverride and $logicalCpu logical processors imply an estimated target of $automaticWorkersEstimate."
} else {
    "--workers 0 asks Go for runtime.GOMAXPROCS(0); with no GOMAXPROCS override and $logicalCpu logical processors visible, the expected target is $automaticWorkersEstimate."
}
Write-Host "Viper automatic workers: $automaticWorkersDescription"

$script:Results = [Collections.Generic.List[object]]::new()
$script:VariantMetadata = @{}

function Register-VariantMetadata {
    param(
        [Parameter(Mandatory)] [string] $Tool,
        [Parameter(Mandatory)] [string] $Variant,
        [string] $Implementation = "",
        [AllowNull()] $CompressionLevel = $null,
        [AllowNull()] $WorkersRequested = $null,
        [string] $WorkersMode = "",
        [AllowNull()] $WorkersEffectiveEstimate = $null,
        [string] $ComparisonClass = "tuned"
    )
    $script:VariantMetadata["$Tool`n$Variant"] = [pscustomobject]@{
        Implementation = $Implementation
        CompressionLevel = $CompressionLevel
        WorkersRequested = $WorkersRequested
        WorkersMode = $WorkersMode
        WorkersEffectiveEstimate = $WorkersEffectiveEstimate
        ComparisonClass = $ComparisonClass
    }
}

Add-Type -TypeDefinition @'
using System;
using System.IO;

public static class ViprBenchData
{
    private static ulong Next(ref ulong state)
    {
        state += 0x9E3779B97F4A7C15UL;
        ulong z = state;
        z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9UL;
        z = (z ^ (z >> 27)) * 0x94D049BB133111EBUL;
        return z ^ (z >> 31);
    }

    private static void Fill(byte[] buffer, int count, ref ulong state)
    {
        int i = 0;
        while (i < count)
        {
            ulong value = Next(ref state);
            for (int j = 0; j < 8 && i < count; ++j, ++i)
            {
                buffer[i] = (byte)(value >> (j * 8));
            }
        }
    }

    public static void GenerateTriplet(
        string sourcePath,
        string scatteredPath,
        string unrelatedPath,
        long size,
        ulong seed)
    {
        const int BufferSize = 1024 * 1024;
        byte[] source = new byte[BufferSize];
        byte[] scattered = new byte[BufferSize];
        byte[] unrelated = new byte[BufferSize];

        Directory.CreateDirectory(Path.GetDirectoryName(sourcePath));
        Directory.CreateDirectory(Path.GetDirectoryName(scatteredPath));
        Directory.CreateDirectory(Path.GetDirectoryName(unrelatedPath));

        long mutationCount = Math.Max(1L, size / 1000L); // exactly 0.1%, rounded down
        long mutationIndex = 0L;
        long nextMutation = size / (2L * mutationCount);
        long offset = 0L;
        ulong sourceState = seed;
        ulong unrelatedState = seed ^ 0xD1B54A32D192ED03UL;

        using (var sourceStream = new FileStream(sourcePath, FileMode.Create, FileAccess.Write, FileShare.None, BufferSize, FileOptions.SequentialScan))
        using (var scatteredStream = new FileStream(scatteredPath, FileMode.Create, FileAccess.Write, FileShare.None, BufferSize, FileOptions.SequentialScan))
        using (var unrelatedStream = new FileStream(unrelatedPath, FileMode.Create, FileAccess.Write, FileShare.None, BufferSize, FileOptions.SequentialScan))
        {
            while (offset < size)
            {
                int count = (int)Math.Min(BufferSize, size - offset);
                Fill(source, count, ref sourceState);
                Buffer.BlockCopy(source, 0, scattered, 0, count);
                Fill(unrelated, count, ref unrelatedState);

                while (mutationIndex < mutationCount && nextMutation < offset + count)
                {
                    int localIndex = checked((int)(nextMutation - offset));
                    scattered[localIndex] ^= (byte)(0xA5 ^ (mutationIndex & 0x1F));
                    mutationIndex++;
                    if (mutationIndex < mutationCount)
                    {
                        nextMutation = checked(((2L * mutationIndex + 1L) * size) / (2L * mutationCount));
                    }
                }

                sourceStream.Write(source, 0, count);
                scatteredStream.Write(scattered, 0, count);
                unrelatedStream.Write(unrelated, 0, count);
                offset += count;
            }
        }
    }
}
'@


function Format-Command {
    param([Parameter(Mandatory)] $Command)
    $parts = @($Command.File) + @($Command.Arguments)
    return ($parts | ForEach-Object {
        $text = [string]$_
        if ($text -match '[\s"]') {
            '"' + ($text -replace '"', '\"') + '"'
        } else {
            $text
        }
    }) -join " "
}

function Get-TailText {
    param([AllowNull()] [string] $Text, [int] $Maximum = 3000)
    if ([string]::IsNullOrEmpty($Text)) { return "" }
    if ($Text.Length -le $Maximum) { return $Text.Trim() }
    return $Text.Substring($Text.Length - $Maximum).Trim()
}

function Invoke-OneProcess {
    param([Parameter(Mandatory)] $Command)

    $psi = [Diagnostics.ProcessStartInfo]::new()
    $psi.FileName = $Command.File
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    foreach ($argument in $Command.Arguments) {
        [void]$psi.ArgumentList.Add([string]$argument)
    }

    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $psi
    $stopwatch = [Diagnostics.Stopwatch]::StartNew()
    if (-not $process.Start()) {
        throw "Could not start process: $(Format-Command $Command)"
    }
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $process.WaitForExit()
    $stopwatch.Stop()
    $process.Refresh()

    $result = [pscustomobject]@{
        WallMs = [double]$stopwatch.Elapsed.TotalMilliseconds
        UserMs = [double]$process.UserProcessorTime.TotalMilliseconds
        KernelMs = [double]$process.PrivilegedProcessorTime.TotalMilliseconds
        PeakWorkingSetBytes = [long]$process.PeakWorkingSet64
        ExitCode = [int]$process.ExitCode
        Stdout = $stdoutTask.GetAwaiter().GetResult()
        Stderr = $stderrTask.GetAwaiter().GetResult()
        Command = Format-Command $Command
    }
    $process.Dispose()
    return $result
}

function Invoke-CommandSequence {
    param([Parameter(Mandatory)] [object[]] $Commands)

    $totalStopwatch = [Diagnostics.Stopwatch]::StartNew()
    $userMs = 0.0
    $kernelMs = 0.0
    $peak = 0L
    $exitCode = 0
    $stdoutParts = [Collections.Generic.List[string]]::new()
    $stderrParts = [Collections.Generic.List[string]]::new()
    $commandParts = [Collections.Generic.List[string]]::new()

    foreach ($command in $Commands) {
        $single = Invoke-OneProcess -Command $command
        $userMs += $single.UserMs
        $kernelMs += $single.KernelMs
        if ($single.PeakWorkingSetBytes -gt $peak) { $peak = $single.PeakWorkingSetBytes }
        $stdoutParts.Add($single.Stdout)
        $stderrParts.Add($single.Stderr)
        $commandParts.Add($single.Command)
        if ($single.ExitCode -ne 0) {
            $exitCode = $single.ExitCode
            break
        }
    }
    $totalStopwatch.Stop()

    [pscustomobject]@{
        WallMs = [double]$totalStopwatch.Elapsed.TotalMilliseconds
        UserMs = $userMs
        KernelMs = $kernelMs
        PeakWorkingSetBytes = $peak
        ExitCode = $exitCode
        Stdout = ($stdoutParts -join "`n--- next process ---`n")
        Stderr = ($stderrParts -join "`n--- next process ---`n")
        Command = ($commandParts -join " && ")
    }
}

function global:ViprBench-GetDirectoryBytes {
    param([Parameter(Mandatory)] [string] $Path)

    if (-not (Test-Path -LiteralPath $Path)) {
        return [long]0
    }

    $item = Get-Item -LiteralPath $Path -ErrorAction Stop
    if (-not $item.PSIsContainer) {
        return [long]$item.Length
    }

    [long]$totalBytes = 0
    foreach ($file in Get-ChildItem -LiteralPath $Path -Recurse -File -ErrorAction Stop) {
        $totalBytes += [long]$file.Length
    }

    return $totalBytes
}

function global:ViprBench-GetRelativeHashes {
    param([Parameter(Mandatory)] [string] $Root)
    if (-not (Test-Path -LiteralPath $Root -PathType Container)) {
        throw "Hash root directory does not exist: $Root"
    }

    $rootFull = [IO.Path]::GetFullPath($Root).TrimEnd('\') + '\'
    $map = [ordered]@{}
    foreach ($file in Get-ChildItem -LiteralPath $Root -Recurse -File | Sort-Object FullName) {
        $relative = $file.FullName.Substring($rootFull.Length).Replace('\', '/')
        $map[$relative] = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    return $map
}

function global:ViprBench-VerifyDirectoryHashes {
    param(
        [Parameter(Mandatory)] [string] $ActualRoot,
        [Parameter(Mandatory)] $ExpectedHashes
    )

    if (-not (Test-Path -LiteralPath $ActualRoot -PathType Container)) {
        return [pscustomobject]@{
            Passed = $false
            Message = "Output directory does not exist: $ActualRoot"
            ExpectedFileCount = $ExpectedHashes.Count
            ActualFileCount = 0
        }
    }

    try {
        $actualHashes = ViprBench-GetRelativeHashes $ActualRoot
    } catch {
        return [pscustomobject]@{
            Passed = $false
            Message = "Could not hash output files: $($_.Exception.Message)"
            ExpectedFileCount = $ExpectedHashes.Count
            ActualFileCount = 0
        }
    }

    foreach ($relativePath in $ExpectedHashes.Keys) {
        if (-not $actualHashes.Contains($relativePath)) {
            return [pscustomobject]@{
                Passed = $false
                Message = "Missing output file: $relativePath"
                ExpectedFileCount = $ExpectedHashes.Count
                ActualFileCount = $actualHashes.Count
            }
        }

        $expectedHash = ([string]$ExpectedHashes[$relativePath]).ToLowerInvariant()
        $actualHash = ([string]$actualHashes[$relativePath]).ToLowerInvariant()
        if ($actualHash -ne $expectedHash) {
            return [pscustomobject]@{
                Passed = $false
                Message = "SHA-256 mismatch for ${relativePath}: expected $expectedHash, got $actualHash"
                ExpectedFileCount = $ExpectedHashes.Count
                ActualFileCount = $actualHashes.Count
            }
        }
    }

    foreach ($relativePath in $actualHashes.Keys) {
        if (-not $ExpectedHashes.Contains($relativePath)) {
            return [pscustomobject]@{
                Passed = $false
                Message = "Unexpected output file: $relativePath"
                ExpectedFileCount = $ExpectedHashes.Count
                ActualFileCount = $actualHashes.Count
            }
        }
    }

    return [pscustomobject]@{
        Passed = $true
        Message = "All $($ExpectedHashes.Count) output file SHA-256 hashes match the destination dataset."
        ExpectedFileCount = $ExpectedHashes.Count
        ActualFileCount = $actualHashes.Count
    }
}

function global:ViprBench-CopyDirectoryContents {
    param(
        [Parameter(Mandatory)] [string] $Source,
        [Parameter(Mandatory)] [string] $Destination
    )
    if (Test-Path -LiteralPath $Destination) {
        Remove-Item -LiteralPath $Destination -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    Get-ChildItem -LiteralPath $Source -Force | Copy-Item -Destination $Destination -Recurse -Force
}

function Save-Results {
    $script:Results | Export-Csv -LiteralPath $resultsPath -NoTypeInformation -Encoding utf8
}

function Add-Result {
    param(
        [Parameter(Mandatory)] [string] $Tool,
        [Parameter(Mandatory)] [string] $Version,
        [Parameter(Mandatory)] $Case,
        [Parameter(Mandatory)] [string] $Phase,
        [Parameter(Mandatory)] [string] $Variant,
        [Parameter(Mandatory)] [int] $Iteration,
        [Parameter(Mandatory)] $Metric,
        [Parameter(Mandatory)] [long] $PatchBytes,
        [Parameter(Mandatory)] [string] $Status,
        [string] $VerificationStatus = "not_applicable",
        [double] $VerificationMs = 0.0,
        [string] $VerificationMessage = "",
        [string] $PatchValidationStatus = "not_applicable",
        [double] $PatchValidationMs = 0.0,
        [string] $PatchValidationMessage = ""
    )

    $metadataKey = "$Tool`n$Variant"
    $metadata = if ($script:VariantMetadata.ContainsKey($metadataKey)) {
        $script:VariantMetadata[$metadataKey]
    } else {
        [pscustomobject]@{
            Implementation = ""
            CompressionLevel = $null
            WorkersRequested = $null
            WorkersMode = ""
            WorkersEffectiveEstimate = $null
            ComparisonClass = "reference"
        }
    }

    $publishTiming = ($Status -eq "success")
    $publishPatchSize = ($Phase -eq "create" -and $Status -eq "success" -and $PatchBytes -gt 0)
    $roundedWallMs = [Math]::Round($Metric.WallMs, 3)
    $timeResult = if ($publishTiming) {
        "$roundedWallMs ms"
    } elseif ($Status -like "unsupported*") {
        "unsupported"
    } elseif ($Status -eq "not_created") {
        "not measured"
    } else {
        "failed"
    }

    $script:Results.Add([pscustomobject]@{
        timestamp_utc = [DateTime]::UtcNow.ToString("o")
        tool = $Tool
        version = $Version
        implementation = $metadata.Implementation
        comparison_class = $metadata.ComparisonClass
        compression_level = $metadata.CompressionLevel
        workers_requested = $metadata.WorkersRequested
        workers_mode = $metadata.WorkersMode
        workers_effective_estimate = $metadata.WorkersEffectiveEstimate
        case = $Case.Name
        relation = $Case.Relation
        file_count = $Case.FileCount
        bytes_per_file = $Case.BytesPerFile
        total_target_bytes = [long]$Case.FileCount * [long]$Case.BytesPerFile
        changed_bytes_per_file = if ($Case.Relation -eq "scattered") { [long][Math]::Max(1, [Math]::Floor($Case.BytesPerFile / 1000.0)) } else { $Case.BytesPerFile }
        phase = $Phase
        variant = $Variant
        iteration = $Iteration
        result = $timeResult
        time_result = $timeResult
        wall_ms = if ($publishTiming) { $roundedWallMs } else { $null }
        user_cpu_ms = if ($publishTiming) { [Math]::Round($Metric.UserMs, 3) } else { $null }
        kernel_cpu_ms = if ($publishTiming) { [Math]::Round($Metric.KernelMs, 3) } else { $null }
        peak_working_set_bytes = if ($publishTiming) { [long]$Metric.PeakWorkingSetBytes } else { $null }
        patch_size_bytes = if ($publishPatchSize) { [long]$PatchBytes } else { $null }
        patch_size_mib = if ($publishPatchSize) { [Math]::Round(([double]$PatchBytes / 1MB), 6) } else { $null }
        patch_bytes = $PatchBytes
        exit_code = [int]$Metric.ExitCode
        patch_validation = $PatchValidationStatus
        patch_validation_ms_not_timed = if ($PatchValidationStatus -in @("passed", "failed")) { [Math]::Round($PatchValidationMs, 3) } else { $null }
        patch_validation_message = $PatchValidationMessage
        sha256_verification = $VerificationStatus
        verification_ms_not_timed = if ($VerificationStatus -in @("passed", "failed")) { [Math]::Round($VerificationMs, 3) } else { $null }
        verification_message = $VerificationMessage
        status = $Status
        stdout_tail = Get-TailText $Metric.Stdout
        stderr_tail = Get-TailText $Metric.Stderr
        command = $Metric.Command
    })
    Save-Results
}

function Add-SkippedResult {
    param(
        [Parameter(Mandatory)] [string] $Tool,
        [Parameter(Mandatory)] [string] $Version,
        [Parameter(Mandatory)] $Case,
        [Parameter(Mandatory)] [string] $Phase,
        [Parameter(Mandatory)] [string] $Variant,
        [Parameter(Mandatory)] [string] $Status
    )
    $metric = [pscustomobject]@{
        WallMs = 0.0; UserMs = 0.0; KernelMs = 0.0
        PeakWorkingSetBytes = 0L; ExitCode = -1
        Stdout = ""; Stderr = ""; Command = ""
    }
    Add-Result -Tool $Tool -Version $Version -Case $Case -Phase $Phase `
        -Variant $Variant -Iteration 0 -Metric $metric -PatchBytes 0 `
        -Status $Status
}

function Get-Iterations {
    param([Parameter(Mandatory)] $Case)
    if ($Case.BytesPerFile -ge 500000000) { return $HugeIterations }
    if ($Case.BytesPerFile -ge 50000000) { return $LargeIterations }
    return $SmallIterations
}

function Get-CaseFiles {
    param([Parameter(Mandatory)] $Case)
    $oldFiles = @(Get-ChildItem -LiteralPath $Case.OldDir -File | Sort-Object Name)
    $newFiles = @(Get-ChildItem -LiteralPath $Case.NewDir -File | Sort-Object Name)
    if ($oldFiles.Count -ne $newFiles.Count) { throw "Mismatched dataset file count: $($Case.Name)" }
    $pairs = [Collections.Generic.List[object]]::new()
    for ($i = 0; $i -lt $oldFiles.Count; $i++) {
        $pairs.Add([pscustomobject]@{ Old = $oldFiles[$i].FullName; New = $newFiles[$i].FullName; Name = $oldFiles[$i].Name })
    }
    return @($pairs)
}

function Invoke-Warmup {
    param(
        [Parameter(Mandatory)] [scriptblock] $Prepare,
        [Parameter(Mandatory)] [scriptblock] $BuildCommands
    )
    if ($NoWarmup) { return $null }
    & $Prepare
    $commands = @(& $BuildCommands)
    return Invoke-CommandSequence -Commands $commands
}

function Invoke-UnmeasuredPatchValidation {
    param(
        [Parameter(Mandatory)] [scriptblock] $Prepare,
        [Parameter(Mandatory)] [scriptblock] $BuildCommands,
        [Parameter(Mandatory)] [scriptblock] $Verify
    )

    $total = [Diagnostics.Stopwatch]::StartNew()
    $passed = $false
    $message = "Validation did not complete."
    try {
        & $Prepare
        $commands = @(& $BuildCommands)
        $metric = Invoke-CommandSequence -Commands $commands
        if ($metric.ExitCode -ne 0) {
            $message = "Unmeasured validation application failed with exit code $($metric.ExitCode): $($metric.Command)`n$(Get-TailText $metric.Stderr)"
        } else {
            try {
                $verification = & $Verify
                $passed = [bool]$verification.Passed
                $message = [string]$verification.Message
            } catch {
                $message = "Unmeasured validation SHA-256 check raised an error: $($_.Exception.Message)"
            }
        }
    } catch {
        $message = "Unmeasured validation setup or execution raised an error: $($_.Exception.Message)"
    } finally {
        $total.Stop()
    }

    return [pscustomobject]@{
        Passed = $passed
        ElapsedMs = $total.Elapsed.TotalMilliseconds
        Message = $message
    }
}

function Measure-Creation {
    param(
        [Parameter(Mandatory)] [string] $Tool,
        [Parameter(Mandatory)] [string] $Version,
        [Parameter(Mandatory)] $Case,
        [Parameter(Mandatory)] [string] $Variant,
        [Parameter(Mandatory)] [int] $Iterations,
        [Parameter(Mandatory)] [scriptblock] $Prepare,
        [Parameter(Mandatory)] [scriptblock] $BuildCommands,
        [Parameter(Mandatory)] [scriptblock] $GetPatchBytes,
        [AllowNull()] [scriptblock] $ValidatePatch = $null,
        [bool] $DoWarmup = $true,
        [string] $KnownUnsupportedStatus = ""
    )

    if ($DoWarmup -and -not $NoWarmup) {
        Write-Host "  [create] warm-up"
        $warm = Invoke-Warmup -Prepare $Prepare -BuildCommands $BuildCommands
        $patchBytes = [long](& $GetPatchBytes)
        $warmValidation = $null
        if ($null -ne $warm -and $warm.ExitCode -eq 0 -and $patchBytes -gt 0 -and $null -ne $ValidatePatch) {
            $warmValidation = & $ValidatePatch
        }

        if ($null -ne $warm -and ($warm.ExitCode -ne 0 -or $patchBytes -le 0 -or ($null -ne $warmValidation -and -not $warmValidation.Passed))) {
            $warmStatus = if ($warm.ExitCode -ne 0 -or $patchBytes -le 0) { "warmup_failed" } else { "warmup_patch_validation_failed" }
            $validationMessage = if ($null -ne $warmValidation) { $warmValidation.Message } else { "" }
            Write-Warning "Creation warm-up failed; measured runs for this variant are skipped: $($warm.Command)`n$validationMessage`n$(Get-TailText $warm.Stderr)"
            Add-Result -Tool $Tool -Version $Version -Case $Case -Phase "create" `
                -Variant $Variant -Iteration 0 -Metric $warm `
                -PatchBytes $patchBytes -Status $warmStatus `
                -PatchValidationStatus $(if ($null -eq $warmValidation) { "not_run" } elseif ($warmValidation.Passed) { "passed" } else { "failed" }) `
                -PatchValidationMs $(if ($null -eq $warmValidation) { 0.0 } else { $warmValidation.ElapsedMs }) `
                -PatchValidationMessage $validationMessage
            return $false
        }
    }

    $succeeded = $false
    for ($iteration = 1; $iteration -le $Iterations; $iteration++) {
        Write-Host "  [create] iteration $iteration/$Iterations"
        & $Prepare
        $commands = @(& $BuildCommands)
        $metric = Invoke-CommandSequence -Commands $commands
        $patchBytes = [long](& $GetPatchBytes)

        $validation = $null
        if ($metric.ExitCode -eq 0 -and $patchBytes -gt 0 -and $null -ne $ValidatePatch) {
            $validation = & $ValidatePatch
        }

        $status = if ($metric.ExitCode -ne 0 -or $patchBytes -le 0) {
            if ([string]::IsNullOrWhiteSpace($KnownUnsupportedStatus)) { "failed" } else { $KnownUnsupportedStatus }
        } elseif ($null -ne $validation -and -not $validation.Passed) {
            "patch_validation_failed"
        } else {
            "success"
        }

        Add-Result -Tool $Tool -Version $Version -Case $Case -Phase "create" `
            -Variant $Variant -Iteration $iteration -Metric $metric `
            -PatchBytes $patchBytes -Status $status `
            -PatchValidationStatus $(if ($null -eq $validation) { "not_run" } elseif ($validation.Passed) { "passed" } else { "failed" }) `
            -PatchValidationMs $(if ($null -eq $validation) { 0.0 } else { $validation.ElapsedMs }) `
            -PatchValidationMessage $(if ($null -eq $validation) { "" } else { $validation.Message })

        if ($status -eq "success") {
            Write-Host "    completed: $([Math]::Round($metric.WallMs, 3)) ms; patch: $patchBytes bytes; validation: passed"
        } else {
            Write-Warning "$Tool creation result rejected for $($Case.Name) / ${Variant}: $(if ($null -eq $validation) { Get-TailText $metric.Stderr } else { $validation.Message })"
        }

        $succeeded = ($status -eq "success")
        if (-not $succeeded) { break }
    }
    return $succeeded
}

function Measure-Application {
    param(
        [Parameter(Mandatory)] [string] $Tool,
        [Parameter(Mandatory)] [string] $Version,
        [Parameter(Mandatory)] $Case,
        [Parameter(Mandatory)] [string] $Variant,
        [Parameter(Mandatory)] [int] $Iterations,
        [Parameter(Mandatory)] [long] $PatchBytes,
        [Parameter(Mandatory)] [scriptblock] $Prepare,
        [Parameter(Mandatory)] [scriptblock] $BuildCommands,
        [Parameter(Mandatory)] [scriptblock] $Verify
    )

    if (-not $NoWarmup) {
        Write-Host "  [apply] warm-up"
        & $Prepare
        $warmCommands = @(& $BuildCommands)
        $warm = Invoke-CommandSequence -Commands $warmCommands

        $warmVerification = [pscustomobject]@{ Passed = $false; Message = "Application process failed before SHA-256 verification." }
        $warmVerificationMs = 0.0
        if ($warm.ExitCode -eq 0) {
            $verificationStopwatch = [Diagnostics.Stopwatch]::StartNew()
            try {
                $warmVerification = & $Verify
            } catch {
                $warmVerification = [pscustomobject]@{ Passed = $false; Message = "SHA-256 verification error: $($_.Exception.Message)" }
            } finally {
                $verificationStopwatch.Stop()
                $warmVerificationMs = $verificationStopwatch.Elapsed.TotalMilliseconds
            }
        }

        if ($warm.ExitCode -ne 0 -or -not $warmVerification.Passed) {
            $warmStatus = if ($warm.ExitCode -ne 0) { "warmup_failed" } else { "warmup_hash_mismatch" }
            Write-Warning "Application warm-up failed; measured runs for this variant are skipped: $($warm.Command)`n$($warmVerification.Message)`n$(Get-TailText $warm.Stderr)"
            Add-Result -Tool $Tool -Version $Version -Case $Case -Phase "apply" `
                -Variant $Variant -Iteration 0 -Metric $warm `
                -PatchBytes $PatchBytes -Status $warmStatus `
                -VerificationStatus $(if ($warm.ExitCode -eq 0) { "failed" } else { "not_run" }) `
                -VerificationMs $warmVerificationMs -VerificationMessage $warmVerification.Message
            return
        }
    }

    for ($iteration = 1; $iteration -le $Iterations; $iteration++) {
        Write-Host "  [apply] iteration $iteration/$Iterations"
        & $Prepare
        $commands = @(& $BuildCommands)

        # Only patch application is timed. SHA-256 verification starts after
        # Invoke-CommandSequence has stopped its stopwatch and returned.
        $metric = Invoke-CommandSequence -Commands $commands

        $verification = [pscustomobject]@{ Passed = $false; Message = "Application process failed before SHA-256 verification." }
        $verificationMs = 0.0
        $verificationStatus = "not_run"

        if ($metric.ExitCode -eq 0) {
            $verificationStopwatch = [Diagnostics.Stopwatch]::StartNew()
            try {
                $verification = & $Verify
                $verificationStatus = if ($verification.Passed) { "passed" } else { "failed" }
            } catch {
                $verification = [pscustomobject]@{ Passed = $false; Message = "SHA-256 verification error: $($_.Exception.Message)" }
                $verificationStatus = "failed"
            } finally {
                $verificationStopwatch.Stop()
                $verificationMs = $verificationStopwatch.Elapsed.TotalMilliseconds
            }
        }

        $status = if ($metric.ExitCode -ne 0) {
            "failed"
        } elseif (-not $verification.Passed) {
            "hash_mismatch"
        } else {
            "success"
        }

        Add-Result -Tool $Tool -Version $Version -Case $Case -Phase "apply" `
            -Variant $Variant -Iteration $iteration -Metric $metric `
            -PatchBytes $PatchBytes -Status $status `
            -VerificationStatus $verificationStatus -VerificationMs $verificationMs `
            -VerificationMessage $verification.Message

        if ($status -eq "success") {
            Write-Host "    completed: $([Math]::Round($metric.WallMs, 3)) ms; SHA-256: passed"
        }

        if ($status -ne "success") {
            Write-Warning "$Tool application result rejected for $($Case.Name) / ${Variant}: $($verification.Message)"
            break
        }
    }
}

# Unmeasured regression check: an empty patch directory must have a size of zero.
$emptySizeProbe = Join-Path $workRoot ".empty-size-probe"
New-Item -ItemType Directory -Force -Path $emptySizeProbe | Out-Null
try {
    $emptySize = ViprBench-GetDirectoryBytes $emptySizeProbe
    if ($emptySize -ne 0L) {
        throw "ViprBench-GetDirectoryBytes regression check failed: expected 0 bytes, got $emptySize."
    }
} finally {
    Remove-Item -LiteralPath $emptySizeProbe -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host "Generating deterministic datasets..."
$datasetShapes = @(
    [pscustomobject]@{ Key = "one_100KB"; FileCount = 1; BytesPerFile = 100000L },
    [pscustomobject]@{ Key = "ten_100KB"; FileCount = 10; BytesPerFile = 100000L },
    [pscustomobject]@{ Key = "one_50MB"; FileCount = 1; BytesPerFile = 50000000L }
)
if ($Include500MB) {
    $datasetShapes += [pscustomobject]@{ Key = "one_500MB"; FileCount = 1; BytesPerFile = 500000000L }
}

$cases = [Collections.Generic.List[object]]::new()
foreach ($shape in $datasetShapes) {
    $shapeRoot = Join-Path $dataRoot $shape.Key
    $oldDir = Join-Path $shapeRoot "old"
    $scatteredDir = Join-Path $shapeRoot "scattered"
    $unrelatedDir = Join-Path $shapeRoot "unrelated"
    New-Item -ItemType Directory -Force -Path $oldDir, $scatteredDir, $unrelatedDir | Out-Null

    for ($index = 1; $index -le $shape.FileCount; $index++) {
        $filename = "file{0:D3}.bin" -f $index
        $sourcePath = Join-Path $oldDir $filename
        $scatteredPath = Join-Path $scatteredDir $filename
        $unrelatedPath = Join-Path $unrelatedDir $filename
        $seed = [UInt64](1469598103934665603L + $shape.BytesPerFile + ($index * 104729L))
        [ViprBenchData]::GenerateTriplet($sourcePath, $scatteredPath, $unrelatedPath, $shape.BytesPerFile, $seed)
    }

    foreach ($relation in @("scattered", "unrelated")) {
        $targetDirectory = if ($relation -eq "scattered") { $scatteredDir } else { $unrelatedDir }
        $cases.Add([pscustomobject]@{
            Name = "$($shape.Key)_$relation"
            Relation = $relation
            FileCount = $shape.FileCount
            BytesPerFile = $shape.BytesPerFile
            OldDir = $oldDir
            NewDir = $targetDirectory
            ExpectedHashes = ViprBench-GetRelativeHashes $targetDirectory
        })
    }
}

$expectedHashesExport = [ordered]@{}
foreach ($caseEntry in $cases) {
    $expectedHashesExport[$caseEntry.Name] = $caseEntry.ExpectedHashes
}
$expectedHashesExport | ConvertTo-Json -Depth 12 |
    Set-Content -LiteralPath $expectedHashesPath -Encoding utf8

$canonicalCommandParts = [Collections.Generic.List[string]]::new()
$canonicalCommandParts.Add("pwsh -NoProfile -File .\benchmarks\windows-x64\run-benchmark.ps1")
$canonicalCommandParts.Add("-SmallIterations $SmallIterations")
$canonicalCommandParts.Add("-LargeIterations $LargeIterations")
$canonicalCommandParts.Add("-HugeIterations $HugeIterations")
if ($Include500MB) { $canonicalCommandParts.Add("-Include500MB") }
if ($NoWarmup) { $canonicalCommandParts.Add("-NoWarmup") }
if ($KeepDatasets) { $canonicalCommandParts.Add("-KeepDatasets") }
if (-not [string]::IsNullOrWhiteSpace($RunLabel)) { $canonicalCommandParts.Add("-RunLabel `"$RunLabel`"") }
$canonicalCommand = $canonicalCommandParts -join " "
Set-Content -LiteralPath $runCommandPath -Value $canonicalCommand -Encoding utf8

$cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
$os = Get-CimInstance Win32_OperatingSystem
$toolHashes = [ordered]@{}
foreach ($entry in $tools.GetEnumerator()) {
    $toolHashes[$entry.Key] = [ordered]@{
        path = [IO.Path]::GetFullPath($entry.Value)
        sha256 = (Get-FileHash -LiteralPath $entry.Value -Algorithm SHA256).Hash.ToLowerInvariant()
        bytes = (Get-Item -LiteralPath $entry.Value).Length
        architecture = $toolArchitectures[$entry.Key]
    }
}
$systemInfo = [ordered]@{
    started_at_utc = [DateTime]::UtcNow.ToString("o")
    run_root = $runRoot
    run_label = $RunLabel
    command = $canonicalCommand
    benchmark_script_version = $BenchmarkScriptVersion
    benchmark_script_sha256 = (Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant()
    powershell = $PSVersionTable.PSVersion.ToString()
    os = $os.Caption
    os_version = $os.Version
    cpu = $cpu.Name
    logical_processors = $logicalCpu
    physical_memory_bytes = [long]$os.TotalVisibleMemorySize * 1024L
    viper_source = if ($hasViperLock) { $toolsLock.viper } else { $null }
    viper_version_outputs = $viperVersionOutputs
    viper_automatic_workers = [ordered]@{
        requested_value = 0
        semantic = "runtime.GOMAXPROCS(0), capped to runtime.NumCPU()"
        logical_processors_visible_to_benchmark = $logicalCpu
        gomaxprocs_environment = if ($hasGomaxprocsOverride) { $gomaxprocsOverride } else { $null }
        effective_estimate = $automaticWorkersEstimate
        description = $automaticWorkersDescription
    }
    fairness = [ordered]@{
        measured_processes_are_serial = $true
        setup_and_sha256_are_outside_timing = $true
        every_successful_created_patch_is_applied_and_sha256_checked_outside_creation_timing = $true
        all_successful_measured_applications_are_sha256_checked_outside_application_timing = $true
        viper_hybrid_uses_omitted_defaults = $true
        competitor_commands_are_recorded_exactly = $true
        no_cross_tool_parallel_execution = $true
    }
    iterations = [ordered]@{
        small = $SmallIterations
        large = $LargeIterations
        huge = $HugeIterations
        warmup = -not $NoWarmup
    }
    data_definition = [ordered]@{
        units = "decimal bytes: 100 KB=100000; 50 MB=50000000; 500 MB=500000000"
        source = "deterministic SplitMix64 high-entropy stream"
        scattered = "exactly floor(size/1000) bytes changed, evenly distributed; minimum 1"
        unrelated = "independent deterministic SplitMix64 stream"
    }
    tools = $toolHashes
}
$systemInfo | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $systemPath -Encoding utf8

$viperVariants = [Collections.Generic.List[object]]::new()
$viperVariants.Add([pscustomobject]@{
    Tool = "Viper-Patcher hybrid"
    Implementation = "hybrid_gui_cli"
    Name = "hybrid_defaults_compression3_workers_auto"
    Creator = $tools.viper_hybrid_creator
    Patcher = $tools.viper_hybrid_patcher
    UseHeadless = $true
    Compression = 3
    Workers = 0
    ExplicitCompression = $false
    ExplicitWorkers = $false
    ComparisonClass = "default_reference"
})
foreach ($compression in @(1, 3, 9)) {
    foreach ($workers in @(1, 0)) {
        $workerLabel = if ($workers -eq 0) { "auto" } else { "1" }
        $comparisonClass = if ($compression -eq 3 -and $workers -eq 0) { "default_equivalent" } else { "tuned" }
        $viperVariants.Add([pscustomobject]@{
            Tool = "Viper-Patcher CLI-only"
            Implementation = "cli_only"
            Name = "cli_compression_${compression}_workers_${workerLabel}"
            Creator = $tools.viper_cli_creator
            Patcher = $tools.viper_cli_patcher
            UseHeadless = $false
            Compression = $compression
            Workers = $workers
            ExplicitCompression = $true
            ExplicitWorkers = $true
            ComparisonClass = $comparisonClass
        })
    }
}

foreach ($viperVariantConfig in $viperVariants) {
    Register-VariantMetadata -Tool $viperVariantConfig.Tool -Variant $viperVariantConfig.Name `
        -Implementation $viperVariantConfig.Implementation `
        -CompressionLevel $viperVariantConfig.Compression `
        -WorkersRequested $viperVariantConfig.Workers `
        -WorkersMode $(if ($viperVariantConfig.Workers -eq 0) { "automatic" } else { "explicit" }) `
        -WorkersEffectiveEstimate $(if ($viperVariantConfig.Workers -eq 0) { $automaticWorkersEstimate } else { $viperVariantConfig.Workers }) `
        -ComparisonClass $viperVariantConfig.ComparisonClass
}
Register-VariantMetadata -Tool "HDiffPatch" -Variant "WD_s64_reference" -Implementation "native_file_or_directory" -ComparisonClass "reference"
Register-VariantMetadata -Tool "xdelta3" -Variant "single_default" -Implementation "single_file" -ComparisonClass "reference"
Register-VariantMetadata -Tool "xdelta3" -Variant "sequential_default" -Implementation "sequential_per_file" -ComparisonClass "reference"
Register-VariantMetadata -Tool "Floating IPS" -Variant "single_ips_exact" -Implementation "single_file" -ComparisonClass "reference"
Register-VariantMetadata -Tool "Floating IPS" -Variant "sequential_ips_exact" -Implementation "sequential_per_file" -ComparisonClass "reference"

foreach ($case in $cases) {
    $iterations = Get-Iterations $case
    $pairs = Get-CaseFiles $case
    Write-Host ""
    Write-Host "=== $($case.Name): $($case.FileCount) file(s), $($case.BytesPerFile) bytes/file ==="

    foreach ($viperVariantConfig in $viperVariants) {
        # Copy values to stable local names because invoked scriptblocks use
        # PowerShell dynamic scope.
        $viperToolName = [string]$viperVariantConfig.Tool
        $viperVariantName = [string]$viperVariantConfig.Name
        $viperCreatorPath = [string]$viperVariantConfig.Creator
        $viperPatcherPath = [string]$viperVariantConfig.Patcher
        $viperUseHeadless = [bool]$viperVariantConfig.UseHeadless
        $viperCompressionLevel = [int]$viperVariantConfig.Compression
        $viperWorkers = [int]$viperVariantConfig.Workers
        $viperExplicitCompression = [bool]$viperVariantConfig.ExplicitCompression
        $viperExplicitWorkers = [bool]$viperVariantConfig.ExplicitWorkers

        Write-Host "$viperToolName $viperVariantName"
        $variantRoot = Join-Path $patchRoot (Join-Path "viper" (Join-Path $case.Name $viperVariantName))
        $patchFile = Join-Path $variantRoot "update.vipr"
        New-Item -ItemType Directory -Force -Path $variantRoot | Out-Null

        $prepareCreate = {
            if (Test-Path -LiteralPath $patchFile) { Remove-Item -LiteralPath $patchFile -Force }
        }
        $buildCreate = {
            $arguments = [Collections.Generic.List[string]]::new()
            if ($viperUseHeadless) { $arguments.Add("--headless") }
            if ($viperExplicitCompression) {
                $arguments.Add("--compression-level")
                $arguments.Add([string]$viperCompressionLevel)
            }
            if ($viperExplicitWorkers) {
                $arguments.Add("--workers")
                $arguments.Add([string]$viperWorkers)
            }
            foreach ($pair in $pairs) {
                $arguments.Add("--file-pair")
                $arguments.Add("$($pair.Old)::$($pair.New)")
            }
            $arguments.Add($patchFile)
            [pscustomobject]@{ File = $viperCreatorPath; Arguments = @($arguments) }
        }
        $getPatchBytes = { ViprBench-GetDirectoryBytes $patchFile }

        $creationValidationDir = Join-Path $workRoot (Join-Path "creation-validation" (Join-Path "viper" (Join-Path $case.Name $viperVariantName)))
        $prepareViperCreationValidation = { ViprBench-CopyDirectoryContents $case.OldDir $creationValidationDir }
        $buildViperCreationValidation = {
            $arguments = [Collections.Generic.List[string]]::new()
            if ($viperUseHeadless) { $arguments.Add("--headless") }
            $arguments.Add("--patch-file")
            $arguments.Add($patchFile)
            if ($viperExplicitWorkers) {
                $arguments.Add("--workers")
                $arguments.Add([string]$viperWorkers)
            }
            $arguments.Add($creationValidationDir)
            [pscustomobject]@{ File = $viperPatcherPath; Arguments = @($arguments) }
        }
        $verifyViperCreationValidation = { ViprBench-VerifyDirectoryHashes $creationValidationDir $case.ExpectedHashes }
        $validateViperPatch = {
            Invoke-UnmeasuredPatchValidation -Prepare $prepareViperCreationValidation `
                -BuildCommands $buildViperCreationValidation -Verify $verifyViperCreationValidation
        }

        $created = Measure-Creation -Tool $viperToolName -Version $RequiredViperVersion -Case $case `
            -Variant $viperVariantName -Iterations $iterations -Prepare $prepareCreate `
            -BuildCommands $buildCreate -GetPatchBytes $getPatchBytes -ValidatePatch $validateViperPatch

        if ($created) {
            $patchBytes = ViprBench-GetDirectoryBytes $patchFile
            $applyDir = Join-Path $workRoot (Join-Path "viper" (Join-Path $case.Name $viperVariantName))
            $prepareApply = { ViprBench-CopyDirectoryContents $case.OldDir $applyDir }
            $buildApply = {
                $arguments = [Collections.Generic.List[string]]::new()
                if ($viperUseHeadless) { $arguments.Add("--headless") }
                $arguments.Add("--patch-file")
                $arguments.Add($patchFile)
                if ($viperExplicitWorkers) {
                    $arguments.Add("--workers")
                    $arguments.Add([string]$viperWorkers)
                }
                $arguments.Add($applyDir)
                [pscustomobject]@{ File = $viperPatcherPath; Arguments = @($arguments) }
            }
            $verifyApply = { ViprBench-VerifyDirectoryHashes $applyDir $case.ExpectedHashes }
            Measure-Application -Tool $viperToolName -Version $RequiredViperVersion -Case $case `
                -Variant $viperVariantName -Iterations $iterations -PatchBytes $patchBytes `
                -Prepare $prepareApply -BuildCommands $buildApply -Verify $verifyApply
        } else {
            Add-SkippedResult -Tool $viperToolName -Version $RequiredViperVersion -Case $case `
                -Phase "apply" -Variant $viperVariantName -Status "not_created"
        }
    }

    Write-Host "HDiffPatch directory/file mode (-WD -s-64)"
    $hdiffVariant = "WD_s64_reference"
    $hdiffRoot = Join-Path $patchRoot (Join-Path "hdiffpatch" $case.Name)
    $hdiffPatch = Join-Path $hdiffRoot "update.hdiff"
    New-Item -ItemType Directory -Force -Path $hdiffRoot | Out-Null
    $oldInput = if ($case.FileCount -eq 1) { $pairs[0].Old } else { $case.OldDir }
    $newInput = if ($case.FileCount -eq 1) { $pairs[0].New } else { $case.NewDir }
    $prepareHdiffCreate = {
        if (Test-Path -LiteralPath $hdiffPatch) { Remove-Item -LiteralPath $hdiffPatch -Force }
    }
    $buildHdiffCreate = {
        [pscustomobject]@{
            File = $tools.hdiff
            Arguments = @("-WD", "-s-64", $oldInput, $newInput, $hdiffPatch)
        }
    }
    $getHdiffBytes = { ViprBench-GetDirectoryBytes $hdiffPatch }
    $hdiffValidationRoot = Join-Path $workRoot (Join-Path "creation-validation" (Join-Path "hdiffpatch" $case.Name))
    if ($case.FileCount -eq 1) {
        $hdiffValidationOutput = Join-Path $hdiffValidationRoot $pairs[0].Name
        $prepareHdiffValidation = {
            if (Test-Path -LiteralPath $hdiffValidationRoot) { Remove-Item -LiteralPath $hdiffValidationRoot -Recurse -Force }
            New-Item -ItemType Directory -Force -Path $hdiffValidationRoot | Out-Null
        }
        $buildHdiffValidation = {
            [pscustomobject]@{ File = $tools.hpatch; Arguments = @($pairs[0].Old, $hdiffPatch, $hdiffValidationOutput) }
        }
        $verifyHdiffValidation = { ViprBench-VerifyDirectoryHashes $hdiffValidationRoot $case.ExpectedHashes }
    } else {
        $hdiffValidationOutput = Join-Path $hdiffValidationRoot "output"
        $prepareHdiffValidation = {
            if (Test-Path -LiteralPath $hdiffValidationRoot) { Remove-Item -LiteralPath $hdiffValidationRoot -Recurse -Force }
            New-Item -ItemType Directory -Force -Path $hdiffValidationRoot | Out-Null
        }
        $buildHdiffValidation = {
            [pscustomobject]@{ File = $tools.hpatch; Arguments = @($case.OldDir, $hdiffPatch, $hdiffValidationOutput) }
        }
        $verifyHdiffValidation = { ViprBench-VerifyDirectoryHashes $hdiffValidationOutput $case.ExpectedHashes }
    }
    $validateHdiffPatch = {
        Invoke-UnmeasuredPatchValidation -Prepare $prepareHdiffValidation `
            -BuildCommands $buildHdiffValidation -Verify $verifyHdiffValidation
    }
    $hdiffCreated = Measure-Creation -Tool "HDiffPatch" -Version $RequiredHDiffVersion -Case $case `
        -Variant $hdiffVariant -Iterations $iterations -Prepare $prepareHdiffCreate `
        -BuildCommands $buildHdiffCreate -GetPatchBytes $getHdiffBytes -ValidatePatch $validateHdiffPatch
    if ($hdiffCreated) {
        $hdiffBytes = ViprBench-GetDirectoryBytes $hdiffPatch
        $hdiffApplyRoot = Join-Path $workRoot (Join-Path "hdiffpatch" $case.Name)
        if ($case.FileCount -eq 1) {
            $hdiffOutput = Join-Path $hdiffApplyRoot $pairs[0].Name
            $prepareHdiffApply = {
                if (Test-Path -LiteralPath $hdiffApplyRoot) { Remove-Item -LiteralPath $hdiffApplyRoot -Recurse -Force }
                New-Item -ItemType Directory -Force -Path $hdiffApplyRoot | Out-Null
            }
            $buildHdiffApply = {
                [pscustomobject]@{ File = $tools.hpatch; Arguments = @($pairs[0].Old, $hdiffPatch, $hdiffOutput) }
            }
            $verifyHdiff = { ViprBench-VerifyDirectoryHashes $hdiffApplyRoot $case.ExpectedHashes }
        } else {
            $hdiffOutput = Join-Path $hdiffApplyRoot "output"
            $prepareHdiffApply = {
                if (Test-Path -LiteralPath $hdiffApplyRoot) { Remove-Item -LiteralPath $hdiffApplyRoot -Recurse -Force }
                New-Item -ItemType Directory -Force -Path $hdiffApplyRoot | Out-Null
            }
            $buildHdiffApply = {
                [pscustomobject]@{ File = $tools.hpatch; Arguments = @($case.OldDir, $hdiffPatch, $hdiffOutput) }
            }
            $verifyHdiff = { ViprBench-VerifyDirectoryHashes $hdiffOutput $case.ExpectedHashes }
        }
        Measure-Application -Tool "HDiffPatch" -Version $RequiredHDiffVersion -Case $case `
            -Variant $hdiffVariant -Iterations $iterations -PatchBytes $hdiffBytes `
            -Prepare $prepareHdiffApply -BuildCommands $buildHdiffApply -Verify $verifyHdiff
    } else {
        Add-SkippedResult -Tool "HDiffPatch" -Version $RequiredHDiffVersion -Case $case `
            -Phase "apply" -Variant $hdiffVariant -Status "not_created"
    }

    Write-Host "xdelta3 sequential VCDIFF"
    $xdeltaVariant = if ($case.FileCount -eq 1) { "single_default" } else { "sequential_default" }
    $xdeltaPatchDir = Join-Path $patchRoot (Join-Path "xdelta" $case.Name)
    $prepareXdeltaCreate = {
        if (Test-Path -LiteralPath $xdeltaPatchDir) { Remove-Item -LiteralPath $xdeltaPatchDir -Recurse -Force }
        New-Item -ItemType Directory -Force -Path $xdeltaPatchDir | Out-Null
    }
    $buildXdeltaCreate = {
        foreach ($pair in $pairs) {
            $patch = Join-Path $xdeltaPatchDir ($pair.Name + ".xdelta")
            [pscustomobject]@{ File = $tools.xdelta; Arguments = @("-e", "-s", $pair.Old, $pair.New, $patch) }
        }
    }
    $getXdeltaBytes = { ViprBench-GetDirectoryBytes $xdeltaPatchDir }
    $xdeltaValidationOutput = Join-Path $workRoot (Join-Path "creation-validation" (Join-Path "xdelta" $case.Name))
    $prepareXdeltaValidation = {
        if (Test-Path -LiteralPath $xdeltaValidationOutput) { Remove-Item -LiteralPath $xdeltaValidationOutput -Recurse -Force }
        New-Item -ItemType Directory -Force -Path $xdeltaValidationOutput | Out-Null
    }
    $buildXdeltaValidation = {
        foreach ($pair in $pairs) {
            $patch = Join-Path $xdeltaPatchDir ($pair.Name + ".xdelta")
            $output = Join-Path $xdeltaValidationOutput $pair.Name
            [pscustomobject]@{ File = $tools.xdelta; Arguments = @("-d", "-s", $pair.Old, $patch, $output) }
        }
    }
    $verifyXdeltaValidation = { ViprBench-VerifyDirectoryHashes $xdeltaValidationOutput $case.ExpectedHashes }
    $validateXdeltaPatch = {
        Invoke-UnmeasuredPatchValidation -Prepare $prepareXdeltaValidation `
            -BuildCommands $buildXdeltaValidation -Verify $verifyXdeltaValidation
    }
    $xdeltaCreated = Measure-Creation -Tool "xdelta3" -Version "3.2.0" -Case $case `
        -Variant $xdeltaVariant -Iterations $iterations -Prepare $prepareXdeltaCreate `
        -BuildCommands $buildXdeltaCreate -GetPatchBytes $getXdeltaBytes -ValidatePatch $validateXdeltaPatch
    if ($xdeltaCreated) {
        $xdeltaBytes = ViprBench-GetDirectoryBytes $xdeltaPatchDir
        $xdeltaOutput = Join-Path $workRoot (Join-Path "xdelta" $case.Name)
        $prepareXdeltaApply = {
            if (Test-Path -LiteralPath $xdeltaOutput) { Remove-Item -LiteralPath $xdeltaOutput -Recurse -Force }
            New-Item -ItemType Directory -Force -Path $xdeltaOutput | Out-Null
        }
        $buildXdeltaApply = {
            foreach ($pair in $pairs) {
                $patch = Join-Path $xdeltaPatchDir ($pair.Name + ".xdelta")
                $output = Join-Path $xdeltaOutput $pair.Name
                [pscustomobject]@{ File = $tools.xdelta; Arguments = @("-d", "-s", $pair.Old, $patch, $output) }
            }
        }
        $verifyXdelta = { ViprBench-VerifyDirectoryHashes $xdeltaOutput $case.ExpectedHashes }
        Measure-Application -Tool "xdelta3" -Version "3.2.0" -Case $case `
            -Variant $xdeltaVariant -Iterations $iterations -PatchBytes $xdeltaBytes `
            -Prepare $prepareXdeltaApply -BuildCommands $buildXdeltaApply -Verify $verifyXdelta
    } else {
        Add-SkippedResult -Tool "xdelta3" -Version "3.2.0" -Case $case `
            -Phase "apply" -Variant $xdeltaVariant -Status "not_created"
    }

    Write-Host "Floating IPS sequential IPS"
    $flipsVariant = if ($case.FileCount -eq 1) { "single_ips_exact" } else { "sequential_ips_exact" }
    $flipsPatchDir = Join-Path $patchRoot (Join-Path "flips" $case.Name)
    $prepareFlipsCreate = {
        if (Test-Path -LiteralPath $flipsPatchDir) { Remove-Item -LiteralPath $flipsPatchDir -Recurse -Force }
        New-Item -ItemType Directory -Force -Path $flipsPatchDir | Out-Null
    }
    $buildFlipsCreate = {
        foreach ($pair in $pairs) {
            $patch = Join-Path $flipsPatchDir ($pair.Name + ".ips")
            [pscustomobject]@{ File = $tools.flips; Arguments = @("--create", "--ips", "--exact", $pair.Old, $pair.New, $patch) }
        }
    }
    $getFlipsBytes = { ViprBench-GetDirectoryBytes $flipsPatchDir }
    $flipsValidationOutput = Join-Path $workRoot (Join-Path "creation-validation" (Join-Path "flips" $case.Name))
    $prepareFlipsValidation = {
        if (Test-Path -LiteralPath $flipsValidationOutput) { Remove-Item -LiteralPath $flipsValidationOutput -Recurse -Force }
        New-Item -ItemType Directory -Force -Path $flipsValidationOutput | Out-Null
    }
    $buildFlipsValidation = {
        foreach ($pair in $pairs) {
            $patch = Join-Path $flipsPatchDir ($pair.Name + ".ips")
            $output = Join-Path $flipsValidationOutput $pair.Name
            [pscustomobject]@{ File = $tools.flips; Arguments = @("--apply", "--exact", $patch, $pair.Old, $output) }
        }
    }
    $verifyFlipsValidation = { ViprBench-VerifyDirectoryHashes $flipsValidationOutput $case.ExpectedHashes }
    $validateFlipsPatch = {
        Invoke-UnmeasuredPatchValidation -Prepare $prepareFlipsValidation `
            -BuildCommands $buildFlipsValidation -Verify $verifyFlipsValidation
    }

    $ipsKnownUnsupported = $case.BytesPerFile -gt 16777216L
    $flipsIterations = if ($ipsKnownUnsupported) { 1 } else { $iterations }
    $flipsCreated = Measure-Creation -Tool "Floating IPS" -Version "198" -Case $case `
        -Variant $flipsVariant -Iterations $flipsIterations -Prepare $prepareFlipsCreate `
        -BuildCommands $buildFlipsCreate -GetPatchBytes $getFlipsBytes `
        -ValidatePatch $validateFlipsPatch -DoWarmup (-not $ipsKnownUnsupported) `
        -KnownUnsupportedStatus $(if ($ipsKnownUnsupported) { "unsupported_ips_over_16MiB" } else { "" })

    if ($flipsCreated) {
        $flipsBytes = ViprBench-GetDirectoryBytes $flipsPatchDir
        $flipsOutput = Join-Path $workRoot (Join-Path "flips" $case.Name)
        $prepareFlipsApply = {
            if (Test-Path -LiteralPath $flipsOutput) { Remove-Item -LiteralPath $flipsOutput -Recurse -Force }
            New-Item -ItemType Directory -Force -Path $flipsOutput | Out-Null
        }
        $buildFlipsApply = {
            foreach ($pair in $pairs) {
                $patch = Join-Path $flipsPatchDir ($pair.Name + ".ips")
                $output = Join-Path $flipsOutput $pair.Name
                [pscustomobject]@{ File = $tools.flips; Arguments = @("--apply", "--exact", $patch, $pair.Old, $output) }
            }
        }
        $verifyFlips = { ViprBench-VerifyDirectoryHashes $flipsOutput $case.ExpectedHashes }
        Measure-Application -Tool "Floating IPS" -Version "198" -Case $case `
            -Variant $flipsVariant -Iterations $iterations -PatchBytes $flipsBytes `
            -Prepare $prepareFlipsApply -BuildCommands $buildFlipsApply -Verify $verifyFlips
    } else {
        $skipStatus = if ($ipsKnownUnsupported) { "unsupported_ips_over_16MiB" } else { "not_created" }
        Add-SkippedResult -Tool "Floating IPS" -Version "198" -Case $case `
            -Phase "apply" -Variant $flipsVariant -Status $skipStatus
    }
}

function Get-Percentile {
    param([double[]] $Values, [double] $Percentile)

    [double[]]$normalizedValues = @($Values)
    if (@($normalizedValues).Count -eq 0) { return $null }

    [double[]]$sorted = @($normalizedValues | Sort-Object)
    if (@($sorted).Count -eq 1) { return [double]$sorted[0] }

    $position = (@($sorted).Count - 1) * $Percentile
    $lower = [Math]::Floor($position)
    $upper = [Math]::Ceiling($position)
    if ($lower -eq $upper) { return [double]$sorted[$lower] }
    $weight = $position - $lower
    return [double]$sorted[$lower] * (1.0 - $weight) + [double]$sorted[$upper] * $weight
}

Write-Host "Building summary.csv..."
$summaryRows = [Collections.Generic.List[object]]::new()
foreach ($group in ($script:Results | Group-Object tool, version, case, phase, variant)) {
    $rows = @($group.Group)
    $success = @($rows | Where-Object { $_.status -eq "success" })
    $allRowsSucceeded = ($rows.Count -gt 0 -and $success.Count -eq $rows.Count)
    $knownUnsupported = @($rows | Where-Object { $_.status -like "unsupported*" }).Count -gt 0
    $notMeasured = @($rows | Where-Object { $_.status -eq "not_created" }).Count -gt 0
    $strictFailure = (-not $allRowsSucceeded -and -not $knownUnsupported -and -not $notMeasured)

    # A group only publishes timings when every requested repetition succeeded.
    # Creation success includes an out-of-timer apply+SHA-256 validation; apply
    # success includes an out-of-timer SHA-256 validation.
    [double[]]$walls = @()
    if ($allRowsSucceeded) {
        $walls = [double[]]@($success | ForEach-Object { [double]$_.wall_ms })
    }

    [long[]]$patchSizes = @()
    if ($rows[0].phase -eq "create" -and $allRowsSucceeded) {
        $patchSizes = [long[]]@($success | ForEach-Object { [long]$_.patch_bytes })
    }

    $wallCount = @($walls).Count
    $patchSizeCount = @($patchSizes).Count
    $medianWall = if ($wallCount -gt 0) { [Math]::Round((Get-Percentile $walls 0.5), 3) } else { $null }
    $medianPatchBytes = if ($patchSizeCount -gt 0) { [long](Get-Percentile ([double[]]$patchSizes) 0.5) } else { $null }
    $timeResult = if ($allRowsSucceeded) {
        "$medianWall ms"
    } elseif ($knownUnsupported) {
        "unsupported"
    } elseif ($notMeasured) {
        "not measured"
    } else {
        "failed"
    }

    $summaryStatus = if ($allRowsSucceeded) {
        "success"
    } elseif ($knownUnsupported) {
        ($rows | Where-Object { $_.status -like "unsupported*" } | Select-Object -First 1).status
    } elseif ($notMeasured) {
        "not_created"
    } else {
        "failed"
    }

    $verificationStatuses = @($rows | ForEach-Object { $_.sha256_verification } | Sort-Object -Unique)
    $patchValidationStatuses = @($rows | ForEach-Object { $_.patch_validation } | Sort-Object -Unique)
    $summaryRows.Add([pscustomobject]@{
        tool = $rows[0].tool
        version = $rows[0].version
        implementation = $rows[0].implementation
        comparison_class = $rows[0].comparison_class
        compression_level = $rows[0].compression_level
        workers_requested = $rows[0].workers_requested
        workers_mode = $rows[0].workers_mode
        workers_effective_estimate = $rows[0].workers_effective_estimate
        case = $rows[0].case
        phase = $rows[0].phase
        variant = $rows[0].variant
        measured_rows = $rows.Count
        successful_rows = $success.Count
        result = $timeResult
        time_result = $timeResult
        patch_size_result = if ($null -ne $medianPatchBytes) { "$medianPatchBytes bytes" } elseif ($rows[0].phase -eq "create" -and -not $allRowsSucceeded) { "unavailable" } else { "not applicable" }
        status = $summaryStatus
        median_wall_ms = $medianWall
        min_wall_ms = if ($wallCount -gt 0) { [Math]::Round(($walls | Measure-Object -Minimum).Minimum, 3) } else { $null }
        max_wall_ms = if ($wallCount -gt 0) { [Math]::Round(($walls | Measure-Object -Maximum).Maximum, 3) } else { $null }
        p95_wall_ms = if ($wallCount -gt 0) { [Math]::Round((Get-Percentile $walls 0.95), 3) } else { $null }
        patch_size_bytes = $medianPatchBytes
        patch_size_mib = if ($null -ne $medianPatchBytes) { [Math]::Round(([double]$medianPatchBytes / 1MB), 6) } else { $null }
        min_patch_bytes = if ($patchSizeCount -gt 0) { [long](($patchSizes | Measure-Object -Minimum).Minimum) } else { $null }
        median_patch_bytes = $medianPatchBytes
        max_patch_bytes = if ($patchSizeCount -gt 0) { [long](($patchSizes | Measure-Object -Maximum).Maximum) } else { $null }
        patch_validation = if ($rows[0].phase -ne "create") {
            "not_applicable"
        } elseif ($allRowsSucceeded -and @($patchValidationStatuses).Count -eq 1 -and $patchValidationStatuses[0] -eq "passed") {
            "passed"
        } elseif ($knownUnsupported -or $notMeasured) {
            "not_run"
        } else {
            "failed"
        }
        sha256_verification = if ($rows[0].phase -ne "apply") {
            "not_applicable"
        } elseif ($allRowsSucceeded -and @($verificationStatuses).Count -eq 1 -and $verificationStatuses[0] -eq "passed") {
            "passed"
        } elseif ($knownUnsupported -or $notMeasured) {
            "not_run"
        } else {
            "failed"
        }
        failure_statuses = if ($strictFailure) {
            (@($rows | Where-Object { $_.status -ne "success" } | ForEach-Object { $_.status } | Sort-Object -Unique) -join ";")
        } else {
            ""
        }
    })
}
$summaryRows | Export-Csv -LiteralPath $summaryPath -NoTypeInformation -Encoding utf8
Write-Host "Summary completed: $summaryPath"

# This view avoids presenting Viper's tuned CLI profiles as its only result. It
# retains the hybrid defaults, the CLI-only default-equivalent profile, and the
# designated reference profile of every competitor. Full tuning data remains in
# summary.csv and results.csv.
$fairComparisonRows = @($summaryRows | Where-Object {
    $_.comparison_class -in @("default_reference", "default_equivalent", "reference")
})
$fairComparisonRows | Export-Csv -LiteralPath $fairComparisonPath -NoTypeInformation -Encoding utf8
Write-Host "Reference comparison completed: $fairComparisonPath"

$reportBuilderScript = Join-Path $PSScriptRoot "build-report.ps1"
if (-not (Test-Path -LiteralPath $reportBuilderScript -PathType Leaf)) {
    throw "Missing report builder: $reportBuilderScript"
}
& $reportBuilderScript -RunDirectory $runRoot

if (-not $KeepDatasets) {
    Remove-Item -LiteralPath $dataRoot -Recurse -Force
    Remove-Item -LiteralPath $workRoot -Recurse -Force
}

Write-Host ""
Write-Host "Benchmark complete."
Write-Host "Raw results: $resultsPath"
Write-Host "Summary:     $summaryPath"
Write-Host "Reference:   $fairComparisonPath"
Write-Host "Report:      $reportPath"
Write-Host "README text: $readmeSnippetPath"
Write-Host "System info: $systemPath"
Write-Host "Expected SHA-256 hashes: $expectedHashesPath"
