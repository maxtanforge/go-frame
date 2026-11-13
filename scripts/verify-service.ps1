[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repoRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$workDir = Join-Path $tempRoot ('go-frame-service-' + [Guid]::NewGuid().ToString('N'))
$oldFlags, $oldProcs, $oldCache = $env:GOFLAGS, $env:GOMAXPROCS, $env:GOCACHE
$oldWork, $oldProxy, $oldSumDB = $env:GOWORK, $env:GOPROXY, $env:GOSUMDB
$oldNoProxy, $oldToolchain = $env:GONOPROXY, $env:GOTOOLCHAIN
$pushed = $false
try {
    $null = New-Item -ItemType Directory -Path $workDir
    Copy-Item -Path (Join-Path $repoRoot 'templates/service/*') -Destination $workDir -Recurse -Force
    Push-Location $workDir
    $pushed = $true
    # Verification only: the source template never receives a local replace.
    go mod edit "-replace=github.com/maxtanforge/go-frame=$repoRoot"
    if ($LASTEXITCODE -ne 0) { throw 'Could not apply verification-only replacement.' }
    # Warm the dependency graph (test deps of dependencies included) with the
    # default environment; every verification step below still runs offline.
    go mod tidy
    if ($LASTEXITCODE -ne 0) { throw 'Dependency graph warm-up failed.' }
    $env:GOFLAGS = '-p=1'
    $env:GOMAXPROCS = '2'
    $env:GOCACHE = Join-Path $tempRoot 'go-frame-enterprise-cache'
    $env:GOWORK = 'off'
    $env:GOPROXY = 'off'
    $env:GOSUMDB = 'off'
    $env:GONOPROXY = 'none'
    $env:GOTOOLCHAIN = 'local'
    go mod tidy
    if ($LASTEXITCODE -ne 0) { throw 'Service dependency resolution failed.' }
    go test ./... -count=1 -timeout=2m
    if ($LASTEXITCODE -ne 0) { throw 'Service tests failed.' }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Service vet failed.' }
    go build -buildvcs=false -o service.exe ./cmd/api
    if ($LASTEXITCODE -ne 0) { throw 'Service build failed.' }
    Write-Host 'Service verification passed using a temporary local replacement; remote module download was not verified.'
} finally {
    if ($pushed) { Pop-Location }
    $env:GOFLAGS, $env:GOMAXPROCS, $env:GOCACHE = $oldFlags, $oldProcs, $oldCache
    $env:GOWORK, $env:GOPROXY, $env:GOSUMDB = $oldWork, $oldProxy, $oldSumDB
    $env:GONOPROXY, $env:GOTOOLCHAIN = $oldNoProxy, $oldToolchain
    $resolved = [IO.Path]::GetFullPath($workDir)
    if ((Split-Path -Parent $resolved).TrimEnd('\') -ne $tempRoot.TrimEnd('\') -or (Split-Path -Leaf $resolved) -notmatch '^go-frame-service-[0-9a-f]{32}$') {
        throw 'Refusing cleanup outside the owned temporary service directory.'
    }
    if (Test-Path -LiteralPath $resolved) { Remove-Item -LiteralPath $resolved -Recurse -Force }
}
