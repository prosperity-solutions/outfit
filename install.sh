#!/bin/sh
# Install outfit from a GitHub release. Ported in spirit from veld's installer
# (MIT; see NOTICE).
#
#   curl -fsSL -H "Authorization: Bearer $(gh auth token)" \
#     https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
#
# While the repository is private the download needs a GitHub token that can read
# it. The script finds one the way `outfit update` will: OUTFIT_GITHUB_TOKEN,
# GH_TOKEN, GITHUB_TOKEN, then `gh auth token`. With none, it uses the public
# release URLs, which work once the repository is public.
#
# Environment:
#   OUTFIT_VERSION        Install this release (for example 0.3.1) instead of the latest.
#   OUTFIT_INSTALL_DIR    Where to put the binary when outfit is not installed yet
#                         (default ~/.local/bin). An existing install is replaced in
#                         place, wherever `command -v outfit` finds it.
#   OUTFIT_GITHUB_TOKEN   A GitHub token with read access to the repository.
#   OUTFIT_BASE_URL       Public download base (default https://github.com/prosperity-solutions/outfit).
#                         For tests.
#   OUTFIT_API_URL        API base (default https://api.github.com/repos/prosperity-solutions/outfit).
#                         For tests.
#   OUTFIT_MINISIGN_PUBKEY  Replaces the embedded signing key(s) below. For tests.
#
# A release always carries checksums.txt and checksums.txt.minisig; both are
# downloaded even when minisign is not installed.
# It checks the SHA-256 of the download (which catches a damaged download, not a
# tampered release: the checksum list comes from the same place as the archive) and,
# when a `minisign` binary is on PATH, the signature of the checksum list against the
# release key below (which does catch tampering). Install minisign first if you can.
# It never uses sudo.

set -eu

REPO=prosperity-solutions/outfit
# Keep in step with internal/update/keys.txt (tests/validate-install-contract.sh).
PUBKEYS="RWQwzESR8TzokwCh60NclFts0vEw5Co4E72Bdf8LX03rafQUTVhhKUWU"

tmp=""
stage=""

say() { printf '%s\n' "$*"; }
err() { printf 'outfit install: %s\n' "$*" >&2; exit 1; }

cleanup() {
  [ -z "$stage" ] || rm -f "$stage"
  [ -z "$tmp" ] || rm -rf "$tmp"
}

# Detect the release platform name.
platform() {
  os=$(uname -s)
  arch=$(uname -m)
  case "$os/$arch" in
    Linux/x86_64 | Linux/amd64) echo linux_amd64 ;;
    Linux/aarch64 | Linux/arm64) echo linux_arm64 ;;
    Darwin/x86_64 | Darwin/arm64) echo darwin_all ;;
    *) err "no outfit build for $os/$arch (there are linux amd64/arm64 and macOS builds)" ;;
  esac
}

find_token() {
  token_source=""
  for name in OUTFIT_GITHUB_TOKEN GH_TOKEN GITHUB_TOKEN; do
    eval "token=\${$name:-}"
    if [ -n "$token" ]; then token_source=$name; break; fi
  done
  if [ -z "$token" ] && command -v gh >/dev/null 2>&1; then
    token=$(gh auth token --hostname github.com 2>/dev/null || true)
    [ -z "$token" ] || token_source="gh auth token"
  fi
  # Tokens are [A-Za-z0-9_] plus dots and dashes. Anything else would break out of
  # the curl config line below, so treat it as no token.
  case "$token" in
    *[!A-Za-z0-9_.-]*) say "ignoring the GitHub token from $token_source: it has unexpected characters" >&2; token="" ;;
  esac
}

# fetch_public URL OUT
fetch_public() {
  curl -fsSL --retry 3 --retry-delay 2 -o "$2" "$1" ||
    err "could not download $1 (a private repository needs a token: set OUTFIT_GITHUB_TOKEN or run \`gh auth login\`)"
}

# fetch_api URL OUT ACCEPT. The token travels in a curl config on stdin, not in
# argv, so it never shows up in `ps`. curl drops the Authorization header when a
# redirect leaves the host, so the asset redirect to storage does not carry it.
fetch_api() {
  printf 'header = "Authorization: Bearer %s"\n' "$token" |
    curl -fsSL --retry 3 --retry-delay 2 -K - -H "Accept: $3" -H "X-GitHub-Api-Version: 2022-11-28" -o "$2" "$1" ||
    err "could not download $1 with the GitHub token from $token_source (does it have read access to $REPO? A stale GH_TOKEN or GITHUB_TOKEN wins over \`gh auth\`: unset it, or set OUTFIT_GITHUB_TOKEN to the one you want)"
}

# asset_list JSON prints "<id> <name>" for each release asset.
asset_list() {
  tr ',' '\n' <"$1" | awk '
    /"url" *: *"[^"]*\/releases\/assets\/[0-9]+"/ {
      s = $0; sub(/.*\/releases\/assets\//, "", s); sub(/".*/, "", s); id = s; next }
    id != "" && /^ *\{? *"name" *: *"/ {
      s = $0; sub(/^[^:]*: *"/, "", s); sub(/".*/, "", s); print id, s; id = "" }'
}

# get NAME: download one release asset into $tmp.
get() {
  if [ -n "$token" ]; then
    id=$(awk -v n="$1" '$2 == n { print $1; exit }' "$tmp/assets.txt")
    [ -n "$id" ] || err "the release has no asset named $1"
    fetch_api "$api/releases/assets/$id" "$tmp/$1" "application/octet-stream"
  else
    fetch_public "$assets/$1" "$tmp/$1"
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{ print $1 }'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{ print $1 }'
  else
    err "need sha256sum or shasum to check the download"
  fi
}

verify_signature() {
  command -v minisign >/dev/null 2>&1 || {
    say "note: minisign is not installed, so only the checksum was checked (brew install minisign)"
    return 0
  }
  for key in ${OUTFIT_MINISIGN_PUBKEY:-$PUBKEYS}; do
    if minisign -V -m "$tmp/checksums.txt" -x "$tmp/checksums.txt.minisig" -P "$key" >/dev/null 2>&1; then
      say "signature OK"
      return 0
    fi
  done
  err "the signature on checksums.txt does not match the outfit release key; nothing was installed"
}

# Only these files, as plain files, with plain names.
check_archive() {
  tar -tzf "$1" >"$tmp/entries.txt" || err "$2 is not a readable tar.gz"
  while IFS= read -r entry; do
    case "$entry" in
      outfit | LICENSE | README.md | NOTICE) ;;
      *) err "unexpected entry '$entry' in $2; nothing was installed" ;;
    esac
  done <"$tmp/entries.txt"
  grep -qx outfit "$tmp/entries.txt" || err "$2 has no outfit binary"
  tar -tvzf "$1" | cut -c1 | grep -qv -- '-' && err "$2 holds something other than plain files; nothing was installed"
  return 0
}

install_dir() {
  existing=$(command -v outfit 2>/dev/null || true)
  case "$existing" in
    /*)
      dir=$(dirname "$existing")
      if [ -w "$dir" ]; then
        if [ -n "${OUTFIT_INSTALL_DIR:-}" ] && [ "$OUTFIT_INSTALL_DIR" != "$dir" ]; then
          say "note: outfit is already installed in $dir, so it is updated there (OUTFIT_INSTALL_DIR is only for a first install)" >&2
        fi
        echo "$dir"
      elif [ -n "${OUTFIT_INSTALL_DIR:-}" ]; then
        say "note: $dir (where outfit is installed) is not writable, so it goes to $OUTFIT_INSTALL_DIR. Put that folder before $dir on your PATH, or the old one wins." >&2
        echo "$OUTFIT_INSTALL_DIR"
      else
        err "outfit is installed in $dir, which you cannot write to (this installer never uses sudo). Re-run with OUTFIT_INSTALL_DIR=<a folder you own> and put it first on your PATH."
      fi ;;
    *) echo "${OUTFIT_INSTALL_DIR:-$HOME/.local/bin}" ;;
  esac
}

main() {
  [ $# -eq 0 ] || err "unknown argument '$1' (the installer takes none; options are environment variables, see the top of this script)"
  command -v curl >/dev/null 2>&1 || err "curl is required"
  command -v tar >/dev/null 2>&1 || err "tar is required"
  # In POSIX sh a trapped signal does not end the script by itself.
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  trap 'exit 129' HUP

  plat=$(platform)
  ver=${OUTFIT_VERSION:-}
  ver=${ver#v}
  case "$ver" in
    *[!0-9A-Za-z.+-]*) err "OUTFIT_VERSION '$ver' is not a version number like 0.3.1" ;;
  esac
  token=""
  find_token
  tmp=$(mktemp -d)

  base=${OUTFIT_BASE_URL:-https://github.com/$REPO}
  api=${OUTFIT_API_URL:-https://api.github.com/repos/$REPO}
  if [ -n "$token" ]; then
    # The token goes to the API URL and nowhere else, and never in the clear.
    case "$api" in
      https://* | file://* | http://localhost[:/]* | http://127.0.0.1[:/]*) ;;
      *) err "refusing to send a GitHub token to $api (only https URLs; OUTFIT_API_URL is for tests)" ;;
    esac
    rel=latest
    [ -z "$ver" ] || rel=tags/v$ver
    fetch_api "$api/releases/$rel" "$tmp/release.json" "application/vnd.github+json"
    asset_list "$tmp/release.json" >"$tmp/assets.txt"
    [ -s "$tmp/assets.txt" ] || err "that release lists no downloadable files (is it still a draft?)"
    # The build must be the one the release is named for: a validly signed older
    # build re-uploaded under a newer release would otherwise pass every check.
    tag=$(tr ',' '\n' <"$tmp/release.json" | awk '/"tag_name" *:/ { s = $0; sub(/^[^:]*: *"/, "", s); sub(/".*/, "", s); print s; exit }')
    case "$tag" in v[0-9]*) ;; *) err "the release has no usable tag name" ;; esac
  elif [ -n "$ver" ]; then
    assets=$base/releases/download/v$ver
  else
    assets=$base/releases/latest/download
  fi

  get checksums.txt
  archive=$(awk -v p="$plat" '$2 ~ "^outfit_[0-9A-Za-z.+-]+_" p "\\.tar\\.gz$" { print $2 }' "$tmp/checksums.txt")
  if [ -z "$archive" ] || [ "$(printf '%s\n' "$archive" | wc -l)" -ne 1 ]; then
    err "checksums.txt does not list exactly one $plat build"
  fi
  if [ -n "$ver" ] && [ "$archive" != "outfit_${ver}_$plat.tar.gz" ]; then
    err "asked for $ver but the release holds $archive"
  fi
  if [ -n "$token" ] && [ "$archive" != "outfit_${tag#v}_$plat.tar.gz" ]; then
    err "release $tag holds $archive, which is not its own build; nothing was installed"
  fi
  name=${archive#outfit_}
  say "Installing outfit ${name%_"$plat".tar.gz} for $plat"

  get "$archive"
  get checksums.txt.minisig
  want=$(awk -v n="$archive" '$2 == n { print $1 }' "$tmp/checksums.txt")
  case "$want" in
    *[!0-9a-f]* | "") err "checksums.txt has no valid SHA-256 for $archive" ;;
  esac
  got=$(sha256_of "$tmp/$archive")
  [ "$got" = "$want" ] || err "SHA-256 mismatch for $archive (expected $want, got $got); nothing was installed"
  verify_signature
  check_archive "$tmp/$archive" "$archive"

  mkdir "$tmp/x"
  tar -xzf "$tmp/$archive" -C "$tmp/x"
  if [ ! -f "$tmp/x/outfit" ] || [ -L "$tmp/x/outfit" ]; then err "$archive has no plain outfit file"; fi

  dir=$(install_dir)
  mkdir -p "$dir" 2>/dev/null || true
  stage=$(mktemp "$dir/.outfit.XXXXXX" 2>/dev/null) ||
    err "cannot write to $dir (this installer never uses sudo; set OUTFIT_INSTALL_DIR to a folder you own)"
  cp "$tmp/x/outfit" "$stage"
  chmod 755 "$stage"
  mv -f "$stage" "$dir/outfit"
  stage=""

  case ":$PATH:" in
    *":$dir:"*) ;;
    *)
      rc=~/.profile
      case "${SHELL:-}" in */zsh) rc=~/.zshrc ;; */bash) rc=~/.bashrc ;; esac
      say "note: $dir is not on your PATH. For this terminal:  export PATH=\"$dir:\$PATH\""
      say "      To keep it, add that line to $rc" ;;
  esac
  "$dir/outfit" version
}

main "$@"
