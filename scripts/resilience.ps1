[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
foreach ($key in @('FRAME_TEST_MYSQL_DSN','FRAME_TEST_POSTGRES_DSN','FRAME_TEST_REDIS_ADDR')) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($key))) {
        throw "Set $key to an explicitly designated test service."
    }
}
$previousRecovery = $env:FRAME_TEST_RECOVERY
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    $env:FRAME_TEST_RECOVERY = '1'
    go test ./internal/faulttest ./database/mysql ./database/postgres ./redis -run 'TestProxy|TestRecoveryIntegration' -count=1 -timeout=2m -v
    if ($LASTEXITCODE -ne 0) { throw 'Backend recovery checks failed.' }
    go test ./http/middleware -run '^TestAdmissionSustainedHTTPOverload$' -count=1 -timeout=1m -v
    if ($LASTEXITCODE -ne 0) { throw 'HTTP overload check failed.' }
} finally {
    $env:FRAME_TEST_RECOVERY = $previousRecovery
    Pop-Location
}
