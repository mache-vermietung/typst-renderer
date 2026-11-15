#!/bin/sh
# Regenerates the Go code for proto/renderer/v1 with the tool versions pinned in tools.mod.
set -eu
cd "$(dirname "$0")/.."
go tool -modfile=tools.mod buf generate
