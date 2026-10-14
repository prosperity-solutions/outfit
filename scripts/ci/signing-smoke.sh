#!/usr/bin/env bash
# Proves the macOS signing path works, on every PR, without the real secrets: the
# same GoReleaser snapshot the release runs, with the same hook
# (scripts/release/macos-sign.sh), a throwaway "Developer ID"-named certificate and
# an encrypted throwaway minisign key. Then it checks what matters:
#
#   * the universal binary is signed with the hardened runtime, as "outfit", in BOTH
#     slices, and it RUNS. (The first release attempt produced a binary macOS killed
#     on launch because of the Apple chain in a real Developer ID certificate. A
#     throwaway certificate has no such chain, so this cannot reproduce that class
#     of failure: the release's own smoke legs, which run the artifact signed with
#     the real certificate before publishing, are the guard for it.)
#   * checksums.txt is signed with the encrypted key (aead's CLI reads the password
#     as a line from stdin: without the trailing newline GoReleaser's `stdin` gives,
#     signing fails with "failed to read password: EOF") and verifies with the same
#     Go code the release uses;
#   * the archives have the names install.sh looks for.
#
# What it cannot exercise, and OUTFIT_CODESIGN_SELFTEST=1 therefore skips: Apple's
# timestamp server, the Apple-issued team in the designated requirement, and
# notarization. Timestamp and team are checked on the real certificate by the
# release's smoke legs (check-macos-signature.sh) before anything is published;
# notarization is checked in the build hook, and its branch is exercised below
# with a stub notarytool.
#
# CI ONLY, macOS only: it trusts the throwaway certificate system-wide (`sudo
# security add-trusted-cert`), because codesign and `find-identity -v` refuse an
# untrusted identity. Never run it on your own Mac.
# Needs: go, goreleaser and aead's minisign (go install aead.dev/minisign/cmd/minisign@v0.3.0).
set -euo pipefail

[ -n "${CI:-}" ] || { echo "refusing to run outside CI: this trusts a throwaway certificate system-wide" >&2; exit 1; }
[ "$(uname -s)" = Darwin ] || { echo "needs a macOS runner (codesign)" >&2; exit 1; }

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail() { echo "::error::$*" >&2; exit 1; }
ok() { echo "ok:   $*"; }
team=TESTTEAM00

cat >"$work/cert.cnf" <<CNF
[req]
distinguished_name = dn
prompt = no
[dn]
CN = Developer ID Application: outfit CI Smoke ($team)
OU = $team
O = outfit CI Smoke
[ext]
basicConstraints = critical, CA:false
keyUsage = critical, digitalSignature
extendedKeyUsage = critical, codeSigning
CNF
# /usr/bin/openssl (LibreSSL) on purpose: its PKCS#12 defaults are ones `security
# import` reads. 60 days: macos-keychain.sh refuses a certificate with under 30 left.
/usr/bin/openssl req -x509 -newkey rsa:2048 -nodes -days 60 -keyout "$work/key.pem" -out "$work/cert.pem" \
  -config "$work/cert.cnf" -extensions ext 2>/dev/null
/usr/bin/openssl pkcs12 -export -inkey "$work/key.pem" -in "$work/cert.pem" -out "$work/smoke.p12" -passout pass:smoke
sudo security add-trusted-cert -d -r trustRoot -p codeSign -k /Library/Keychains/System.keychain "$work/cert.pem"

MACOS_SIGN_P12=$(base64 <"$work/smoke.p12")
export MACOS_SIGN_P12 MACOS_SIGN_PASSWORD=smoke MACOS_TEAM_ID=$team OUTFIT_CODESIGN_SELFTEST=1
unset MACOS_NOTARY_KEY MACOS_NOTARY_KEY_ID MACOS_NOTARY_ISSUER_ID

# The identity is chosen by team, so the wrong team must find nothing.
if MACOS_TEAM_ID=WRONGTEAM0 scripts/release/macos-keychain.sh import "$work/wrong.keychain-db" 2>/dev/null; then
  scripts/release/macos-keychain.sh delete "$work/wrong.keychain-db" || true
  fail "macos-keychain.sh accepted a certificate for the wrong team"
fi
scripts/release/macos-keychain.sh delete "$work/wrong.keychain-db" || true
ok "an identity for the wrong team is refused"

printf 'pw\npw\n' | minisign -G -p "$work/mini.pub" -s "$work/mini.key" >/dev/null
export MINISIGN_KEY_FILE="$work/mini.key" MINISIGN_PASSWORD=pw

goreleaser release --snapshot --clean --skip=publish >"$work/goreleaser.log" 2>&1 ||
  { cat "$work/goreleaser.log"; fail "goreleaser failed"; }

bin=dist/outfit-darwin_darwin_all/outfit
[ -f "$bin" ] || fail "no universal binary at $bin"
for arch in arm64 x86_64; do
  lipo -thin "$arch" "$bin" -output "$work/outfit-$arch"
  codesign --verify --strict "$work/outfit-$arch" || fail "the $arch slice does not verify"
  info=$(codesign -dvv "$work/outfit-$arch" 2>&1)
  printf '%s\n' "$info" | grep -q '^Identifier=outfit$' || fail "the $arch slice is not signed as 'outfit'"
  printf '%s\n' "$info" | grep -Eq 'flags=0x[0-9a-f]+\([^)]*runtime' || fail "the $arch slice has no hardened runtime"
done
codesign --verify --strict "$bin" || fail "the universal binary does not verify"
ok "both slices and the universal binary verify, as 'outfit', with the hardened runtime"

# A signed binary that does not start (see the header for what this can and cannot catch).
"$bin" version | grep -q '^outfit ' || fail "the signed binary does not run"
ok "the signed universal binary runs"

(cd dist && for f in outfit_*_linux_amd64.tar.gz outfit_*_linux_arm64.tar.gz outfit_*_darwin_all.tar.gz; do [ -f "$f" ] || fail "no archive $f (install.sh needs exactly these names)"; done)
ok "the three archive names install.sh expects"

go run ./internal/tools/verifysig dist/checksums.txt dist/checksums.txt.minisig "$work/mini.pub" >/dev/null ||
  fail "the minisign signature does not verify"
ok "checksums.txt is signed with the encrypted key and verifies"

# The archived binary is the signed one, not an earlier copy.
mkdir "$work/x"
tar -xzf dist/outfit_*_darwin_all.tar.gz -C "$work/x" outfit
codesign --verify --strict "$work/x/outfit" || fail "the binary inside the darwin archive is not the signed one"
ok "the binary inside the archive is signed"

# The notarization branch of the hook, with a stub `xcrun`: Apple cannot be reached
# with a throwaway certificate, but the zip, the JSON parsing, the Accepted check
# and the log dump on failure are ours.
stub="$work/stub"
mkdir "$stub"
cat >"$stub/xcrun" <<'STUB'
#!/bin/sh
echo "$*" >>"$STUB_LOG"
case "$2" in
  submit)
    if [ "$STUB_RESULT" = accepted ]; then echo '{"id":"sub-1","status":"Accepted"}'; else echo '{"id":"sub-1","status":"Invalid"}'; exit 1; fi ;;
  log) echo '{"issues":[{"message":"stub"}]}' ;;
esac
STUB
chmod +x "$stub/xcrun"
export STUB_LOG="$work/stub.log"
MACOS_NOTARY_KEY=$(printf '%s\n' '-----BEGIN PRIVATE KEY-----' 'AAAA' '-----END PRIVATE KEY-----' | base64)
export MACOS_NOTARY_KEY MACOS_NOTARY_KEY_ID=KEYID MACOS_NOTARY_ISSUER_ID=ISSUER

cp "$bin" "$work/notarize-ok"
: >"$STUB_LOG"
PATH="$stub:$PATH" STUB_RESULT=accepted scripts/release/macos-sign.sh "$work/notarize-ok" >"$work/n1.log" 2>&1 ||
  { cat "$work/n1.log"; fail "an accepted notarization failed the hook"; }
grep -q 'notarytool submit' "$STUB_LOG" || fail "the hook never called notarytool submit"
grep -q 'notarytool log' "$STUB_LOG" && fail "the hook fetched a log for an accepted submission"
ok "an accepted notarization passes"

cp "$bin" "$work/notarize-bad"
: >"$STUB_LOG"
if PATH="$stub:$PATH" STUB_RESULT=invalid scripts/release/macos-sign.sh "$work/notarize-bad" >"$work/n2.log" 2>&1; then
  fail "a rejected notarization did not fail the hook"
fi
grep -q 'notarytool log sub-1' "$STUB_LOG" || fail "a rejected notarization did not dump the log"
ok "a rejected notarization fails the hook and dumps the log"
