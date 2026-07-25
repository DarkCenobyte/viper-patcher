param(
    [string]$Destination = "dist/third-party-licenses/go-modules"
)

$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path $Destination | Out-Null

$Modules = go list -m -f '{{if .Dir}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' all
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}
foreach ($Line in $Modules) {
    if ([string]::IsNullOrWhiteSpace($Line)) {
        continue
    }
    $Parts = $Line -split '\|', 3
    if ($Parts.Count -ne 3 -or [string]::IsNullOrWhiteSpace($Parts[1])) {
        continue
    }
    $ModulePath = $Parts[0]
    $ModuleVersion = $Parts[1]
    $ModuleDirectory = $Parts[2]
    $SafeName = ("${ModulePath}_${ModuleVersion}" -replace '[^A-Za-z0-9._-]', '_')
    $ModuleDestination = Join-Path $Destination $SafeName
    $LicenseFiles = Get-ChildItem -Path $ModuleDirectory -File | Where-Object {
        $_.Name -match '^(LICENSE|COPYING|NOTICE)'
    }
    if (-not $LicenseFiles) {
        Write-Warning "No top-level license file found for $ModulePath $ModuleVersion."
        continue
    }
    New-Item -ItemType Directory -Force -Path $ModuleDestination | Out-Null
    foreach ($LicenseFile in $LicenseFiles) {
        Copy-Item $LicenseFile.FullName $ModuleDestination
    }
}
