[CmdletBinding()]
param()

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$failed = $false

Push-Location (Join-Path $repoRoot 'backend')
try {
    & go test ./...
    if ($LASTEXITCODE -ne 0) { $failed = $true }

    & go vet ./...
    if ($LASTEXITCODE -ne 0) { $failed = $true }
} finally {
    Pop-Location
}

Push-Location (Join-Path $repoRoot 'frontend')
try {
    & npm.cmd run check
    if ($LASTEXITCODE -ne 0) { $failed = $true }
} finally {
    Pop-Location
}

if ($failed) {
    throw 'One or more checks failed.'
}

Write-Host 'All checks passed.'

