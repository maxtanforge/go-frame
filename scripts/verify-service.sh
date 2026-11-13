#!/bin/sh
# Port of verify-service.ps1 for Unix shells. Verifies templates/service as an
# independent module via a temporary, verification-only local replacement;
# remote module download is not verified.
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
temp_root=${TMPDIR:-/tmp}
work_dir=$temp_root/go-frame-service-$(date +%s)-$$
# Isolate the template build the same way the PowerShell version does: no
# workspace, no proxy, no toolchain downloads, a private build cache.
frame_go() {
    GOFLAGS=-p=1 GOMAXPROCS=2 GOCACHE="$temp_root/go-frame-enterprise-cache" \
        GOWORK=off GOPROXY=off GOSUMDB=off GONOPROXY=none GOTOOLCHAIN=local \
        go "$@"
}
cleanup() {
    [ -d "$work_dir" ] || return 0
    resolved=$(CDPATH= cd -- "$work_dir" && pwd)
    case "$resolved" in
        "$temp_root"/go-frame-service-*) rm -rf -- "$resolved" ;;
        *) echo 'Refusing cleanup outside the owned temporary service directory.' >&2 ;;
    esac
}
trap cleanup EXIT HUP INT TERM
mkdir -- "$work_dir"
cp -R -- "$repo_root/templates/service/." "$work_dir/"
cd -- "$work_dir"
# Verification only: the source template never receives a local replace.
frame_go mod edit "-replace=github.com/maxtanforge/go-frame=$repo_root"
# Warm the dependency graph (test deps of dependencies included) with the
# default environment; every verification step below still runs offline.
go mod tidy
frame_go mod tidy
frame_go test ./... -count=1 -timeout=2m
frame_go vet ./...
frame_go build -buildvcs=false -o service-bin ./cmd/api
echo 'Service verification passed using a temporary local replacement; remote module download was not verified.'
