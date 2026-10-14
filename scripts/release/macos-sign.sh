#!/usr/bin/env bash
# Sign (and, when the notary secrets exist, notarize) the macOS binary. Run by
# GoReleaser as a post hook of the universal binary (.goreleaser.yaml), on a macOS
# runner. Needs xcrun/codesign, so it cannot run on Linux.
#
#   scripts/release/macos-sign.sh PATH_TO_outfit
#
# env (all from release.yml; see docs/macos-signing.md):
#   MACOS_SIGN_P12, MACOS_SIGN_PASSWORD, MACOS_TEAM_ID   sign (all three, or none)
#   MACOS_NOTARY_KEY, MACOS_NOTARY_KEY_ID, MACOS_NOTARY_ISSUER_ID   also notarize
#
# Without MACOS_SIGN_P12 the binary is left as it is, with a log line saying so:
# curl-installed binaries are never quarantined, so Gatekeeper does not check
# them. Notarizing a bare executable cannot be stapled, and does not need to be.
set -euo pipefail

die() {
  if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::error::$*" >&2; else echo "error: $*" >&2; fi
  exit 1
}

bin=${1:?usage: macos-sign.sh PATH_TO_outfit}
[ -f "$bin" ] || die "$bin does not exist"
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

if [ -z "${MACOS_SIGN_P12:-}" ]; then
  echo "MACOS_SIGN_P12 is not set: leaving $bin unsigned (see docs/macos-signing.md)" >&2
  exit 0
fi
[ "$(uname -s)" = Darwin ] || die "signing needs macOS (codesign); this is $(uname -s)"
: "${MACOS_SIGN_PASSWORD:?MACOS_SIGN_PASSWORD is not set, but MACOS_SIGN_P12 is}"
: "${MACOS_TEAM_ID:?the repository variable MACOS_TEAM_ID is not set, but MACOS_SIGN_P12 is}"

work=$(mktemp -d)
kc="$work/outfit-signing.keychain-db"
cleanup() {
  "$here/macos-keychain.sh" delete "$kc" || true
  rm -rf "$work"
}
trap cleanup EXIT

identity=$("$here/macos-keychain.sh" import "$kc")
KEYCHAIN=$kc "$here/macos-codesign.sh" "$identity" outfit "$bin"

if [ -z "${MACOS_NOTARY_KEY:-}" ]; then
  echo "MACOS_NOTARY_KEY is not set: signed, not notarized" >&2
  exit 0
fi
: "${MACOS_NOTARY_KEY_ID:?MACOS_NOTARY_KEY_ID is not set, but MACOS_NOTARY_KEY is}"
: "${MACOS_NOTARY_ISSUER_ID:?MACOS_NOTARY_ISSUER_ID is not set, but MACOS_NOTARY_KEY is}"

# The App Store Connect API key (.p8), from base64, for the length of this run only.
key="$work/notary.p8"
(umask 077 && printf '%s' "$MACOS_NOTARY_KEY" | base64 --decode > "$key") 2>/dev/null || die "MACOS_NOTARY_KEY is not valid base64"
[ -s "$key" ] || die "MACOS_NOTARY_KEY decoded to nothing"
notary=(xcrun notarytool)
auth=(--key "$key" --key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID")

# notarytool takes a zip, a pkg or a dmg, not a bare file.
ditto -c -k --keepParent "$bin" "$work/outfit.zip"
echo "notarizing (this waits for Apple, up to 20 minutes)..." >&2
rc=0
out=$("${notary[@]}" submit "$work/outfit.zip" "${auth[@]}" --wait --timeout 20m --output-format json) || rc=$?
printf '%s\n' "$out" >&2
field() { printf '%s' "$out" | plutil -extract "$1" raw -o - - 2>/dev/null || true; }
status=$(field status)
id=$(field id)
if [ "$rc" != 0 ] || [ "$status" != Accepted ]; then
  if [ -n "$id" ]; then
    echo "--- notarization log for $id ---" >&2
    "${notary[@]}" log "$id" "${auth[@]}" >&2 || true
  fi
  die "notarization did not succeed (status '${status:-unknown}', exit $rc)"
fi
echo "notarized $bin (submission $id)" >&2
