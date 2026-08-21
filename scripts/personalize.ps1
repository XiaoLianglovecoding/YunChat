[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[A-Za-z0-9-]+$')]
    [string]$GitHubUser,

    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$Author
)

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$skipDirs = @('.git', 'node_modules', 'dist', 'data')
$extensions = @('.go', '.mod', '.sum', '.md', '.yml', '.yaml', '.json', '.ts', '.tsx', '.sql')

Get-ChildItem -LiteralPath $repoRoot -Recurse -File | Where-Object {
    $relative = $_.FullName.Substring($repoRoot.Length).TrimStart('\', '/')
    $segments = $relative -split '[\\/]'
    ($segments | Where-Object { $skipDirs -contains $_ }).Count -eq 0 -and
        ($extensions -contains $_.Extension -or $_.Name -eq 'LICENSE')
} | ForEach-Object {
    $content = [System.IO.File]::ReadAllText($_.FullName)
    $updated = $content.Replace('YOUR_GITHUB_USERNAME', $GitHubUser).Replace('YOUR_NAME', $Author)
    if ($updated -ne $content) {
        [System.IO.File]::WriteAllText($_.FullName, $updated, [System.Text.UTF8Encoding]::new($false))
        Write-Host "updated $($_.FullName.Substring($repoRoot.Length + 1))"
    }
}

Write-Host 'Personalization complete. Review git diff before committing.'

