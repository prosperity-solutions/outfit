#!/usr/bin/env bash
# A walkthrough of outfit in a throwaway git repository, about half a minute long (a few
# seconds less with --quick).
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
    echo "outfit was not found on PATH. If you just installed it, add ~/.local/bin to PATH (the installer printed the line)" >&2
    echo "or pass the binary: scripts/demo.sh --outfit ~/.local/bin/outfit. Not installed yet? See the README." >&2
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

step "A profile the team shares: files that replace yours, and its own switches (committed in .outfit/)"
mkdir -p .outfit/profiles/yolo
printf '# Agents\nThe team'"'"'s instructions.\n' >AGENTS.md
printf '[outfit]\n    secrets-scan = on\n    locked = secrets-scan\n' >.outfit/config
printf '[outfit]\n    biome = off\n[outfit "hook"]\n    pre-commit = off\n' >.outfit/profiles/yolo/profile.config
printf '# Agents\nMy own instructions: be brief.\n' >.outfit/profiles/yolo/AGENTS.md
git add -A
git add --sparse -f .outfit
run git commit -m "add a shared yolo profile"
[ "$(runs)" = 4 ] || fail "the hook should have run for that commit (no profile yet)"
run outfit ls

step "Teammates who do not use outfit: one line and one document, nothing committed for you (a dry run here)"
run outfit init --team --dry-run
[ ! -e docs/outfit.md ] || fail "a dry run must not write docs/outfit.md"
git diff --quiet -- AGENTS.md || fail "a dry run must not change AGENTS.md"

step "Use it: AGENTS.md is now YOUR copy, and git status stays clean"
run outfit use yolo
run cat AGENTS.md
run git status --short
[ -z "$(git status --short)" ] || fail "git status should be clean with a profile active"
[ -L AGENTS.md ] || fail "AGENTS.md should be a link to the profile's file"
echo "more" >>file.txt
git add -A
run git commit -m "commit while yolo is active (the profile turns the hook off)"
[ "$(runs)" = 4 ] || fail "yolo turns the pre-commit hook off: biome must not have run"

step "Edit through the link: the edit goes to the profile's file, which git sees as an edit of the profile"
echo "- and kind" >>AGENTS.md
run git status --short
grep -q 'and kind' .outfit/profiles/yolo/AGENTS.md || fail "the edit should have gone into the profile"
git checkout -q -- .outfit/profiles/yolo/AGENTS.md

step "An editor's atomic save replaces the link by a file; outfit sync takes the new bytes into the profile and relinks"
printf '# Agents\nSaved by an editor.\n' >AGENTS.md.tmp
mv AGENTS.md.tmp AGENTS.md
run outfit sync
[ -L AGENTS.md ] || fail "sync should have relinked AGENTS.md"
grep -q 'Saved by an editor' .outfit/profiles/yolo/AGENTS.md || fail "the save should be in the profile"
git checkout -q -- .outfit/profiles/yolo/AGENTS.md
run outfit sync

step "A new worktree follows the clone's profile with nothing to do"
run git worktree add -q ../demo-wt
[ -L ../demo-wt/AGENTS.md ] || fail "the new worktree should have the profile's AGENTS.md too"

step "A profile of your own, never versioned: new, edit, and promote it when you want the team to have it"
run outfit new mine --from yolo
run outfit status
run outfit promote mine
git add --sparse -f .outfit/profiles/mine
run git commit -m "share the mine profile"

step "Inactive profiles are hidden from the working tree (agents would find them in every search); show one when you need to edit it"
run outfit author on mine
[ -d .outfit/profiles/mine ] || fail "author on mine should put the profile on disk"
run outfit author
run outfit author off mine
[ ! -e .outfit/profiles/mine ] || fail "author off mine should hide it again"

step "Back to the committed files: AGENTS.md is the team's again"
run outfit use base
[ ! -L AGENTS.md ] || fail "outfit use base should put the team's AGENTS.md back"

step "Documentation for coding agents is carried by the binary"
run outfit skills basics | head -n 5 || true

step "Is there a newer release? (needs the network)"
if [ "$quick" = 1 ]; then
  echo "    (skipped with --quick: CI has no business asking GitHub from here)"
else
  (outfit update --check 2>&1 || true) | sed 's/^/    /'
fi

step "outfit uninstall: everything init added is removed"
run outfit uninstall
[ "$(git config core.hooksPath)" = .husky ] || fail "core.hooksPath should be back to .husky"
[ ! -L AGENTS.md ] || fail "uninstall should put the team's AGENTS.md back"
echo "last" >>file.txt
git add -A
run git commit -m "fifth commit (husky directly)"
[ "$(runs)" = 5 ] || fail "husky should keep working without outfit"

printf '\n\033[32m✓ The demo checked everything it showed.\033[0m\n'
