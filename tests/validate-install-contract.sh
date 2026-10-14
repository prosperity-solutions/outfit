#!/usr/bin/env bash
# The installer's contract with the rest of the repo, checked from the files:
#   1. every OUTFIT_* variable named in install.sh's header is read in its body,
#      and every one it reads is named in the header;
#   2. docs/install.md documents exactly those variables (no promise the script
#      ignores, no silent knob);
#   3. the signing key(s) embedded in install.sh are exactly the ones in
#      internal/update/keys.txt, so `outfit update` and `curl | sh` trust the
#      same keys.
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

[ "$fail" = 0 ] && echo "install contract OK ($(wc -w <<<"$header" | tr -d ' ') variables, $(wc -l <<<"$embedded" | tr -d ' ') key(s))"
exit "$fail"
