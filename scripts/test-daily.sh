#!/bin/sh
set -eu
if [ "$(uname -s)" != Linux ] || [ "$(id -u)" = 0 ]; then
    echo 'Run daily tests as an unprivileged user in Linux / SCP-Test.' >&2
    exit 1
fi
cd "$(dirname "$0")/.."
go test ./... -count=1 "$@"
go vet ./...
