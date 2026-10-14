#!/usr/bin/env bash
# Proves the macOS signing path works on every PR, without real secrets: builds
# the universal binary through GoReleaser with a throwaway certificate chain
# (a made-up root and a "Developer ID"-shaped leaf with a team OU), then checks
# with `quill describe` that BOTH architectures carry a hardened-runtime,
# timestamped signature with the right team ID. Runs on Linux, like the release.
#
# Needs: goreleaser, quill, openssl on PATH. Exits non-zero on any miss.
set -euo pipefail

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
team=TESTTEAM00

openssl req -x509 -newkey rsa:2048 -nodes -keyout "$work/ca.key" -out "$work/ca.pem" -days 2 \
  -subj "/CN=outfit test root" 2>/dev/null
openssl req -newkey rsa:2048 -nodes -keyout "$work/leaf.key" -out "$work/leaf.csr" \
  -subj "/OU=$team/CN=Developer ID Application: outfit test ($team)" 2>/dev/null
printf 'basicConstraints=critical,CA:false\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=critical,codeSigning\n' >"$work/leaf.ext"
openssl x509 -req -in "$work/leaf.csr" -CA "$work/ca.pem" -CAkey "$work/ca.key" -CAcreateserial \
  -out "$work/leaf.pem" -days 2 -extfile "$work/leaf.ext" 2>/dev/null
openssl pkcs12 -export -inkey "$work/leaf.key" -in "$work/leaf.pem" -certfile "$work/ca.pem" \
  -out "$work/chain.p12" -passout pass:smoke

# base64 -w0 is GNU only; tr works on both.
MACOS_SIGN_P12=$(base64 <"$work/chain.p12" | tr -d '\n')
export MACOS_SIGN_P12
export MACOS_SIGN_PASSWORD=smoke
unset MACOS_NOTARY_KEY MACOS_NOTARY_KEY_ID MACOS_NOTARY_ISSUER_ID

goreleaser release --snapshot --clean --skip=publish,sign >/dev/null
bin=dist/outfit-darwin_darwin_all/outfit
[ -f "$bin" ] || { echo "no universal binary at $bin"; exit 1; }

desc=$(quill describe "$bin" 2>&1)
fail=0
want() { # want DESCRIPTION PATTERN COUNT
  n=$(printf '%s\n' "$desc" | grep -c -- "$2" || true)
  if [ "$n" -ne "$3" ]; then echo "FAIL: $1 (wanted $3 of '$2', found $n)"; fail=1; else echo "ok:   $1"; fi
}
want "two architectures in the universal binary" "^Binary [0-9] of 2" 2
want "both slices have the hardened runtime" "Flags:    runtime" 2
want "both slices carry the team ID" "TeamID:   $team" 2
want "both slices are timestamped" "signing timestamp" 2
want "the Developer ID leaf is in the chain" "CN:  Developer ID Application: outfit test ($team)" 2
exit "$fail"
