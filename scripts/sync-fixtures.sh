#!/usr/bin/env bash
# Copy the shared contract fixtures from the backend tree (the single source of
# truth) into the client test-resources tree, byte-for-byte.
#
# 08-ROADMAP.md 5 specifies this as a Gradle task once the client module exists
# (WP-C1). Until then this script is the mechanism, and CI runs it before the
# client contract lane so both suites provably deserialise the same bytes.
#
#   scripts/sync-fixtures.sh          # sync
#   scripts/sync-fixtures.sh --check  # fail if the destination is out of date
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
src="$repo_root/backend/internal/testutil/testdata"
dst="$repo_root/client/app/src/test/resources/fixtures"

mode=${1:-sync}

if [[ ! -d "$src" ]]; then
  echo "sync-fixtures: source $src does not exist" >&2
  exit 1
fi

if [[ "$mode" == "--check" ]]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  cp -R "$src/." "$tmp/"
  if [[ -d "$dst" ]] && diff -r "$tmp" "$dst" >/dev/null 2>&1; then
    echo "sync-fixtures: client fixtures are up to date"
    exit 0
  fi
  echo "sync-fixtures: client fixtures are STALE - run scripts/sync-fixtures.sh and commit" >&2
  exit 1
fi

rm -rf "$dst"
mkdir -p "$dst"
cp -R "$src/." "$dst/"

# Byte-identity guard: a mismatch here means the copy is not faithful.
while IFS= read -r -d '' f; do
  rel=${f#"$src"/}
  cmp -s "$f" "$dst/$rel" || { echo "sync-fixtures: $rel differs after copy" >&2; exit 1; }
done < <(find "$src" -type f -print0)

count=$(find "$src" -type f | wc -l | tr -d ' ')
echo "sync-fixtures: copied $count files -> ${dst#"$repo_root"/}"
