#!/usr/bin/env bash
# Checks the Developer ID signature on a macOS binary, on a Mac:
#   scripts/release/check-macos-signature.sh PATH_TO_outfit
# TEAM_ID, when set, must equal the signature's TeamIdentifier. Used before
# publishing (on the smoke runners) and again on the published file.
set -euo pipefail

bin=${1:?usage: check-macos-signature.sh BINARY}
[ "$(uname -s)" = Darwin ] || { echo "needs macOS (codesign)"; exit 2; }

codesign --verify --strict --verbose=2 "$bin"
info=$(codesign -dvv "$bin" 2>&1)
printf '%s\n' "$info" | grep -E '^(Identifier|Authority|TeamIdentifier|Timestamp|CodeDirectory)'
printf '%s\n' "$info" | grep -q '^Authority=Developer ID Application' ||
  { echo "::error::not signed by a Developer ID Application certificate"; exit 1; }
printf '%s\n' "$info" | grep -q 'flags=.*runtime' || { echo "::error::hardened runtime is not enabled"; exit 1; }
printf '%s\n' "$info" | grep -q '^Timestamp=' || { echo "::error::the signature has no secure timestamp"; exit 1; }
if [ -n "${TEAM_ID:-}" ]; then
  got=$(printf '%s\n' "$info" | sed -n 's/^TeamIdentifier=//p')
  [ "$got" = "$TEAM_ID" ] || { echo "::error::TeamIdentifier is '$got', expected '$TEAM_ID'"; exit 1; }
fi
# Informational: a bare executable is not an app, so spctl may say so.
spctl -a -t install -vv "$bin" || true
echo "archs: $(lipo -archs "$bin")"
