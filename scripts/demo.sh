#!/usr/bin/env bash
# A walkthrough of outfit in a throwaway git repository, about a minute long.
# Every step is printed before it runs, and the script checks what it claims, so
# CI runs it too (`scripts/demo.sh --quick`).
#
#   scripts/demo.sh [--quick] [--outfit PATH]
#
#   --quick        shorten the fake hook's delay (CI)
#   --outfit PATH  the binary to demo (default: `outfit` on PATH, else a fresh build)
#
# It never touches your own git config: HOME points into the temp dir.
set -euo pipefail

quick=0
bin=""
while [ $# -gt 0 ]; do
  case "$1" in
    --quick) quick=1 ;;
    --outfit) shift; bin=${1:?--outfit needs a path} ;;
    -h | --help) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1 (try --help)" >&2; exit 2 ;;
  esac
  shift
done

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if [ -z "$bin" ]; then
  if command -v outfit >/dev/null 2>&1; then
    bin=$(command -v outfit)
  elif [ -f "$root/cmd/outfit/main.go" ] && command -v go >/dev/null 2>&1; then
    echo "(no outfit on PATH: building one from this checkout)"
    (cd "$root" && go build -o "$work/outfit-bin" ./cmd/outfit)
    bin=$work/outfit-bin
  else
    echo "outfit is not installed. See the README for the one-line install." >&2
    exit 1
  fi
fi
mkdir -p "$work/bin"
ln -sf "$bin" "$work/bin/outfit"
export PATH="$work/bin:$PATH"

# A private home, so `outfit set --global` and git's own config stay yours.
export HOME="$work/home"
export GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
export GIT_CONFIG_NOSYSTEM=1
export OUTFIT_NO_UPDATE_CHECK=1
mkdir -p "$HOME"
git config --global user.name "Demo"
git config --global user.email "demo@example.com"
git config --global init.defaultBranch main

delay=2
[ "$quick" = 1 ] && delay=0.3

step() { printf '\n\033[1m▶ %s\033[0m\n' "$*"; }
run() { printf '  $'; printf ' %q' "$@"; printf '\n'; "$@" 2>&1 | sed 's/^/    /'; }
fail() { printf '\n\033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }
runs() { wc -l <"$work/biome.log" | tr -d ' '; }

step "A throwaway repository with a husky-style pre-commit hook and a deliberately slow fake 'biome'"
repo=$work/repo
mkdir -p "$repo/.husky" "$repo/tools"
cd "$repo"
git init -q
cat >tools/biome <<BIOME
#!/bin/sh
# Stands in for a slow linter.
echo "biome ran" >>"$work/biome.log"
echo "  biome: checking staged files (takes a while)..."
sleep $delay
BIOME
printf '#!/bin/sh\n./tools/biome\n' >.husky/pre-commit
chmod +x tools/biome .husky/pre-commit
git config core.hooksPath .husky
: >"$work/biome.log"
echo "hello" >file.txt
git add -A
run git commit -m "first commit (the hook runs)"
[ "$(runs)" = 1 ] || fail "the hook should have run once"
first=$(git rev-parse HEAD)

step "outfit init: git keeps running your hooks, now through outfit"
run outfit init
run outfit status
echo "more" >>file.txt
git add -A
run git commit -m "second commit (the hook still runs)"
[ "$(runs)" = 2 ] || fail "the hook should still run after init"

step "Skip the slow hook just for you, without touching the team's files"
run outfit set hook.pre-commit=off
echo "again" >>file.txt
git add -A
run git commit -m "third commit (no biome)"
[ "$(runs)" = 2 ] || fail "hook.pre-commit=off should have skipped biome"
git diff --quiet "$first" HEAD -- .husky tools || fail "the team's hook files must be untouched"

step "Turn it back on (an empty value unsets the override)"
run outfit set hook.pre-commit=
echo "once more" >>file.txt
git add -A
run git commit -m "fourth commit (biome is back)"
[ "$(runs)" = 3 ] || fail "the hook should run again once the override is gone"

step "outfit uninstall: everything init added is removed"
run outfit uninstall
[ "$(git config core.hooksPath)" = .husky ] || fail "core.hooksPath should be back to .husky"
echo "last" >>file.txt
git add -A
run git commit -m "fifth commit (husky directly)"
[ "$(runs)" = 4 ] || fail "husky should keep working without outfit"

printf '\n\033[32m✓ The demo checked everything it showed.\033[0m\n'
