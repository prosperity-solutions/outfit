#!/usr/bin/env bash
# The installer's contract with the rest of the repo, checked from the files:
#   1. every OUTFIT_* variable named in install.sh's header is read in its body,
#      and every one it reads is named in the header;
#   2. docs/install.md documents exactly those variables (no promise the script
#      ignores, no silent knob);
#   3. the signing key(s) embedded in install.sh are exactly the ones in
#      internal/update/keys.txt, so `outfit update` and `curl | sh` trust the
#      same keys;
#   4. the variables the updater shares with the installer (token, base and API
#      URLs) appear in the Go code as string literals (the names it looks up), and
#      every OUTFIT_* name the Go code mentions is documented (docs/*.md other than
#      the spec, README.md, AGENTS.md).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail=0
bad() { echo "FAIL: $*"; fail=1; }

vars() { grep -oE 'OUTFIT_[A-Z0-9_]+' | sort -u; }

split=$(grep -n '^set -eu' install.sh | head -1 | cut -d: -f1)
header=$(head -n "$split" install.sh | grep -E '^#   OUTFIT_' | vars)
body=$(tail -n +"$((split + 1))" install.sh | vars)

for v in $header; do
  grep -qx "$v" <<<"$body" || bad "$v is in install.sh's header but never read"
done
for v in $body; do
  grep -qx "$v" <<<"$header" || bad "install.sh reads $v but its header does not list it"
done

docs=$(vars <docs/install.md)
for v in $header; do grep -qx "$v" <<<"$docs" || bad "$v is not documented in docs/install.md"; done
for v in $docs; do grep -qx "$v" <<<"$header" || bad "docs/install.md names $v, which install.sh does not read"; done

embedded=$(sed -n 's/^PUBKEYS="\(.*\)"$/\1/p' install.sh | tr ' ' '\n' | sort)
committed=$(grep -vE '^(#|untrusted comment:|$)' internal/update/keys.txt | tr -d '\r ' | sort)
[ -n "$embedded" ] || bad "no PUBKEYS line in install.sh"
[ "$embedded" = "$committed" ] || bad "keys in install.sh differ from internal/update/keys.txt"

# 4. Go side. Test-only code (internal/testkit, *_test.go) is not the product.
gofiles=$(find cmd internal -name '*.go' ! -name '*_test.go' ! -path 'internal/testkit/*')
# shellcheck disable=SC2086
govars=$(cat $gofiles | vars)
# shellcheck disable=SC2086
gostrings=$(cat $gofiles | grep -oE '"OUTFIT_[A-Z0-9_]+"' | sort -u)
corpus=$(cat README.md AGENTS.md)
for f in docs/*.md; do
  [ "$f" = docs/HANDOVER.md ] || corpus="$corpus$(cat "$f")"
done
for v in $govars; do
  # OUTFIT_BIN is a variable inside the generated hook shim's own text, not environment.
  [ "$v" = OUTFIT_BIN ] && continue
  grep -qF "$v" <<<"$corpus" || bad "the Go code reads $v but no document (docs/*.md, README.md, AGENTS.md) mentions it"
done
for v in OUTFIT_GITHUB_TOKEN OUTFIT_BASE_URL OUTFIT_API_URL; do
  grep -qx "$v" <<<"$header" || bad "$v is not in install.sh's header"
  grep -qF "\"$v\"" <<<"$gostrings" || bad "install.sh reads $v but the updater (Go) has no \"$v\" literal to look it up by"
done

# 5. install.sh's strict archive allow-list matches what .goreleaser.yaml packs. (The
# Go side, internal/update.ExtractBinary, is deliberately lenient about extra plain
# files, so it never refuses an archive install.sh accepts.)
sh_names=$(sed -n '/^check_archive()/,/^}/p' install.sh | grep -oE 'outfit \| LICENSE \| README\.md \| NOTICE' | tr -d ' |' | head -1)
[ -n "$sh_names" ] || bad "could not read the allow-list from install.sh's check_archive"
yaml_files=$(sed -n 's/^    files: \[\(.*\)\]$/\1/p' .goreleaser.yaml | tr -d ' ' | tr ',' '\n' | sort | tr -d '\n')
want_files=$(printf '%s' "LICENSE README.md NOTICE" | tr ' ' '\n' | sort | tr -d '\n')
[ "$yaml_files" = "$want_files" ] || bad ".goreleaser.yaml's archive files ($yaml_files) differ from install.sh's allow-list minus the binary ($want_files)"

[ "$fail" = 0 ] && echo "install contract OK ($(wc -w <<<"$header" | tr -d ' ') variables, $(wc -l <<<"$embedded" | tr -d ' ') key(s))"
exit "$fail"
