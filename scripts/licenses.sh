#!/bin/sh
# Regenerates third_party/go-licenses (license texts of all Go dependencies linked into the
# binary). Run after changing dependencies; CI verifies the result is committed.
set -eu
cd "$(dirname "$0")/.."
rm -rf third_party/go-licenses
GOFLAGS=-mod=mod go run github.com/google/go-licenses/v2@v2.0.1 save ./cmd/typst-renderer \
  --save_path=third_party/go-licenses --ignore github.com/mache-vermietung/typst-renderer
