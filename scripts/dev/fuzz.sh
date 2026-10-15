#!/usr/bin/env bash
#
# Run every fuzz target for a while (default 20 s each). A failure leaves the input in
# testdata/fuzz/<Target>/ of its package: commit it, it becomes a regression test.
set -euo pipefail

secs=${1:-20}
cd "$(dirname "$0")/../.."
targets=(
  "config FuzzParseFile"
  "config FuzzNormalizeKey"
  "plan FuzzSparse"
  "plan FuzzPlan"
  "update FuzzExtractBinary"
  "update FuzzExtractBinaryEntries"
)
for t in "${targets[@]}"; do
  read -r pkg name <<<"$t"
  echo "== $pkg $name (${secs}s)"
  go test "./internal/$pkg" -run '^$' -fuzz "^${name}\$" -fuzztime "${secs}s"
done
