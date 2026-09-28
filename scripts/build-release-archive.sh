#!/usr/bin/env sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output=${1:-"$root/release-$(date -u +%Y%m%d%H%M%S).tar.gz"}
python3 "$root/scripts/package_release.py" --root "$root" --output "$output"
printf 'sha256: '
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$output" | awk '{print $1}'
else
  shasum -a 256 "$output" | awk '{print $1}'
fi
