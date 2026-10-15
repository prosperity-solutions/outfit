#!/usr/bin/env bash
#
# The soft performance gate (docs/HANDOVER.md §7.6): `outfit sync` must cost no more than
# 10 ms over the cheapest thing a git hook can do, `git rev-parse --git-dir`. Soft means
# it runs on Linux only and the job may fail without failing the workflow: a noisy runner
# is a reason to look, not to block a merge. The HARD gate (how many git processes a
# command spawns) is `gitcalls` in the testscripts and runs on every OS.
#
# Usage: scripts/ci/perf-soft.sh [path-to-outfit]   (default: build ./cmd/outfit)
#   env: PERF_BUDGET_MS  the allowed difference of the medians (default 10)
#        PERF_RUNS       hyperfine runs per command (default 60)
set -euo pipefail

budget=${PERF_BUDGET_MS:-10}
runs=${PERF_RUNS:-60}
command -v hyperfine >/dev/null || { echo "hyperfine is not installed" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is not installed" >&2; exit 2; }

root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

outfit=${1:-}
if [ -z "$outfit" ]; then
  (cd "$root" && go build -o "$work/outfit" ./cmd/outfit)
  outfit=$work/outfit
fi

# A repository like the ones outfit is for: tracked files, committed profiles, a profile
# in use (so a sync has links to look at), hooks installed.
export HOME=$work/home GIT_CONFIG_GLOBAL=$work/home/.gitconfig GIT_CONFIG_NOSYSTEM=1 OUTFIT_NO_UPDATE_CHECK=1
mkdir -p "$HOME"
git config --global user.email perf@example.com
git config --global user.name perf
git config --global init.defaultBranch main
repo=$work/repo
git init -q "$repo"
cd "$repo"
mkdir -p apps/web .outfit/profiles/yolo
printf '# Agents\nbase\n' > AGENTS.md
printf '# Web\nbase\n' > apps/web/AGENTS.md
printf '# Agents\nyolo\n' > .outfit/profiles/yolo/AGENTS.md
printf '[outfit]\n\tbiome = off\n' > .outfit/profiles/yolo/profile.config
for i in $(seq 1 200); do printf 'file %s\n' "$i" > "src-$i.txt"; done
git add -A
git commit -qm 'chore: base'
"$outfit" init >/dev/null
"$outfit" use yolo >/dev/null

echo "repository ready: $(git ls-files | wc -l | tr -d ' ') tracked files, profile yolo in use"
hyperfine --warmup 5 --runs "$runs" --shell=none --export-json "$work/bench.json" \
  "git rev-parse --git-dir" \
  "$outfit sync" >/dev/null

# hyperfine 1.x has .results[].median (seconds); 2.x moved the statistics under
# .summary.time_wall_clock.
median() { jq --argjson i "$1" '(.results[$i].median // .results[$i].summary.time_wall_clock.median) * 1000' "$work/bench.json"; }
git_ms=$(median 0)
sync_ms=$(median 1)
diff_ms=$(jq -n --argjson a "$sync_ms" --argjson b "$git_ms" '$a - $b')
printf 'median git rev-parse --git-dir: %.2f ms\nmedian outfit sync:             %.2f ms\ndifference:                     %.2f ms (budget %s ms)\n' "$git_ms" "$sync_ms" "$diff_ms" "$budget"
if jq -en --argjson d "$diff_ms" --argjson b "$budget" '$d > $b' >/dev/null; then
  echo "::warning::outfit sync is ${diff_ms} ms slower than git rev-parse --git-dir (budget ${budget} ms)" >&2
  exit 1
fi
echo "within budget"
