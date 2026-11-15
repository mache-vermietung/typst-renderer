#!/bin/sh
# Installs the Typst release pinned in build/typst.version into <dest-dir> after verifying the
# SHA-256 checksum from build/typst.sha256. Used by the Dockerfile and by CI.
#
# Usage: scripts/install-typst.sh <dest-dir>
set -eu

dest="${1:?usage: install-typst.sh <dest-dir>}"
root="$(cd "$(dirname "$0")/.." && pwd)"
version="$(tr -d '\r\n' < "$root/build/typst.version")"

case "${TYPST_ARCH:-$(uname -m)}" in
  x86_64 | amd64) archive="typst-x86_64-unknown-linux-musl.tar.xz" ;;
  aarch64 | arm64) archive="typst-aarch64-unknown-linux-musl.tar.xz" ;;
  *) echo "unsupported architecture: ${TYPST_ARCH:-$(uname -m)}" >&2; exit 1 ;;
esac

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL "https://github.com/typst/typst/releases/download/v${version}/${archive}" -o "$tmp/$archive"

expected="$(grep -F "  $archive" "$root/build/typst.sha256" | cut -d' ' -f1)"
[ -n "$expected" ] || { echo "no checksum for $archive in build/typst.sha256" >&2; exit 1; }
echo "$expected  $tmp/$archive" | sha256sum -c -

tar -xf "$tmp/$archive" -C "$tmp"
mkdir -p "$dest"
install -m 0755 "$tmp/${archive%.tar.xz}/typst" "$dest/typst"

# Apache-2.0 requires shipping the license text and NOTICE with the binary.
licenses="$dest/../licenses/typst"
mkdir -p "$licenses"
install -m 0644 "$tmp/${archive%.tar.xz}/LICENSE" "$tmp/${archive%.tar.xz}/NOTICE" "$licenses/"
