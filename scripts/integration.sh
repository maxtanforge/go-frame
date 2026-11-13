#!/bin/sh
set -eu
if [ "${1-}" = '--business' ]; then
    [ "$#" -eq 1 ] || { echo 'Usage: integration.sh [--business]' >&2; exit 2; }
    : "${FRAME_BUSINESS_TEST_POSTGRES_DSN:?Set a disposable business PostgreSQL test database}"
    cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
    go test ./examples/business/internal/category/store -run '^TestPostgresIntegration$' -count=1 -timeout=2m -v
    exit
fi
[ "$#" -eq 0 ] || { echo 'Usage: integration.sh [--business]' >&2; exit 2; }
: "${FRAME_TEST_MYSQL_DSN:?Set a disposable MySQL test database}"
: "${FRAME_TEST_POSTGRES_DSN:?Set a disposable PostgreSQL test database}"
: "${FRAME_TEST_REDIS_ADDR:?Set a disposable Redis test service}"
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
go test ./database/mysql ./database/postgres ./redis ./cache/redis ./lock/redis ./examples/ent -run Integration -count=1 -timeout=2m
