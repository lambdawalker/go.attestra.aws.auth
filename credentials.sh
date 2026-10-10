#!/usr/bin/env sh
set -eu
exec "$(dirname "$0")/deploy.sh" -manage-credentials "$@"
