[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    $null = Get-Command go -ErrorAction Stop
    & go run ./cmd/frame dev
    if ($LASTEXITCODE -ne 0) { throw "Development supervisor exited with code $LASTEXITCODE" }
} finally { Pop-Location }
