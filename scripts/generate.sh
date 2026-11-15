#!/bin/sh
# Regenerates the Go code for proto/renderer/v1 with the tool versions pinned in tools.mod.
set -eu
cd "$(dirname "$0")/.."

# tools.mod can require a newer Go than go.mod (buf's dependencies do). Run the tools with that
# toolchain instead of raising the minimum Go version of the module itself.
tools_go="$(awk '/^go /{print $2}' tools.mod)"

GOTOOLCHAIN="go${tools_go}" go tool -modfile=tools.mod buf generate
