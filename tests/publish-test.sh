#!/usr/bin/env bash
# Tests scripts/release/publish.sh against a stub `gh` that keeps a release in a
# directory and can be told to fail: flaky uploads, partial uploads, a refused
# flip. The property under test: the release only ever becomes public with every
# asset present at the right size, and every failure leaves it a draft.
set -u

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
PASSES=0
FAILS=0
pass() { PASSES=$((PASSES + 1)); echo "ok   $1"; }
fail() { FAILS=$((FAILS + 1)); echo "FAIL $1"; [ -f "$WORK/out" ] && sed 's/^/       /' "$WORK/out" | tail -12; }
check() { local n=$1; shift; if "$@"; then pass "$n"; else fail "$n"; fi; }

# ── stub gh ─────────────────────────────────────────────────────────────────
# State lives in $STATE: draft (file present while a draft), assets/<name>,
# state/<name> (uploaded|starter), uploads (count of upload calls).
mkdir -p "$WORK/bin"
cat >"$WORK/bin/gh" <<'STUB'
#!/usr/bin/env bash
S=${STUB_STATE:?}
mkdir -p "$S/assets" "$S/state"
inc() { n=$(cat "$S/$1" 2>/dev/null || echo 0); echo $((n + 1)) >"$S/$1"; }
case "$1 $2" in
  "release view")
    [ -f "$S/probe_fail" ] && { echo "HTTP 502: Bad Gateway" >&2; exit 1; }
    [ -f "$S/exists" ] || { echo "release not found" >&2; exit 1; }
    case "$*" in
      *"--json databaseId"*) echo 4242 ;;
      *"--json isDraft"*) [ -f "$S/draft" ] && echo true || echo false ;;
      *"--json assets"*)
        inc reads
        if [ -f "$S/read_fail_always" ]; then exit 1; fi
        for f in "$S"/assets/*; do
          [ -e "$f" ] || continue
          n=$(basename "$f")
          if [ "$(cat "$S/state/$n")" = uploaded ]; then printf '%s\t%s\n' "$n" "$(wc -c <"$f" | tr -d ' ')"; fi
        done ;;
    esac ;;
  "release create")
    inc creates
    [ -f "$S/create_fail" ] && exit 1
    case "$*" in *--draft*) ;; *) echo "create without --draft" >&2; exit 9 ;; esac
    touch "$S/exists" "$S/draft" ;;
  "release upload")
    inc uploads
    k=$(cat "$S/uploads")
    shift 3
    if [ "$k" -le "${STUB_FAIL_UPLOADS:-0}" ]; then exit 1; fi
    for f in "$@"; do
      [ "$f" = --clobber ] && continue
      n=$(basename "$f")
      if [ -n "${STUB_PARTIAL:-}" ] && [ "$n" = "$STUB_PARTIAL" ] && [ "$k" -le "${STUB_PARTIAL_UNTIL:-1}" ]; then
        head -c 3 "$f" >"$S/assets/$n"; echo starter >"$S/state/$n"; continue
      fi
      cp "$f" "$S/assets/$n"; echo uploaded >"$S/state/$n"
    done ;;
  "api -X")
    inc patches
    [ -f "$S/patch_fail" ] && exit 1
    case "$*" in *make_latest=legacy*) ;; *) echo "flip without make_latest=legacy" >&2; exit 9 ;; esac
    case "$*" in *draft=false*) rm -f "$S/draft" ;; esac ;;
  *) echo "stub gh: unexpected $*" >&2; exit 9 ;;
esac
STUB
chmod +x "$WORK/bin/gh"

VER=1.2.3
mkrel() { # mkrel DIR
  mkdir -p "$1"
  rm -f "$1"/*
  local p
  for p in linux_amd64 linux_arm64 darwin_all; do echo "archive $p" >"$1/outfit_${VER}_$p.tar.gz"; done
  echo sums >"$1/checksums.txt"
  echo sig >"$1/checksums.txt.minisig"
}
REL=$WORK/rel
mkrel "$REL"

run() { # run STATE_DIR [env...] -- args...
  local st=$1
  shift
  local envs=()
  while [ $# -gt 0 ] && [ "$1" != -- ]; do envs+=("$1"); shift; done
  shift
  mkdir -p "$st"
  (cd "$WORK" && env PATH="$WORK/bin:$PATH" STUB_STATE="$st" GH_REPO=o/r PUBLISH_SLEEP_SCALE=0 ${envs[@]+"${envs[@]}"} bash "$ROOT/scripts/release/publish.sh" "$@") >"$WORK/out" 2>&1
  RC=$?
}
is_public() { [ -f "$1/exists" ] && [ ! -f "$1/draft" ]; }
is_draft() { [ -f "$1/exists" ] && [ -f "$1/draft" ]; }
has_all_assets() { [ "$(ls "$1/assets" | wc -l | tr -d ' ')" = 5 ] && ! grep -rq starter "$1/state"; }
out_has() { grep -qF -- "$1" "$WORK/out"; }
rc() { [ "$RC" = "$1" ]; }

S=$WORK/s1; run "$S" -- v$VER "$REL"
check "happy path publishes" is_public "$S"
check "  with every asset" has_all_assets "$S"
check "  exit 0" rc 0

S=$WORK/s2; run "$S" -- v$VER "$REL"; run "$S" -- v$VER "$REL"
check "running again on a published release is harmless" rc 0
check "  and does not create a second release" bash -c '[ "$(cat "$1/creates")" = 1 ]' _ "$S"

S=$WORK/s3; run "$S" STUB_FAIL_UPLOADS=2 -- v$VER "$REL"
check "flaky uploads are retried" is_public "$S"
check "  ending with every asset" has_all_assets "$S"

S=$WORK/s4; run "$S" STUB_PARTIAL=checksums.txt STUB_PARTIAL_UNTIL=2 -- v$VER "$REL"
check "a partial upload is caught by size and state and redone" is_public "$S"
check "  with every asset whole" has_all_assets "$S"

S=$WORK/s5; run "$S" STUB_FAIL_UPLOADS=99 -- v$VER "$REL"
check "uploads that never work leave a draft" is_draft "$S"
check "  and fail the job" rc 1
check "  naming what is missing" out_has "missing"

S=$WORK/s6; run "$S" STUB_PARTIAL=checksums.txt STUB_PARTIAL_UNTIL=99 -- v$VER "$REL"
check "an asset that stays partial never publishes" is_draft "$S"

S=$WORK/s7; mkdir -p "$S"; touch "$S/patch_fail"; run "$S" -- v$VER "$REL"
check "a refused flip fails the job" rc 1
check "  and stays a draft" is_draft "$S"
check "  without claiming it is merely a draft" out_has "could not be confirmed published"

S=$WORK/s8; mkdir -p "$S"; touch "$S/read_fail_always" "$S/exists" "$S/draft"; run "$S" -- v$VER "$REL"
check "if the release cannot be read, nothing is published" is_draft "$S"
check "  and it does not guess" out_has "rather than guessing"
check "  and uploads nothing" bash -c '[ ! -e "$1/uploads" ]' _ "$S"

# A draft left by an earlier build holds a same-size, different-content checksums.txt.
S=$WORK/s8b; mkdir -p "$S/assets" "$S/state"; touch "$S/exists" "$S/draft"
for f in "$REL"/*; do n=$(basename "$f"); cp "$f" "$S/assets/$n"; echo uploaded >"$S/state/$n"; done
tr 'a-z' 'A-Z' <"$REL/checksums.txt" >"$S/assets/checksums.txt"
run "$S" -- v$VER "$REL"
check "a draft with a stale same-size asset is topped up with the right bytes" cmp -s "$S/assets/checksums.txt" "$REL/checksums.txt"
check "  and published" is_public "$S"

# An error while looking for the release is not "no such release".
S=$WORK/s8c; mkdir -p "$S"; touch "$S/probe_fail" "$S/exists" "$S/draft"; run "$S" -- v$VER "$REL"
check "a failed probe never creates a second release" bash -c '[ ! -e "$1/creates" ]' _ "$S"
check "  and fails the job" rc 1

# A published release is never modified.
S=$WORK/s8d; mkdir -p "$S/assets" "$S/state"; touch "$S/exists"
for f in "$REL"/*; do n=$(basename "$f"); cp "$f" "$S/assets/$n"; echo uploaded >"$S/state/$n"; done
run "$S" -- v$VER "$REL"
check "re-running on a complete published release changes nothing" rc 0
check "  and uploads nothing" bash -c '[ ! -e "$1/uploads" ] && [ ! -e "$1/patches" ]' _ "$S"
echo different >"$S/assets/outfit_${VER}_darwin_all.tar.gz"
run "$S" -- v$VER "$REL"
check "a published release whose assets differ is refused" rc 1
check "  and not touched" bash -c '[ ! -e "$1/uploads" ]' _ "$S"

S=$WORK/s9; mkdir -p "$S"; touch "$S/create_fail"; run "$S" -- v$VER "$REL"
check "a release that cannot be created fails" rc 1

S=$WORK/s10; run "$S" -- "" "$REL"
check "an empty tag is refused" rc 1
S=$WORK/s11; run "$S" -- latest "$REL"
check "a tag that is not a version is refused" rc 1
check "  and nothing was called" bash -c '[ ! -e "$1/exists" ]' _ "$S"

mkrel "$WORK/rel2"; rm "$WORK/rel2/checksums.txt.minisig"
S=$WORK/s12; run "$S" -- v$VER "$WORK/rel2"
check "an unsigned release is refused before anything is created" rc 1
check "  and no release exists" bash -c '[ ! -e "$1/exists" ]' _ "$S"

mkrel "$WORK/rel3"; rm "$WORK/rel3/outfit_${VER}_darwin_all.tar.gz"
S=$WORK/s13; run "$S" -- v$VER "$WORK/rel3"
check "a release missing a platform is refused" rc 1

mkrel "$WORK/rel4"; echo x >"$WORK/rel4/bad name.txt"
S=$WORK/s14; run "$S" -- v$VER "$WORK/rel4"
check "an asset name GitHub would rewrite is refused" rc 1
check "  with the name in the message" out_has "bad name.txt"

mkrel "$WORK/rel5"; mkdir "$WORK/rel5/nested"
S=$WORK/s15; run "$S" -- v$VER "$WORK/rel5"
check "a nested directory is refused" rc 1

mkrel "$WORK/rel6"; ln -s checksums.txt "$WORK/rel6/link.txt"
S=$WORK/s16; run "$S" -- v$VER "$WORK/rel6"
check "a symlink is refused" rc 1

echo
echo "publish-test: $PASSES passed, $FAILS failed"
[ "$FAILS" -eq 0 ]
