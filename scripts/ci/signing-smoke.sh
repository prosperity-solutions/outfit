#!/usr/bin/env bash
# Proves the macOS signing path works on every PR, without real secrets: builds
# the universal binary through GoReleaser with a throwaway certificate chain
# (a made-up root and a "Developer ID"-shaped leaf with a team OU), then checks
# with `quill describe` that BOTH architectures carry a hardened-runtime,
# timestamped signature with the right team ID. Runs on Linux, like the release.
#
# It also runs the minisign signing step with a throwaway ENCRYPTED key (aead's
# CLI reads the password as a line from stdin; a password without its newline made
# the first release rehearsal fail with "failed to read password: EOF") and
# verifies the result with the same Go code the release uses.
#
# Needs: go, goreleaser, quill, openssl and aead's minisign (go install
# aead.dev/minisign/cmd/minisign@v0.3.0) on PATH. Exits non-zero on any miss.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"
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

printf 'pw\npw\n' | minisign -G -p "$work/mini.pub" -s "$work/mini.key" >/dev/null
export MINISIGN_KEY_FILE="$work/mini.key" MINISIGN_PASSWORD=pw

goreleaser release --snapshot --clean --skip=publish >/dev/null
bin=dist/outfit-darwin_darwin_all/outfit
[ -f "$bin" ] || { echo "no universal binary at $bin"; exit 1; }

(cd "$root" && go run ./internal/tools/verifysig dist/checksums.txt dist/checksums.txt.minisig "$work/mini.pub") ||
  { echo "FAIL: the minisign signature does not verify"; exit 1; }
echo "ok:   checksums.txt is signed and verifies"

# The names install.sh looks for. A renamed archive template would otherwise only
# fail after merge, in the release's smoke legs.
for plat in linux_amd64 linux_arm64 darwin_all; do
  ls dist/outfit_*_"$plat".tar.gz >/dev/null 2>&1 || { echo "FAIL: no archive named outfit_<version>_$plat.tar.gz in dist/ (install.sh needs exactly that)"; exit 1; }
done
echo "ok:   the three archive names install.sh expects"

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
