[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$RunDirectory
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ($PSVersionTable.PSVersion.Major -lt 7) {
    throw "PowerShell 7 or newer is required. Run this script with pwsh.exe."
}

$runRoot = [IO.Path]::GetFullPath($RunDirectory)
if (-not (Test-Path -LiteralPath $runRoot -PathType Container)) {
    throw "Run directory does not exist: $runRoot"
}

$summaryPath = Join-Path $runRoot "summary.csv"
$fairComparisonPath = Join-Path $runRoot "fair-comparison.csv"
$reportPath = Join-Path $runRoot "report.md"
$readmeSnippetPath = Join-Path $runRoot "README-snippet.md"
$systemPath = Join-Path $runRoot "system.json"
$toolsLockPath = Join-Path $runRoot "tools-lock.json"
$runCommandPath = Join-Path $runRoot "run-command.txt"

foreach ($requiredPath in @($summaryPath, $fairComparisonPath)) {
    if (-not (Test-Path -LiteralPath $requiredPath -PathType Leaf)) {
        throw "Required benchmark output is missing: $requiredPath"
    }
}

$summaryRows = @(Import-Csv -LiteralPath $summaryPath)
$fairComparisonRows = @(Import-Csv -LiteralPath $fairComparisonPath)
if ($summaryRows.Count -eq 0) {
    throw "summary.csv contains no rows: $summaryPath"
}
if ($fairComparisonRows.Count -eq 0) {
    throw "fair-comparison.csv contains no rows: $fairComparisonPath"
}

function Get-OptionalJson {
    param([Parameter(Mandatory)] [string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return $null }
    return Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json
}

function Get-PropertyValue {
    param(
        [AllowNull()] $Object,
        [Parameter(Mandatory)] [string]$Name
    )
    if ($null -eq $Object) { return $null }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return $null }
    return $property.Value
}

function Get-FirstNonBlankValue {
    param([object[]]$Values)
    foreach ($value in $Values) {
        if ($null -ne $value -and -not [string]::IsNullOrWhiteSpace([string]$value)) {
            return [string]$value
        }
    }
    return $null
}

$systemInfo = Get-OptionalJson -Path $systemPath
$toolsLock = Get-OptionalJson -Path $toolsLockPath
$viperSource = Get-PropertyValue -Object $systemInfo -Name "viper_source"
$viperLock = Get-PropertyValue -Object $toolsLock -Name "viper"
$viperRow = @($summaryRows | Where-Object { $_.tool -like "Viper-Patcher*" } | Select-Object -First 1)
$viperVersion = Get-FirstNonBlankValue -Values @(
    (Get-PropertyValue -Object $viperSource -Name "version"),
    (Get-PropertyValue -Object $viperLock -Name "version"),
    $(if ($viperRow.Count -gt 0) { $viperRow[0].version } else { $null })
)
$viperCommit = Get-FirstNonBlankValue -Values @(
    (Get-PropertyValue -Object $viperSource -Name "commit"),
    (Get-PropertyValue -Object $viperLock -Name "commit")
)

$cpuName = Get-FirstNonBlankValue -Values @((Get-PropertyValue -Object $systemInfo -Name "cpu"), "unknown CPU")
$logicalProcessors = Get-PropertyValue -Object $systemInfo -Name "logical_processors"
$workerInfo = Get-PropertyValue -Object $systemInfo -Name "viper_automatic_workers"
$automaticWorkers = Get-PropertyValue -Object $workerInfo -Name "effective_estimate"
if ($null -eq $automaticWorkers) {
    $workerRow = @($summaryRows | Where-Object {
        $_.tool -like "Viper-Patcher*" -and
        $_.workers_mode -eq "automatic" -and
        -not [string]::IsNullOrWhiteSpace([string]$_.workers_effective_estimate)
    } | Select-Object -First 1)
    if ($workerRow.Count -gt 0) { $automaticWorkers = $workerRow[0].workers_effective_estimate }
}

$memoryBytes = Get-PropertyValue -Object $systemInfo -Name "physical_memory_bytes"
$osName = Get-FirstNonBlankValue -Values @((Get-PropertyValue -Object $systemInfo -Name "os"), "unknown Windows version")
$osVersion = Get-PropertyValue -Object $systemInfo -Name "os_version"
$iterationInfo = Get-PropertyValue -Object $systemInfo -Name "iterations"
$smallIterations = Get-PropertyValue -Object $iterationInfo -Name "small"
$largeIterations = Get-PropertyValue -Object $iterationInfo -Name "large"
$hugeIterations = Get-PropertyValue -Object $iterationInfo -Name "huge"
$warmup = Get-PropertyValue -Object $iterationInfo -Name "warmup"
$canonicalCommand = if (Test-Path -LiteralPath $runCommandPath -PathType Leaf) {
    (Get-Content -LiteralPath $runCommandPath -Raw).Trim()
} else {
    Get-PropertyValue -Object $systemInfo -Name "command"
}

$invariantCulture = [Globalization.CultureInfo]::InvariantCulture

function ConvertTo-MarkdownCell {
    param([AllowNull()] $Value)
    if ($null -eq $Value) { return "" }
    return ([string]$Value).Replace("|", "\|").Replace("`r", " ").Replace("`n", " ")
}

function Format-BenchmarkTime {
    param([AllowNull()] $Row)
    if ($null -eq $Row) { return "not measured" }
    if ($Row.status -ne "success") { return ConvertTo-MarkdownCell $Row.status }
    return [string]::Format($invariantCulture, "{0:F3} ms", [double]$Row.median_wall_ms)
}

function Format-PatchSize {
    param([AllowNull()] $CreateRow)
    if ($null -eq $CreateRow) { return "not measured" }
    if ($CreateRow.status -ne "success" -or [string]::IsNullOrWhiteSpace([string]$CreateRow.median_patch_bytes)) {
        return ConvertTo-MarkdownCell $CreateRow.status
    }
    $bytes = [long]$CreateRow.median_patch_bytes
    if ($bytes -ge 1MB) {
        return [string]::Format($invariantCulture, "{0:F3} MiB ({1:N0} bytes)", ($bytes / 1MB), $bytes)
    }
    if ($bytes -ge 1KB) {
        return [string]::Format($invariantCulture, "{0:F3} KiB ({1:N0} bytes)", ($bytes / 1KB), $bytes)
    }
    return [string]::Format($invariantCulture, "{0:N0} bytes", $bytes)
}

function Get-PivotRows {
    param([Parameter(Mandatory)] [object[]]$Rows)
    $pivot = [Collections.Generic.List[object]]::new()
    foreach ($group in ($Rows | Group-Object tool, version, case, variant)) {
        $createRow = @($group.Group | Where-Object { $_.phase -eq "create" } | Select-Object -First 1)
        $applyRow = @($group.Group | Where-Object { $_.phase -eq "apply" } | Select-Object -First 1)
        $first = $group.Group[0]
        $pivot.Add([pscustomobject]@{
            tool = $first.tool
            version = $first.version
            case = $first.case
            variant = $first.variant
            comparison_class = $first.comparison_class
            create = if ($createRow.Count -gt 0) { $createRow[0] } else { $null }
            apply = if ($applyRow.Count -gt 0) { $applyRow[0] } else { $null }
        })
    }
    return @($pivot)
}

function Add-ResultTables {
    param(
        [Parameter(Mandatory)] [Text.StringBuilder]$Builder,
        [Parameter(Mandatory)] [object[]]$Rows,
        [Parameter(Mandatory)] [string[]]$Cases
    )
    foreach ($caseName in $Cases) {
        $caseRows = @($Rows | Where-Object { $_.case -eq $caseName } | Sort-Object tool, variant)
        if ($caseRows.Count -eq 0) { continue }
        [void]$Builder.AppendLine("### ``$caseName``")
        [void]$Builder.AppendLine()
        [void]$Builder.AppendLine("| Tool | Profile | Create median | Apply median | Patch size |")
        [void]$Builder.AppendLine("|---|---|---:|---:|---:|")
        foreach ($row in $caseRows) {
            $toolLabel = "$(ConvertTo-MarkdownCell $row.tool) $(ConvertTo-MarkdownCell $row.version)"
            [void]$Builder.AppendLine("| $toolLabel | ``$(ConvertTo-MarkdownCell $row.variant)`` | $(Format-BenchmarkTime $row.create) | $(Format-BenchmarkTime $row.apply) | $(Format-PatchSize $row.create) |")
        }
        [void]$Builder.AppendLine()
    }
}

$preferredCaseOrder = @(
    "one_100KB_scattered",
    "one_100KB_unrelated",
    "ten_100KB_scattered",
    "ten_100KB_unrelated",
    "one_50MB_scattered",
    "one_50MB_unrelated",
    "one_500MB_scattered",
    "one_500MB_unrelated"
)
$availableCases = @($summaryRows | ForEach-Object { $_.case } | Sort-Object -Unique)
$orderedCases = [Collections.Generic.List[string]]::new()
foreach ($caseName in $preferredCaseOrder) {
    if ($availableCases -contains $caseName) { $orderedCases.Add($caseName) }
}
foreach ($caseName in @($availableCases | Where-Object { $preferredCaseOrder -notcontains $_ } | Sort-Object)) {
    $orderedCases.Add($caseName)
}

$referencePivot = Get-PivotRows -Rows $fairComparisonRows
$allPivot = Get-PivotRows -Rows $summaryRows
$viperTunedRows = @($allPivot | Where-Object { $_.tool -like "Viper-Patcher*" })

$report = [Text.StringBuilder]::new()
[void]$report.AppendLine("# Windows x64 benchmark report")
[void]$report.AppendLine()
[void]$report.AppendLine("Generated: $([DateTime]::UtcNow.ToString('yyyy-MM-dd HH:mm:ss')) UTC")
[void]$report.AppendLine()
if (-not [string]::IsNullOrWhiteSpace($viperVersion)) {
    $viperDescription = "- Viper-Patcher: ``$viperVersion``"
    if (-not [string]::IsNullOrWhiteSpace($viperCommit)) { $viperDescription += " at commit ``$viperCommit``" }
    [void]$report.AppendLine($viperDescription)
}
[void]$report.AppendLine("- CPU: $(ConvertTo-MarkdownCell $cpuName)")
if ($null -ne $logicalProcessors) {
    $processorDescription = "- Logical processors: $logicalProcessors"
    if ($null -ne $automaticWorkers) { $processorDescription += "; Viper automatic worker estimate: $automaticWorkers" }
    [void]$report.AppendLine($processorDescription)
} elseif ($null -ne $automaticWorkers) {
    [void]$report.AppendLine("- Viper automatic worker estimate: $automaticWorkers")
}
if ($null -ne $memoryBytes) {
    [void]$report.AppendLine([string]::Format($invariantCulture, "- Memory: {0:F2} GiB", ([double]$memoryBytes / 1GB)))
}
$osDescription = "- OS: $(ConvertTo-MarkdownCell $osName)"
if ($null -ne $osVersion -and -not [string]::IsNullOrWhiteSpace([string]$osVersion)) { $osDescription += " $(ConvertTo-MarkdownCell $osVersion)" }
[void]$report.AppendLine($osDescription)
if ($null -ne $smallIterations -or $null -ne $largeIterations -or $null -ne $hugeIterations) {
    [void]$report.AppendLine("- Iterations: small=$smallIterations, large=$largeIterations, huge=$hugeIterations; warm-up=$warmup")
}
if (-not [string]::IsNullOrWhiteSpace([string]$canonicalCommand)) {
    [void]$report.AppendLine("- Command: ``$canonicalCommand``")
}
[void]$report.AppendLine()
[void]$report.AppendLine("Every created patch was applied and SHA-256 checked outside the creation timer. Every measured application was SHA-256 checked after its timer stopped. A failed repetition prevents a timing median from being published for that group.")
[void]$report.AppendLine()
[void]$report.AppendLine("## Reference comparison")
[void]$report.AppendLine()
[void]$report.AppendLine("This view contains the Viper hybrid defaults, the CLI-only default-equivalent profile, and one documented reference profile per competitor. See ``summary.csv`` for every Viper tuning profile.")
[void]$report.AppendLine()
Add-ResultTables -Builder $report -Rows $referencePivot -Cases @($orderedCases)
[void]$report.AppendLine("## Viper tuning matrix")
[void]$report.AppendLine()
Add-ResultTables -Builder $report -Rows $viperTunedRows -Cases @($orderedCases)
[void]$report.AppendLine("## Files")
[void]$report.AppendLine()
[void]$report.AppendLine("- ``results.csv``: every repetition")
[void]$report.AppendLine("- ``summary.csv``: complete aggregated matrix")
[void]$report.AppendLine("- ``fair-comparison.csv``: reference-only view")
[void]$report.AppendLine("- ``system.json`` and ``tools-lock.json``: hardware, parameters, assets, architectures and hashes")
[void]$report.AppendLine("- ``expected-hashes.json``: expected dataset hashes")
[void]$report.AppendLine()
[void]$report.AppendLine("Synthetic inputs are deterministic high-entropy data. They do not replace benchmarks on real executables, archives, databases or game assets.")
Set-Content -LiteralPath $reportPath -Value $report.ToString().TrimEnd() -Encoding utf8

$highlightCases = @($orderedCases | Where-Object { $_ -like "one_50MB_*" -or $_ -like "one_500MB_*" })
$snippet = [Text.StringBuilder]::new()
[void]$snippet.AppendLine("## Windows x64 benchmark")
[void]$snippet.AppendLine()
$intro = "Viper-Patcher"
if (-not [string]::IsNullOrWhiteSpace($viperVersion)) { $intro += " ``$viperVersion``" }
$intro += " was compared with HDiffPatch, xdelta3 and Floating IPS"
if (-not [string]::IsNullOrWhiteSpace($cpuName) -and $cpuName -ne "unknown CPU") { $intro += " on $(ConvertTo-MarkdownCell $cpuName)" }
if ($null -ne $logicalProcessors) { $intro += " ($logicalProcessors logical processors)" }
$intro += ". Patch validation and SHA-256 verification were performed outside the measured intervals."
if ($null -ne $automaticWorkers) { $intro += " Automatic Viper workers resolved to an estimated $automaticWorkers workers on this machine." }
[void]$snippet.AppendLine($intro)
[void]$snippet.AppendLine()
if (-not [string]::IsNullOrWhiteSpace([string]$canonicalCommand)) {
    [void]$snippet.AppendLine("Command: ``$canonicalCommand``")
    [void]$snippet.AppendLine()
}
Add-ResultTables -Builder $snippet -Rows $referencePivot -Cases $highlightCases
[void]$snippet.AppendLine("The complete run also includes 100 KB single-file and ten-file cases, all Viper CLI compression/worker profiles, raw repetitions, exact executable hashes and full hardware metadata. Synthetic inputs are deterministic high-entropy data and should be complemented by real-world corpora.")
Set-Content -LiteralPath $readmeSnippetPath -Value $snippet.ToString().TrimEnd() -Encoding utf8

Write-Host "Markdown report completed: $reportPath"
Write-Host "README snippet completed: $readmeSnippetPath"
