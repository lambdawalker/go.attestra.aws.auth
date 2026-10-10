#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
command -v go >/dev/null 2>&1 || { echo "Install Go 1.26.6+ and add it to PATH." >&2; exit 1; }
exec go -C "$root/tools/deploy" run . -repo-root "$root" -teardown-index "$@"
