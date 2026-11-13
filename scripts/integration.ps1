[CmdletBinding()]
param([switch]$Business)
$ErrorActionPreference = 'Stop'
$requiredKeys = @('FRAME_TEST_MYSQL_DSN','FRAME_TEST_POSTGRES_DSN','FRAME_TEST_REDIS_ADDR')
if ($Business) { $requiredKeys = @('FRAME_BUSINESS_TEST_POSTGRES_DSN') }
foreach ($key in $requiredKeys) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($key))) {
        throw "Set $key to a disposable test service before running integration checks."
    }
}
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    if ($Business) {
        go test ./examples/business/internal/category/store -run '^TestPostgresIntegration$' -count=1 -timeout=2m -v
        if ($LASTEXITCODE -ne 0) { throw 'Business PostgreSQL integration checks failed.' }
        return
    }
    go test ./database/mysql ./database/postgres ./redis ./cache/redis ./lock/redis ./examples/ent -run Integration -count=1 -timeout=2m
    if ($LASTEXITCODE -ne 0) { throw 'Integration checks failed.' }
} finally { Pop-Location }
