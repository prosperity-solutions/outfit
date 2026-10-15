#!/usr/bin/env bash
#
# Run every fuzz target for a while (default 20 s each) and report ALL failures, not just the
# first. A failure leaves the input in testdata/fuzz/<Target>/ of its package: commit it, it
# becomes a regression test.
set -uo pipefail

secs=${1:-20}
cd "$(dirname "$0")/../.." || exit 1
targets=(
  "config FuzzParseFile"
  "config FuzzNormalizeKey"
  "plan FuzzSparse"
  "plan FuzzPlan"
  "update FuzzExtractBinary"
  "update FuzzExtractBinaryEntries"
)
failed=()
for t in "${targets[@]}"; do
  read -r pkg name <<<"$t"
  echo "== $pkg $name (${secs}s)"
  if ! go test "./internal/$pkg" -run '^$' -fuzz "^${name}\$" -fuzztime "${secs}s"; then
    failed+=("$pkg/$name")
  fi
done
if [ "${#failed[@]}" -gt 0 ]; then
  echo "FAILED: ${failed[*]} (inputs under internal/<pkg>/testdata/fuzz/)" >&2
  exit 1
fi
echo "all ${#targets[@]} fuzz targets passed"
