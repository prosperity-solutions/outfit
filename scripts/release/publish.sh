#!/usr/bin/env bash
# Publish a GitHub release the careful way. Ported from veld's release workflow
# (MIT; see NOTICE), where softprops/action-gh-release once left releases stuck as
# drafts. Every failure here falls toward "do not publish": a draft is invisible
# to users, a half-published release is not.
#
#   scripts/release/publish.sh vX.Y.Z DIR [NOTES_FILE]
#
# DIR is a flat directory of the release files. Needs gh, authenticated through
# GH_TOKEN, and GH_REPO (owner/name) when not run inside a checkout.
#
# Steps: open (or reuse) a DRAFT release, upload what is missing (capped and
# retried, checked by name, size and state), flip it to published, read it back.
# Safe to re-run after any failure: it tops up what is missing.
set -euo pipefail

TAG=${1:-}
DIR=${2:-}
NOTES=${3:-}
# PUBLISH_SLEEP_SCALE=0 skips the waits (tests).
SCALE=${PUBLISH_SLEEP_SCALE:-1}

die() { echo "::error::$*"; exit 1; }
nap() { sleep "$(($1 * SCALE))"; }
# A capped call. `timeout` is GNU; macOS has none, and the uncapped call is only
# used where the retry loop around it is the real protection.
cap() {
  local secs=$1
  shift
  if command -v timeout >/dev/null 2>&1; then timeout -k 10 "$secs" "$@"; else "$@"; fi
}
size_of() { wc -c <"$1" | tr -d ' '; }

# An empty TAG is worse than a failure: `gh release view ""` returns the repo's
# latest published release with exit 0, so the script would read a previous
# release's assets, find them all "present", and report success having
# published nothing.
: "${GH_REPO:?GH_REPO must be set (owner/name)}"
[ -n "$TAG" ] || die "usage: publish.sh vX.Y.Z DIR [NOTES_FILE]"
case "$TAG" in v[0-9]*) ;; *) die "tag '$TAG' is not a version tag like v0.3.1" ;; esac
[ -d "$DIR" ] || die "$DIR is not a directory"
export TAG
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# DIR has to be a flat directory of regular files. A nested layout (an upload glob
# that re-rooted) or a symlink would make `-maxdepth 1` quietly miss files.
if [ -n "$(find "$DIR" -mindepth 1 -maxdepth 1 -not -type f)" ]; then
  die "$DIR holds entries that are not regular files:$(find "$DIR" -mindepth 1 -maxdepth 1 -not -type f -exec printf ' %s' {} +)"
fi
expected=()
while IFS= read -r -d '' f; do expected+=("$f"); done < <(find "$DIR" -mindepth 1 -maxdepth 1 -type f -print0 | sort -z)
[ "${#expected[@]}" -gt 0 ] || die "no files to publish for $TAG"

# GitHub rewrites characters it dislikes in an asset name (a space becomes a dot)
# and `gh release upload` reads `#` as a label separator, so the completeness
# check below could never match. Fail where the message can still name the file.
for f in "${expected[@]}"; do
  case ${f##*/} in
    *[!A-Za-z0-9._+-]*) die "file name is not safe as a release asset: ${f@Q} (allowed: letters, digits, . _ + -)" ;;
  esac
done

# The release must be complete before it is visible: three platforms, the
# checksum list, and its signature, all for this version.
ver=${TAG#v}
for want in "outfit_${ver}_linux_amd64.tar.gz" "outfit_${ver}_linux_arm64.tar.gz" "outfit_${ver}_darwin_all.tar.gz" checksums.txt checksums.txt.minisig; do
  [ -f "$DIR/$want" ] || die "$DIR has no $want; refusing to publish an incomplete release"
done

topup=0
release_field() { cap 60 gh release view "$TAG" --json "$1" --jq ".$1" 2>/dev/null || true; }

# The probe and the create are GitHub calls too, so they retry like the uploads.
open_release() {
  local attempt delay=5 notes=(--generate-notes)
  [ -z "$NOTES" ] || notes=(--notes-file "$NOTES")
  for attempt in 1 2 3; do
    if cap 60 gh release view "$TAG" >/dev/null 2>&1; then
      echo "Release $TAG already exists, topping it up."
      # Only a draft is replaced wholesale; a published release is left alone.
      [ "$(release_field isDraft)" = true ] && topup=1
      return 0
    fi
    if cap 120 gh release create "$TAG" --draft --verify-tag --title "$TAG" "${notes[@]}"; then
      return 0
    fi
    echo "Could not open a release for $TAG (attempt $attempt/3)."
    if [ "$attempt" -lt 3 ]; then nap "$delay"; delay=$((delay * 3)); fi
  done
  return 1
}
open_release || die "could not create or find a release for $TAG"

# A failed read must never degrade to "assume nothing is missing".
read_assets() {
  local attempt delay=5
  for attempt in 1 2 3; do
    if cap 60 gh release view "$TAG" --json assets \
      --jq '.assets[] | select(.state == "uploaded") | "\(.name)\t\(.size)"' >"$work/have.tsv"; then
      return 0
    fi
    echo "Could not read $TAG (attempt $attempt/3)."
    if [ "$attempt" -lt 3 ]; then nap "$delay"; delay=$((delay * 3)); fi
  done
  return 1
}

# Sets `missing`. Every path out reflects a release we actually read, or exits.
refresh_missing() {
  read_assets || die "gave up reading $TAG; leaving it a draft rather than guessing"
  declare -A have=()
  local name size f
  while IFS=$'\t' read -r name size; do have["$name"]="$size"; done <"$work/have.tsv"
  missing=()
  for f in "${expected[@]}"; do
    name=${f##*/}
    # Measured on its own line so a failed measurement aborts: inlined into the
    # comparison it would expand to "", which is also what an absent asset
    # compares as.
    size=$(size_of "$f")
    if [ "${have["$name"]-}" != "$size" ]; then missing+=("$f"); fi
  done
}

backoff=15
for attempt in 1 2 3 4 5; do
  refresh_missing
  if [ "${#missing[@]}" -eq 0 ] && ! { [ "$attempt" -eq 1 ] && [ "$topup" -eq 1 ]; }; then break; fi
  if [ "$attempt" -gt 1 ]; then
    echo "Retrying in ${backoff}s..."
    nap "$backoff"
    backoff=$((backoff * 2))
  fi
  upload=("${missing[@]}")
  if [ "$attempt" -eq 1 ] && [ "$topup" -eq 1 ]; then
    # Same name and same size is not the same file: checksums.txt and its
    # signature have a fixed size in every build, so a draft left by an earlier
    # build would keep its stale copies. Replace everything once.
    upload=("${expected[@]}")
    echo "Topping up an existing release: replacing all ${#expected[@]} asset(s)."
  else
    echo "Attempt $attempt: uploading ${#missing[@]} of ${#expected[@]} asset(s)."
  fi
  # A hang, not an error, is what cost veld v16.57.1. Cap and retry instead of
  # waiting it out. A killed upload leaves a partial asset, which the size/state
  # check catches next pass and --clobber overwrites.
  cap 600 gh release upload "$TAG" "${upload[@]}" --clobber || true
done

refresh_missing
if [ "${#missing[@]}" -ne 0 ]; then
  printf '  %s\n' "${missing[@]}"
  die "$TAG is missing ${#missing[@]} asset(s) after 5 attempts; leaving it a draft"
fi

# The flip is what makes the release exist for users, and it is read back: the API
# accepting the call is not the same fact as the release having left draft.
# `make_latest=legacy`, not gh's `--latest` (which hard-sets true): publishing an
# OLD stranded draft after a newer release shipped must not point
# releases/latest backwards, and install.sh and `outfit update` resolve that URL.
publish() {
  local attempt delay=15 id
  for attempt in 1 2 3 4 5; do
    id=$(release_field databaseId)
    if [ -n "$id" ]; then
      cap 120 gh api -X PATCH "repos/$GH_REPO/releases/$id" \
        -F draft=false -f make_latest=legacy >/dev/null 2>&1 || true
    fi
    if [ "$(release_field isDraft)" = "false" ]; then return 0; fi
    echo "$TAG is not published yet (attempt $attempt/5)."
    if [ "$attempt" -lt 5 ]; then nap "$delay"; delay=$((delay * 2)); fi
  done
  return 1
}

# Deliberately not "it is still a draft": reaching here can also mean the flip
# landed and only the read-back failed.
publish || die "$TAG carries all ${#expected[@]} assets but could not be confirmed published; re-run this job to check"
echo "Published $TAG with ${#expected[@]} assets."
