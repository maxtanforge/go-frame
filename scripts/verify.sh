#!/bin/sh
set -eu

race=false
consumer=false
bench=false
changed=false
coverage=false
timing=false
bench_time=1s
while [ "$#" -gt 0 ]; do
    case "$1" in
        --race) race=true ;;
        --consumer) consumer=true ;;
        --bench) bench=true ;;
        --changed) changed=true ;;
        --coverage) coverage=true ;;
        --timing) timing=true ;;
        --benchtime)
            shift
            [ "$#" -gt 0 ] || { echo '--benchtime requires a duration' >&2; exit 2; }
            bench_time=$1 ;;
        *) echo "Unknown option: $1" >&2; exit 2 ;;
    esac
    shift
done
if "$changed" && { "$consumer" || "$bench" || "$coverage"; }; then
    echo '--changed is a smoke mode and cannot be combined with --consumer, --bench, or --coverage.' >&2
    exit 2
fi
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
command -v go >/dev/null
command -v gofmt >/dev/null
command -v git >/dev/null
# NUL-separated paths also support repository locations/files containing spaces.
source_list=$(mktemp)
trap 'rm -f "$source_list"' EXIT HUP INT TERM
git ls-files -z --cached --others --exclude-standard -- '*.go' > "$source_list"
unformatted=$(xargs -0 sh -c 'for file do
    [ -f "$file" ] || continue
    gofmt -l "$file" || exit
done' sh < "$source_list")
if [ -n "$unformatted" ]; then
    printf 'Run gofmt on:\n%s\n' "$unformatted" >&2
    exit 1
fi
if "$race"; then
    [ "$(go env CGO_ENABLED)" = 1 ] || {
        echo 'Race verification requires CGO_ENABLED=1 and a supported C compiler. No settings were changed.' >&2
        exit 1
    }
fi
set -- go test ./... -count=1 -timeout=5m
if "$changed" || "$coverage" || "$timing"; then
    set -- go run ./scripts/verify-test
    if "$changed"; then set -- "$@" --changed; fi
    if "$coverage"; then set -- "$@" --coverage; fi
    if "$timing"; then set -- "$@" --timing; fi
fi
if "$race"; then set -- "$@" -race; fi
"$@"
go vet ./...
if ! "$changed"; then
    # Pinned golangci-lint: no local install required, first run compiles via the module proxy.
    go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners
fi
go build -buildvcs=false ./...
if "$bench"; then
    go test ./concurrency ./telemetry/logger -run '^$' -bench . -benchmem "-benchtime=$bench_time"
fi
if "$consumer"; then
    if "$race"; then
        go run ./scripts/consumer --repo . --mode workspace --budget 10m --race
    else
        go run ./scripts/consumer --repo . --mode workspace --budget 10m
    fi
fi
if "$changed"; then
    echo 'Changed smoke verification passed (full root vet/build; lint skipped). Run full verification before committing.'
else
    echo 'Verification passed.'
fi
