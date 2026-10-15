#!/usr/bin/env bash
# Exercises install.sh against a fake release served from disk (file:// URLs): the
# public layout and the API layout, a pinned version, a bad checksum, an
# unsupported platform, a hostile archive, and (when a minisign binary is
# installed) good and bad signatures, both with minisign on PATH and with the
# installer fetching its own pinned copy. Set REQUIRE_MINISIGN=1 to fail instead
# of skip when there is none (CI does). Set INSTALL_TEST_NETWORK=1 to also try the
# real pinned download from GitHub.
set -u

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
FAILS=0
PASSES=0

pass() { PASSES=$((PASSES + 1)); echo "ok   $1"; }
fail() { FAILS=$((FAILS + 1)); echo "FAIL $1"; shift; for l in "$@"; do echo "       $l"; done; }

# check NAME CONDITION...: the condition is a command; the last run's output is shown on failure.
check() {
  local name=$1
  shift
  if "$@"; then pass "$name"; else fail "$name" "output: $(head -c 600 "$WORK/out" 2>/dev/null)"; fi
}

sha256_of() { if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi; }

# A PATH holding only the tools the installer may use, so a developer's own gh,
# outfit or minisign cannot change the result.
farm() { # farm DIR [extra tool...]
  mkdir -p "$1"
  local t p
  for t in sh bash curl tar gzip gunzip awk tr sed grep cut wc mktemp mv cp chmod rm dirname uname sha256sum shasum cat mkdir head printf basename unzip "${@:2}"; do
    p=$(command -v "$t" 2>/dev/null || true)
    case "$p" in /*) ln -sf "$p" "$1/$t" ;; esac
  done
}
farm "$WORK/bin"
HAVE_MINISIGN=0
MINISIGN=$(command -v minisign 2>/dev/null || true)
if [ -n "$MINISIGN" ]; then HAVE_MINISIGN=1; farm "$WORK/bin-ms" minisign; fi
[ "$HAVE_MINISIGN" = 1 ] || { [ "${REQUIRE_MINISIGN:-0}" = 1 ] && fail "minisign is installed" "install it (apt install minisign / brew install minisign)"; }

# ── the fake release ────────────────────────────────────────────────────────
VER=0.9.9
REL=$WORK/rel
mkdir -p "$REL/pub/releases/latest/download" "$REL/pub/releases/download/v$VER" "$REL/api/releases/tags" "$REL/api/releases/assets"
mkdir -p "$WORK/src"
cat >"$WORK/src/outfit" <<'EOF'
#!/bin/sh
echo "outfit version 0.9.9 (fake)"
EOF
chmod +x "$WORK/src/outfit"
for f in LICENSE README.md NOTICE; do echo "$f" >"$WORK/src/$f"; done

mkrelease() { # mkrelease DIR: archives for every platform, checksums.txt
  local d=$1 plat
  rm -f "$d"/*
  : >"$d/checksums.txt"
  for plat in linux_amd64 linux_arm64 darwin_all; do
    tar -czf "$d/outfit_${VER}_$plat.tar.gz" -C "$WORK/src" outfit LICENSE README.md NOTICE
    echo "$(sha256_of "$d/outfit_${VER}_$plat.tar.gz")  outfit_${VER}_$plat.tar.gz" >>"$d/checksums.txt"
  done
}
sign() { # sign DIR [KEYDIR]: writes checksums.txt.minisig
  local d=$1 k=${2:-$WORK/key}
  "$MINISIGN" -S -s "$k/sec" -m "$d/checksums.txt" -x "$d/checksums.txt.minisig" </dev/null >/dev/null 2>&1
}
if [ "$HAVE_MINISIGN" = 1 ]; then
  for k in key other; do
    mkdir -p "$WORK/$k"
    "$MINISIGN" -G -W -f -p "$WORK/$k/pub" -s "$WORK/$k/sec" </dev/null >/dev/null 2>&1 ||
      "$MINISIGN" -G -f -p "$WORK/$k/pub" -s "$WORK/$k/sec" </dev/null >/dev/null 2>&1 ||
      { fail "could not generate a throwaway minisign key"; HAVE_MINISIGN=0; }
  done
fi
pubkey_of() { sed -n '2p' "$1/pub"; }

publish_public() { # publish_public: lay DIR out as the public release URLs do
  local d=$WORK/dist
  mkdir -p "$d"
  mkrelease "$d"
  [ "$HAVE_MINISIGN" = 1 ] && sign "$d" || echo "unsigned" >"$d/checksums.txt.minisig"
  rm -rf "$REL/pub/releases/latest/download" "$REL/pub/releases/download/v$VER"
  mkdir -p "$REL/pub/releases/latest/download" "$REL/pub/releases/download/v$VER"
  cp "$d"/* "$REL/pub/releases/latest/download/"
  cp "$d"/* "$REL/pub/releases/download/v$VER/"
}
publish_api() { # publish_api [minify]: lay dist out as the API does
  local d=$WORK/dist i=100 json="" name f sep=""
  rm -rf "$REL/api"
  mkdir -p "$REL/api/releases/tags" "$REL/api/releases/assets"
  for f in "$d"/*; do
    name=$(basename "$f")
    i=$((i + 1))
    cp "$d/$name" "$REL/api/releases/assets/$i"
    json="$json$sep{\"url\":\"https://api.github.com/repos/x/y/releases/assets/$i\",\"id\":$i,\"node_id\":\"RA_$i\",\"name\":\"$name\",\"label\":\"\",\"uploader\":{\"login\":\"bot\",\"id\":1,\"url\":\"https://api.github.com/users/bot\"},\"content_type\":\"application/gzip\"}"
    sep=","
  done
  local body="{\"url\":\"https://api.github.com/repos/x/y/releases/1\",\"assets_url\":\"https://api.github.com/repos/x/y/releases/1/assets\",\"id\":1,\"name\":\"v$VER\",\"tag_name\":\"v$VER\",\"author\":{\"login\":\"bot\",\"url\":\"https://api.github.com/users/bot\"},\"assets\":[$json]}"
  if [ "${1:-}" = minify ]; then printf '%s' "$body" >"$REL/api/releases/latest"
  else printf '%s' "$body" | sed 's/,"/,\n  "/g; s/{"/{\n  "/g' >"$REL/api/releases/latest"; fi
  cp "$REL/api/releases/latest" "$REL/api/releases/tags/v$VER"
}

# run_install ENV... : runs install.sh in a clean environment. Output in $WORK/out, status in $RC.
RC=0
H=$WORK/home
mkdir -p "$H"
run_install() {
  # Arguments before `--` are environment settings; after it, arguments for the script.
  # WITH_MINISIGN=1 puts a minisign on PATH. WITH_SIGNATURE=1 turns the signature check on
  # (it is skipped by default: most cases are not about it, and it would reach for the network).
  local bin=$WORK/bin envs=() args=() seen=0 skip=1 a
  for a in "$@"; do
    if [ "$a" = -- ] && [ $seen = 0 ]; then seen=1
    elif [ $seen = 1 ]; then args+=("$a")
    elif [ "$a" = WITH_MINISIGN=1 ]; then bin=$WORK/bin-ms
    elif [ "$a" = WITH_SIGNATURE=1 ]; then skip=0
    else envs+=("$a")
    fi
  done
  case " ${envs[*]-} " in *" PATH="*) ;; *) envs+=("PATH=$bin") ;; esac
  [ $skip = 0 ] || envs+=("OUTFIT_SKIP_SIGNATURE=1" "OUTFIT_BOOTSTRAP_URL=file:///nonexistent" "OUTFIT_BOOTSTRAP_SHA256=$(printf 0%.0s $(seq 1 64))")
  env -i HOME="$H" TMPDIR="$WORK" "${envs[@]}" sh "$ROOT/install.sh" ${args[@]+"${args[@]}"} >"$WORK/out" 2>&1
  RC=$?
}
base_env() { echo "OUTFIT_BASE_URL=file://$REL/pub"; }
out_has() { grep -qF -- "$1" "$WORK/out"; }
rc_is() { [ "$RC" = "$1" ]; }
installed() { [ -x "$1/outfit" ] && [ "$("$1/outfit" version)" = "outfit version 0.9.9 (fake)" ]; }
nothing_in() { [ ! -e "$1/outfit" ] && ! ls "$1"/.outfit.* >/dev/null 2>&1; }
no_leftover_tmp() { find "$WORK" -mindepth 1 -maxdepth 1 -name "tmp.*" > "$WORK/left"; [ ! -s "$WORK/left" ] || { cat "$WORK/left" >> "$WORK/out"; return 1; }; }

publish_public

# ── public layout ───────────────────────────────────────────────────────────
D=$WORK/d1
run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D"
check "latest installs and runs" rc_is 0
check "  the binary is the fake release" installed "$D"
check "  prints the new version" out_has "outfit version 0.9.9 (fake)"
check "  hints at PATH when the dir is not on it" out_has "export PATH=\"$D:"
check "  says the signature was not checked when told to skip it" out_has "the release signature was NOT checked"
check "  leaves no temp files behind" no_leftover_tmp

D=$WORK/d2
run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" OUTFIT_VERSION=$VER
check "pinned version installs" rc_is 0
run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" OUTFIT_VERSION=v$VER
check "pinned version with a v installs" rc_is 0
D=$WORK/d2b
run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" OUTFIT_VERSION=9.9.9
check "a version that does not exist fails" rc_is 1
check "  and installs nothing" nothing_in "$D"
run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" 'OUTFIT_VERSION=../etc'
check "a version with slashes is refused" rc_is 1
check "  with a clear message" out_has "is not a version number"

# dir already on PATH: no hint. Existing install is updated in place.
D=$WORK/d3
mkdir -p "$D"
printf '#!/bin/sh\necho old\n' >"$D/outfit"
chmod +x "$D/outfit"
ln -sf "$WORK/bin"/* "$D/" 2>/dev/null || true
run_install "$(base_env)" OUTFIT_INSTALL_DIR="$WORK/elsewhere" "PATH=$D:$WORK/bin"
check "an existing install is replaced where it is" installed "$D"
check "  without a PATH hint" bash -c '! grep -q "not on your PATH" "$1"' _ "$WORK/out"
check "  and says where OUTFIT_INSTALL_DIR went" out_has "already installed in $D"
check "  the other dir was never created" bash -c '[ ! -e "$1" ]' _ "$WORK/elsewhere"

# `curl | sh`: the script arrives on stdin, so nothing in it may read stdin.
D=$WORK/pipe
env -i HOME="$H" PATH="$WORK/bin" TMPDIR="$WORK" OUTFIT_SKIP_SIGNATURE=1 OUTFIT_BOOTSTRAP_URL=file:///nonexistent OUTFIT_BOOTSTRAP_SHA256=0000000000000000000000000000000000000000000000000000000000000000 OUTFIT_BASE_URL="file://$REL/pub" OUTFIT_INSTALL_DIR="$D" \
  sh -c "cat '$ROOT/install.sh' | sh" >"$WORK/out" 2>&1
RC=$?
check "installing through a pipe (curl | sh) works" rc_is 0
check "  and installs the binary" installed "$D"

# An existing install in a folder we cannot write to (never as root, where nothing is read-only).
if [ "$(id -u)" != 0 ]; then
  RO=$WORK/ro
  mkdir -p "$RO"
  printf '#!/bin/sh\necho old\n' >"$RO/outfit"
  chmod +x "$RO/outfit"
  chmod 555 "$RO"
  D=$WORK/mine
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" "PATH=$RO:$WORK/bin"
  check "a read-only existing install falls back to OUTFIT_INSTALL_DIR" installed "$D"
  check "  and warns about PATH order" out_has "Put that folder before"
  run_install "$(base_env)" "PATH=$RO:$WORK/bin"
  check "a read-only existing install with no OUTFIT_INSTALL_DIR fails" rc_is 1
  check "  and names the fix" out_has "Re-run with OUTFIT_INSTALL_DIR"
  chmod 755 "$RO"
fi

# ── checksum, platform, hostile archive ─────────────────────────────────────
D=$WORK/d4
mkdir -p "$D"
printf '#!/bin/sh\necho precious\n' >"$D/outfit"
chmod +x "$D/outfit"
cp "$WORK/dist/checksums.txt" "$WORK/checksums.good"
for f in "$REL"/pub/releases/latest/download/outfit_*.tar.gz; do printf 'tampered' >>"$f"; done
run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" "PATH=$WORK/bin"
check "a corrupted download is refused" rc_is 1
check "  with the checksum named" out_has "SHA-256 mismatch"
check "  and the existing binary is untouched" bash -c '[ "$("$1")" = precious ]' _ "$D/outfit"
check "  and no staging file is left" bash -c '! ls "$1"/.outfit.* >/dev/null 2>&1' _ "$D"
publish_public

mkdir -p "$WORK/fakebin"
farm "$WORK/fakebin-uname"
rm -f "$WORK/fakebin-uname/uname"
cat >"$WORK/fakebin-uname/uname" <<'EOF'
#!/bin/sh
case "$1" in -s) echo FreeBSD ;; -m) echo riscv64 ;; *) echo FreeBSD ;; esac
EOF
chmod +x "$WORK/fakebin-uname/uname"
D=$WORK/d5
env -i HOME="$H" PATH="$WORK/fakebin-uname" OUTFIT_SKIP_SIGNATURE=1 OUTFIT_BOOTSTRAP_URL=file:///nonexistent OUTFIT_BOOTSTRAP_SHA256=0000000000000000000000000000000000000000000000000000000000000000 OUTFIT_BASE_URL="file://$REL/pub" OUTFIT_INSTALL_DIR="$D" sh "$ROOT/install.sh" >"$WORK/out" 2>&1
RC=$?
check "an unsupported platform fails clearly" rc_is 1
check "  naming the platform" out_has "no outfit build for FreeBSD/riscv64"
check "  and installs nothing" nothing_in "$D"

# A hostile archive with a good checksum: the allow-list must still refuse it.
hostile() { # hostile NAME BUILD-COMMAND
  local name=$1 h=$WORK/hostile
  rm -rf "$h"
  mkdir -p "$h/src"
  cp "$WORK/src"/* "$h/src/"
  (cd "$h/src" && eval "$2")
  for plat in linux_amd64 linux_arm64 darwin_all; do
    (cd "$h/src" && tar -czf "$REL/pub/releases/latest/download/outfit_${VER}_$plat.tar.gz" "${@:3}" outfit LICENSE README.md NOTICE $3)
  done
  (cd "$REL/pub/releases/latest/download" && : >checksums.txt && for plat in linux_amd64 linux_arm64 darwin_all; do echo "$(sha256_of "outfit_${VER}_$plat.tar.gz")  outfit_${VER}_$plat.tar.gz" >>checksums.txt; done)
  [ "$HAVE_MINISIGN" = 1 ] && sign "$REL/pub/releases/latest/download"
  D=$WORK/dh
  rm -rf "$D"
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D"
  check "hostile archive ($name) is refused" rc_is 1
  check "  and installs nothing" nothing_in "$D"
}
hostile "extra file" 'echo x > evil' evil
hostile "symlink" 'ln -s /etc/passwd link' link
publish_public

run_install "$(base_env)" OUTFIT_INSTALL_DIR="$WORK/d6" -- --extra
check "an unknown argument is refused" rc_is 1

# ── token / API layout ──────────────────────────────────────────────────────
rm -rf "$REL/pub"
publish_api
TOKEN=ghp_FAKEtoken0123456789
for how in "OUTFIT_GITHUB_TOKEN" "GH_TOKEN" "GITHUB_TOKEN"; do
  D=$WORK/api-$how
  run_install OUTFIT_API_URL="file://$REL/api" OUTFIT_BASE_URL="file://$REL/nothing" OUTFIT_INSTALL_DIR="$D" "$how=$TOKEN"
  check "token from $how: installs through the API" rc_is 0
  check "  and never prints the token" bash -c '! grep -q "$1" "$2"' _ "$TOKEN" "$WORK/out"
  check "  and the binary works" installed "$D"
done
publish_api minify
D=$WORK/api-min
run_install OUTFIT_API_URL="file://$REL/api" OUTFIT_BASE_URL="file://$REL/nothing" OUTFIT_INSTALL_DIR="$D" GH_TOKEN=$TOKEN
check "a minified API response installs too" rc_is 0
D=$WORK/api-pin
run_install OUTFIT_API_URL="file://$REL/api" OUTFIT_BASE_URL="file://$REL/nothing" OUTFIT_INSTALL_DIR="$D" GH_TOKEN=$TOKEN OUTFIT_VERSION=$VER
check "a pinned version installs through the API" rc_is 0
D=$WORK/api-missing
run_install OUTFIT_API_URL="file://$REL/api" OUTFIT_BASE_URL="file://$REL/nothing" OUTFIT_INSTALL_DIR="$D" GH_TOKEN=$TOKEN OUTFIT_VERSION=9.9.9
check "a missing release fails" rc_is 1
check "  and installs nothing" nothing_in "$D"
# A validly signed OLDER build re-uploaded under a newer release is refused.
sed 's/"tag_name":"v'$VER'"/"tag_name":"v9.9.10"/; s/"tag_name": *"v'$VER'"/"tag_name": "v9.9.10"/' "$REL/api/releases/latest" >"$WORK/latest.json"
cp "$REL/api/releases/latest" "$WORK/latest.orig"
cp "$WORK/latest.json" "$REL/api/releases/latest"
D=$WORK/api-replay
run_install OUTFIT_API_URL="file://$REL/api" OUTFIT_BASE_URL="file://$REL/nothing" OUTFIT_INSTALL_DIR="$D" GH_TOKEN=$TOKEN
check "a build that is not the release's own is refused" rc_is 1
check "  with the reason" out_has "not its own build"
check "  and nothing was installed" nothing_in "$D"
cp "$WORK/latest.orig" "$REL/api/releases/latest"
D=$WORK/api-http
run_install OUTFIT_API_URL="http://evil.example/api" OUTFIT_BASE_URL="file://$REL/nothing" OUTFIT_INSTALL_DIR="$D" GH_TOKEN=$TOKEN
check "a token is never sent over plain http" rc_is 1
check "  with the reason" out_has "refusing to send a GitHub token"
for evil in "https://evil.example/api" "https://api.github.com.evil.example/api" "http://localhost:80@evil.example/api" "https://api.github.com@evil.example/api"; do
  D=$WORK/api-evil
  run_install OUTFIT_API_URL="$evil" OUTFIT_BASE_URL="file://$REL/nothing" OUTFIT_INSTALL_DIR="$D" GH_TOKEN=$TOKEN
  check "a token is not sent to $evil" out_has "refusing to send a GitHub token"
done
# gh as the last-resort token source
mkdir -p "$WORK/ghbin"
cp -P "$WORK/bin"/* "$WORK/ghbin/"
printf '#!/bin/sh\n[ "$1 $2" = "auth token" ] && echo %s\n' "$TOKEN" >"$WORK/ghbin/gh"
chmod +x "$WORK/ghbin/gh"
D=$WORK/api-gh
env -i HOME="$H" PATH="$WORK/ghbin" OUTFIT_SKIP_SIGNATURE=1 OUTFIT_BOOTSTRAP_URL=file:///nonexistent OUTFIT_BOOTSTRAP_SHA256=0000000000000000000000000000000000000000000000000000000000000000 OUTFIT_API_URL="file://$REL/api" OUTFIT_BASE_URL="file://$REL/nothing" OUTFIT_INSTALL_DIR="$D" sh "$ROOT/install.sh" >"$WORK/out" 2>&1
RC=$?
check "gh auth token is used when no variable is set" rc_is 0
check "  and not printed" bash -c '! grep -q "$1" "$2"' _ "$TOKEN" "$WORK/out"
D=$WORK/api-badtoken
run_install OUTFIT_API_URL="file://$REL/api" OUTFIT_BASE_URL="file://$REL/nothing" OUTFIT_INSTALL_DIR="$D" 'GH_TOKEN=bad token"quote'
check "a token with odd characters is ignored (and the public URLs are tried)" rc_is 1
check "  with the token-needed hint" out_has "needs a token"

# ── minisign ────────────────────────────────────────────────────────────────
if [ "$HAVE_MINISIGN" = 1 ]; then
  publish_public
  D=$WORK/ms1
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_MINISIGN=1 WITH_SIGNATURE=1 OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/key")"
  check "a good signature is accepted" rc_is 0
  check "  and reported" out_has "signature OK (minisign on this machine)"
  D=$WORK/ms2
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_MINISIGN=1 WITH_SIGNATURE=1 OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/other")"
  check "a signature by another key is refused" rc_is 1
  check "  naming the key problem" out_has "does not match the outfit release key"
  check "  and nothing was installed" nothing_in "$D"
  D=$WORK/ms3
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_MINISIGN=1 WITH_SIGNATURE=1 OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/other") $(pubkey_of "$WORK/key")"
  check "any one of several trusted keys is enough (key rotation)" rc_is 0
  # The embedded key is the real release key, not the throwaway one.
  D=$WORK/ms4
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_MINISIGN=1 WITH_SIGNATURE=1
  check "the embedded key rejects a throwaway signature" rc_is 1
  # Checksum list edited after signing, with a matching archive hash: only the signature can catch it.
  D=$WORK/ms5
  printf '\n' >>"$REL/pub/releases/latest/download/checksums.txt"
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_MINISIGN=1 WITH_SIGNATURE=1 OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/key")"
  check "a checksum list changed after signing is refused" rc_is 1

  # ── no minisign on the machine: the installer fetches its own, pinned by hash ──
  publish_public
  # A stand-in for the upstream package, laid out the way the real ones are: a zip
  # with `minisign` on macOS, a tar.gz with minisign-linux/<arch>/minisign elsewhere.
  PKG=$WORK/boot
  rm -rf "$PKG"
  mkdir -p "$PKG/minisign-linux/x86_64" "$PKG/minisign-linux/aarch64"
  cp "$MINISIGN" "$PKG/minisign"
  cp "$MINISIGN" "$PKG/minisign-linux/x86_64/minisign"
  cp "$MINISIGN" "$PKG/minisign-linux/aarch64/minisign"
  if [ "$(uname -s)" = Darwin ]; then
    (cd "$PKG" && zip -q ../boot.pkg minisign)
  else
    (cd "$PKG" && tar -czf ../boot.pkg minisign-linux)
  fi
  BOOT_SHA=$(sha256_of "$WORK/boot.pkg")
  boot_env() { echo "OUTFIT_BOOTSTRAP_URL=file://$WORK/boot.pkg"; }
  D=$WORK/bs1
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_SIGNATURE=1 "$(boot_env)" OUTFIT_BOOTSTRAP_SHA256="$BOOT_SHA" OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/key")"
  check "no minisign installed: the pinned download checks the signature" rc_is 0
  check "  and says so" out_has "signature OK (pinned minisign download)"
  check "  and the binary is installed" installed "$D"
  check "  and leaves no temp files behind" no_leftover_tmp
  D=$WORK/bs2
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_SIGNATURE=1 "$(boot_env)" OUTFIT_BOOTSTRAP_SHA256="$BOOT_SHA" OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/other")"
  check "  a signature by another key is still refused" rc_is 1
  check "    naming the key problem" out_has "does not match the outfit release key"
  check "    and nothing was installed" nothing_in "$D"
  D=$WORK/bs3
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_SIGNATURE=1 "$(boot_env)" "OUTFIT_BOOTSTRAP_SHA256=$(printf 'a%.0s' $(seq 1 64))" OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/key")"
  check "  a download that does not match the pinned hash is never run" rc_is 1
  check "    and the refusal says so, without suggesting to skip the check" out_has "does not match the SHA-256 pinned"
  check "    (no skip hint)" bash -c '! grep -q OUTFIT_SKIP_SIGNATURE "$1"' _ "$WORK/out"
  check "    and nothing was installed" nothing_in "$D"
  D=$WORK/bs3b
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_SIGNATURE=1 "$(boot_env)" "OUTFIT_BOOTSTRAP_SHA256=$(printf 'a%.0s' $(seq 1 64))" OUTFIT_SKIP_SIGNATURE=1
  check "    OUTFIT_SKIP_SIGNATURE=1 is the person's own call, and warns" out_has "NOT checked"
  D=$WORK/bs4
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_SIGNATURE=1 "OUTFIT_BOOTSTRAP_URL=file://$WORK/does-not-exist" OUTFIT_BOOTSTRAP_SHA256="$BOOT_SHA" OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/key")"
  check "  a download that fails refuses to install" rc_is 1
  check "    saying what failed and the way out" out_has "could not download"
  check "    and nothing was installed" nothing_in "$D"
  D=$WORK/bs5
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_SIGNATURE=1 "OUTFIT_BOOTSTRAP_URL=file://$WORK/does-not-exist" OUTFIT_SKIP_SIGNATURE=1
  check "  OUTFIT_SKIP_SIGNATURE=1 installs on the SHA-256 alone" rc_is 0
  check "    with a warning" out_has "NOT checked"
  D=$WORK/bs5b
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_SIGNATURE=1 "$(boot_env)" OUTFIT_BOOTSTRAP_SHA256="$BOOT_SHA" OUTFIT_SKIP_SIGNATURE=1 OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/other")"
  check "  OUTFIT_SKIP_SIGNATURE=1 never skips a check that can run (bad signature still refused)" rc_is 1
  check "    and nothing was installed" nothing_in "$D"
  D=$WORK/bs5c
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_MINISIGN=1 WITH_SIGNATURE=1 OUTFIT_SKIP_SIGNATURE=1 OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/other")"
  check "  nor with minisign on PATH" rc_is 1
  # A download that matches its hash but cannot run is not a tampered release.
  UN=$WORK/unrunnable
  rm -rf "$UN"
  mkdir -p "$UN/minisign-linux/x86_64" "$UN/minisign-linux/aarch64"
  for f in minisign minisign-linux/x86_64/minisign minisign-linux/aarch64/minisign; do printf '#!/bin/sh\nexit 126\n' >"$UN/$f"; done
  if [ "$(uname -s)" = Darwin ]; then (cd "$UN" && zip -q ../unrunnable.pkg minisign); else (cd "$UN" && tar -czf ../unrunnable.pkg minisign-linux); fi
  D=$WORK/bs5d
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_SIGNATURE=1 "OUTFIT_BOOTSTRAP_URL=file://$WORK/unrunnable.pkg" OUTFIT_BOOTSTRAP_SHA256="$(sha256_of "$WORK/unrunnable.pkg")" OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/key")"
  check "  a download that cannot run is not reported as a bad signature" rc_is 1
  check "    it says it cannot run here" out_has "cannot run here"
  check "    and names the way out" out_has "OUTFIT_SKIP_SIGNATURE=1"
  check "    and does not claim a key mismatch" bash -c '! grep -q "does not match the outfit release key" "$1"' _ "$WORK/out"
  # The Linux member is chosen by architecture: the x86_64 one is a stub that cannot
  # run, the aarch64 one is the real thing, and vice versa.
  LP=$WORK/linuxpkg
  for arch in x86_64 aarch64; do
    rm -rf "$LP" "$WORK/linux-$arch.pkg"
    mkdir -p "$LP/minisign-linux/x86_64" "$LP/minisign-linux/aarch64"
    cp "$MINISIGN" "$LP/minisign-linux/x86_64/minisign"
    cp "$MINISIGN" "$LP/minisign-linux/aarch64/minisign"
    other=aarch64
    [ "$arch" = aarch64 ] && other=x86_64
    rm -f "$LP/minisign-linux/$other/minisign"
    printf '#!/bin/sh\nexit 126\n' >"$LP/minisign-linux/$other/minisign"
    (cd "$LP" && tar -czf "$WORK/linux-$arch.pkg" minisign-linux)
    FU=$WORK/fakeuname-$arch
    farm "$FU" minisign
    rm -f "$FU/uname"
    printf '#!/bin/sh\ncase "$1" in -s) echo Linux ;; -m) echo %s ;; *) echo Linux ;; esac\n' "$arch" >"$FU/uname"
    chmod +x "$FU/uname"
    rm -f "$FU/minisign"
    D=$WORK/bs-$arch
    env -i HOME="$H" PATH="$FU" TMPDIR="$WORK" OUTFIT_BASE_URL="file://$REL/pub" OUTFIT_INSTALL_DIR="$D" \
      OUTFIT_BOOTSTRAP_URL="file://$WORK/linux-$arch.pkg" OUTFIT_BOOTSTRAP_SHA256="$(sha256_of "$WORK/linux-$arch.pkg")" \
      OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/key")" sh "$ROOT/install.sh" >"$WORK/out" 2>&1
    RC=$?
    check "  Linux $arch uses the $arch member of the package" rc_is 0
    check "    and says the pinned download checked it" out_has "signature OK (pinned minisign download)"
  done
  D=$WORK/bs6
  run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_MINISIGN=1 WITH_SIGNATURE=1 "OUTFIT_BOOTSTRAP_URL=file://$WORK/does-not-exist" OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/key")"
  check "  a minisign on PATH is used without any download" rc_is 0
  if [ "${INSTALL_TEST_NETWORK:-0}" = 1 ]; then
    D=$WORK/bs7
    run_install "$(base_env)" OUTFIT_INSTALL_DIR="$D" WITH_SIGNATURE=1 OUTFIT_MINISIGN_PUBKEY="$(pubkey_of "$WORK/key")"
    check "the real pinned minisign downloads, matches its hash and verifies" rc_is 0
    check "  and says so" out_has "signature OK (pinned minisign download)"
  fi
else
  echo "skip minisign cases (no minisign installed)"
fi

echo
echo "install-test: $PASSES passed, $FAILS failed"
[ "$FAILS" -eq 0 ]
