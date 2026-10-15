# How git actually behaves

Everything here was **verified against real git 2.43 on Linux** unless marked
otherwise. macOS (Apple git and Homebrew git) was **not** tested when this
was written; CI exercises it on every PR (Apple git) and on every push to `main`
(Homebrew git, Intel). Treat every claim as a hypothesis that a test must pin; when git
disagrees with this file, write the failing test, adapt the code, and record the
real behaviour here.

Seeded from `docs/HANDOVER.md`. Each entry names the test that pins it once that
test exists.

| # | Claim | Pinned by |
|---|---|---|
| 1 | A non-cone sparse pattern `!/<path>` plus `sparse.expectFilesOutsideOfPatterns=true` marks the tracked file skip-worktree: `git status` stays clean and plain `git add <path>` refuses. It survives `stash -u`, `reset --hard`, `clean -fdx`, `rebase` and `checkout`. | `swaps-basic.txtar`, `swaps-safety.txtar`; without `sparse.expectFilesOutsideOfPatterns` git clears the bit and shows the link as ` T` (row 32) |
| 2 | Sparse checkout removes a directory whose only file got hidden, so the parent must be recreated (`mkdir -p`) before the symlink is made. | `swaps-basic.txtar` (apps/web) |
| 3 | On a conflicting merge git replaces a symlink with a regular file full of conflict markers; it does **not** write through the link. | _pending (PR 7)_ |
| 4 | `git ls-files -s -t -- <paths>` prints `S 100644 <sha> 0\t<path>` — one call gives the skip-worktree tag, unmerged stages and the base blob. (Re-verify on 2.36 in CI.) | `gitx` `TestLsFilesSkipWorktreeFormat` (format only; sparse behaviour arrives in PR 6) |
| 5 | `post-checkout` also fires on `git worktree add`, with an all-zero old sha. | _pending (PR 7)_ |
| 6 | `post-merge` also fires on a fast-forward `git pull`. | _pending (PR 3)_ |
| 7 | `git pull --rebase` fires `post-checkout` and then `post-rewrite`. | _pending (PR 3)_ |
| 8 | `reset --hard` fires no hook; the next trigger catches up. | `swaps-hooks.txtar` |
| 9 | `core.hooksPath` at `--worktree` scope beats `--local`, so husky rewriting `core.hooksPath` on every `npm install` cannot win. | _pending (PR 3)_ |
| 10 | A **relative** `core.hooksPath` resolves per worktree; in linked worktrees `.git` is a file, so no hooks run at all. The path must be absolute (`git rev-parse --path-format=absolute --git-common-dir`). | _pending (PR 3)_ |
| 11 | Chaining to the original hook passes args, stdin (pre-push) and the exit code through exactly, with a husky v9 layout. | _pending (PR 3)_ |
| 12 | A failing `post-checkout` makes `git worktree add` exit non-zero even though the worktree was created. A `post-*` hook must therefore return the chained hook's exit code, never outfit's own. | _pending (PR 3)_ |
| 13 | `git sparse-checkout disable` followed by `commit -a` **commits the symlink into history** (mode `120000`). Hence the pre-commit guard. | `swaps-safety.txtar`, `hooks-guard.txtar` |
| 14 | `git worktree add` copies the **whole** `config.worktree`, including `outfit.profile`; it also copies the sparse patterns. | _pending (PR 7)_ |
| 15 | Removing links and restoring base files must happen **before** disabling sparse checkout; otherwise status shows ` T` and `commit -a` commits `120000`. | `swaps-uninstall.txtar` |
| 16 | `.git/info/exclude` hides nothing tracked from `git status`, so edits to swapped files still show. | `swaps-basic.txtar` |
| 17 | With `/.outfit/` excluded: `rg` (including `--hidden`) finds nothing under `.outfit/`; `rg --hidden -L` finds only the canonical `AGENTS.md`; `grep -r` finds exactly one hit (the real file); `grep -R` finds both paths (accepted residual risk). Verified with ripgrep 14.1 and GNU grep 3.11. | `swaps-uninstall.txtar` (`grep -r`; `rg` is not required to run the suite) |
| 18 | `git config get` exists only from git 2.46; the floor is 2.36, so only `git config --get` / `--list` are used. | `gitx` `TestNoGitConfigGet` |
| 19 | Ubuntu 22.04 ships git 2.34, below the floor. | `gitx` `TestCheckFloor` (the error names the PPA fix) |
| 20 | After a repository tree is **copied** (every mtime changes), a cone `sparse-checkout set` left files that should have been removed, because git still saw their stat data as stale. Running `git update-index -q --refresh` after the copy fixes it. Fixtures do this once after copying. *(Observed with Apple git 2.54 on macOS.)* | `testkit` `TestOptions/cone` |
| 21 | A dotted key under `[outfit]` (`hook.pre-commit = off`) is **not valid git config syntax**: `git config -f <file> --get outfit.biome` dies with `bad config line` (exit 128) for the **whole file**, even for other keys. The valid spelling is a subsection: `[outfit "hook"]` + `pre-commit = off`, which git reads as `outfit.hook.pre-commit`. outfit accepts both, but committed files that non-users read with `git config -f` must use the subsection form. | `non-user-fallback.txtar` |
| 22 | `git config --list --show-scope -z` prints `<scope> NUL <key> LF <value> NUL`; a key written without `=` has no LF and no value (it means true). Scopes seen: `system`, `global`, `local`, `worktree`, `command`. Section and variable names arrive lowercased; subsections keep their case. | `config` `TestParseGitList`, `cli` integration tests |
| 23 | In the **main** worktree the per-worktree git dir and the common dir are the same directory, so two tiers that both live under `<dir>/outfit/profiles/` would collide there. The worktree tier therefore uses `<git-dir>/outfit/worktree-profiles/`. | `profiles` `TestLocalAndWorktreeTiersAndNameClash` |
| 24 | `git worktree add` copies the main worktree's `config.worktree` into the new worktree, so a `--worktree`-scoped `outfit.*` value set in the main worktree appears in every worktree created afterwards. (Spec §3.6 has outfit strip the copies on the all-zero `post-checkout`.) | _pending (PR 7)_ |
| 25 | When git cannot read a repository's config (`safe.directory`, "dubious ownership"), `git config --list --show-scope` silently omits the local and worktree scopes and still exits 0. outfit treats "no local-scope entry at all" as that condition. `GIT_TEST_ASSUME_DIFFERENT_OWNER=1` reproduces it. | `cli` `TestDubiousOwnershipFailsLoudly` |
| 26 | With `worktree.useRelativePaths` (git ≥ 2.48) the file `<common>/worktrees/<n>/gitdir` holds a path relative to that directory. | `repo` `TestRelativeBackPointer` |
| 27 | While outfit's sparse config hides a path (`core.sparseCheckout=true`, pattern `!/<path>`), `git commit -a` does **not** stage a modified copy of it ("no changes added to commit") and plain `git add <path>` refuses; only `git add --sparse <path>` stages it. With the sparse config disabled, `commit -a` stages and commits whatever is there, symlink included (row 13). The pre-commit guard exists for those two ways in. | `hooks-guard.txtar` |
| 28 | `git config --file <git-dir>/config.worktree …` reads and writes a worktree's own config whether or not its checkout exists (a missing file reads as empty, `--unset-all` of an absent key exits 5), so init and uninstall never need to run git inside a checkout. | `hooks-worktrees.txtar` |
| 29 | Tools resolve the hooks directory with `git rev-parse --git-path hooks`, which honours `core.hooksPath`: after `outfit init` that is outfit's shim directory. `lefthook install` (2.2.2) renames an existing shim to `<hook>.old` and writes its own hook there. | `hooks-adopt.txtar` (simulated) |
| 30 | A hook's working directory is the worktree root for client-side hooks but `$GIT_DIR` (with `GIT_DIR=.`) for receive-side ones, so a dispatcher must not force a cwd. | `hooks` `Chain` (inherits the caller's cwd) |
| 31 | `core.hooksPath` may be written with a leading `~/`, which git expands itself; a shell fallback must expand it too. | `hooks-tilde.txtar` |

## Test tracing

`OUTFIT_GIT_TRACE_FILE=<path>`: when set, `internal/gitx` appends one line per git
process it starts (the test harness uses it to pin how many git processes a command
may spawn). Unset in normal use.
| 32 | Without `sparse.expectFilesOutsideOfPatterns=true`, git notices a link standing at a skip-worktree path, clears the bit, and `git status` shows ` T <path>`. With it (set at `--worktree` scope), the path stays skip-worktree and `git status` is clean (verified on git 2.36.6, Apple git 2.54 and git 2.55). | `swaps-basic.txtar` |
| 33 | `git reset -- <path>` (and `update-index`) drops the skip-worktree bit of a swapped path, so the link would show as a type change and could be staged. sync restores it with `git update-index --skip-worktree`. If sparse checkout was switched off by hand first, the bit must be restored **before** the sparse patterns are written again, or git sees the links as locally modified files and refuses to hide them. | `swaps-safety.txtar` |
| 34 | With `/.outfit/` in `.git/info/exclude`, `git add <path-under-.outfit>` and `git add .outfit` refuse even for TRACKED, modified files ("paths are ignored"); `git add -u .outfit`, `git add -A` and `git commit -a` work. Fix texts therefore say `git add -u .outfit`. | `swaps-refusals.txtar` (the fix text), `docs/profiles.md` |
| 35 | `git sparse-checkout set` leaves files that have uncommitted changes in place and only warns ("not up to date … left despite sparse patterns"), exit 0: the tree is half-switched. outfit refuses to switch over such edits beforehand and fails loudly if git still left files behind. | `swaps-refusals.txtar` |
| 36 | `git sparse-checkout disable` leaves `core.sparseCheckout=false` and `index.sparse=false` in the config. Uninstall removes the keys outfit's sparse use added, so `git config --list` comes back byte for byte (a `true` value of `index.sparse` is someone's sparse index and stays). | `swaps-uninstall.txtar` |
| 37 | `git restore <path>` (git 2.55) runs the `post-checkout` hook, with the third argument `0` (a file checkout). outfit's sync therefore also runs after a file restore. | `swaps-hooks.txtar` |
| 38 | `git add --sparse <path>` on a path git is told to expect outside the patterns stages nothing on current git; on other versions it stages the file. The guard does not rely on either: it inspects what is staged. A symlink forced into the index by hand (`update-index --cacheinfo 120000,…`) is refused by the pre-commit guard. | `swaps-safety.txtar` |
| 39 | A linked worktree has its own sparse patterns, skip-worktree bits and links: syncs run per worktree, serialized by a per-worktree lock, and ten parallel syncs across three worktrees end valid. | `swaps-parallel.txtar` |
