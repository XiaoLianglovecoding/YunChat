[CmdletBinding()]
param(
    [switch]$InstallFrontend
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$goCache = Join-Path $projectRoot ".cache/go-build"
New-Item -ItemType Directory -Force -Path $goCache | Out-Null

Push-Location $projectRoot
try {
    docker compose config --quiet
}
finally {
    Pop-Location
}

Push-Location (Join-Path $projectRoot "backend")
try {
    $env:GOCACHE = $goCache
    go test ./...
}
finally {
    Pop-Location
}

Push-Location (Join-Path $projectRoot "frontend")
try {
    if ($InstallFrontend) {
        npm.cmd ci
    }
    npm.cmd run typecheck
    npm.cmd test
    npm.cmd run build
}
finally {
    Pop-Location
}
