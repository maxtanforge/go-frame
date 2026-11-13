[CmdletBinding()]
param(
    [switch]$Race,
    [switch]$Consumer,
    [switch]$Bench,
    [switch]$Changed,
    [switch]$Coverage,
    [switch]$Timing,
    [string]$BenchTime = '1s'
)

$ErrorActionPreference = 'Stop'
if ($Changed -and ($Consumer -or $Bench -or $Coverage)) {
    throw '-Changed is a smoke mode and cannot be combined with -Consumer, -Bench, or -Coverage.'
}
$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    $null = Get-Command go, gofmt, git -ErrorAction Stop
    $goFiles = @(git ls-files --cached --others --exclude-standard -- '*.go')
    if ($LASTEXITCODE -ne 0) { throw 'Could not list Go source files.' }
    # A tracked file removed in the working tree is still reported by ls-files.
    $goFiles = @($goFiles | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf })
    if ($goFiles.Count -gt 0) {
        $unformatted = @(gofmt -l @goFiles)
        if ($LASTEXITCODE -ne 0) { throw 'gofmt failed.' }
        if ($unformatted.Count -gt 0) { throw "Run gofmt on:`n$($unformatted -join "`n")" }
    }
    if ($Race) {
        $cgo = go env CGO_ENABLED
        if ($LASTEXITCODE -ne 0 -or $cgo -ne '1') {
            throw 'Race verification requires CGO_ENABLED=1 and a supported C compiler. No settings were changed.'
        }
    }
    $testArgs = @('test', './...', '-count=1', '-timeout=5m')
    if ($Changed -or $Coverage -or $Timing) {
        $testArgs = @('run', './scripts/verify-test')
        if ($Changed) { $testArgs += '--changed' }
        if ($Coverage) { $testArgs += '--coverage' }
        if ($Timing) { $testArgs += '--timing' }
    }
    if ($Race) { $testArgs += '-race' }
    & go @testArgs
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed.' }
    if (-not $Changed) {
        # Pinned golangci-lint: no local install required, first run compiles via the module proxy.
        & go run 'github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0' run --allow-serial-runners
        if ($LASTEXITCODE -ne 0) { throw 'golangci-lint failed.' }
    }
    go build -buildvcs=false ./...
    if ($LASTEXITCODE -ne 0) { throw 'go build failed.' }
    if ($Bench) {
        go test ./concurrency ./telemetry/logger -run '^$' -bench . -benchmem "-benchtime=$BenchTime"
        if ($LASTEXITCODE -ne 0) { throw 'Benchmarks failed.' }
    }
    if ($Consumer) {
        $consumerArgs = @('run', './scripts/consumer', '--repo', '.', '--mode', 'workspace', '--budget', '10m')
        if ($Race) { $consumerArgs += '--race' }
        & go @consumerArgs
        if ($LASTEXITCODE -ne 0) { throw 'Independent consumer verification failed.' }
    }
    if ($Changed) {
        Write-Host 'Changed smoke verification passed (full root vet/build; lint skipped). Run full verification before committing.'
    } else {
        Write-Host 'Verification passed.'
    }
} finally {
    Pop-Location
}
