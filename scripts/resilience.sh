#!/bin/sh
set -eu
: "${FRAME_TEST_MYSQL_DSN:?Set an explicitly designated MySQL test database}"
: "${FRAME_TEST_POSTGRES_DSN:?Set an explicitly designated PostgreSQL test database}"
: "${FRAME_TEST_REDIS_ADDR:?Set an explicitly designated Redis test service}"
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
FRAME_TEST_RECOVERY=1 go test ./internal/faulttest ./database/mysql ./database/postgres ./redis -run 'TestProxy|TestRecoveryIntegration' -count=1 -timeout=2m -v
go test ./http/middleware -run '^TestAdmissionSustainedHTTPOverload$' -count=1 -timeout=1m -v
