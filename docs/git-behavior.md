# How git actually behaves

Everything here was **verified against real git 2.43 on Linux** unless marked
otherwise. macOS (Apple git 2.39 and Homebrew git) was **not** tested when this
was written. Treat every claim as a hypothesis that a test must pin; when git
disagrees with this file, write the failing test, adapt the code, and record the
real behaviour here.

Seeded from `docs/HANDOVER.md`. Each entry names the test that pins it once that
test exists.

| # | Claim | Pinned by |
|---|---|---|
| 1 | A non-cone sparse pattern `!/<path>` plus `sparse.expectFilesOutsideOfPatterns=true` marks the tracked file skip-worktree: `git status` stays clean and plain `git add <path>` refuses. It survives `stash -u`, `reset --hard`, `clean -fdx`, `rebase` and `checkout`. | _pending (PR 6)_ |
| 2 | Sparse checkout removes a directory whose only file got hidden, so the parent must be recreated (`mkdir -p`) before the symlink is made. | _pending (PR 6)_ |
| 3 | On a conflicting merge git replaces a symlink with a regular file full of conflict markers; it does **not** write through the link. | _pending (PR 7)_ |
| 4 | `git ls-files -s -t -- <paths>` prints `S 100644 <sha> 0\t<path>` — one call gives the skip-worktree tag, unmerged stages and the base blob. (Re-verify on 2.36 in CI.) | _pending (PR 1/6)_ |
| 5 | `post-checkout` also fires on `git worktree add`, with an all-zero old sha. | _pending (PR 7)_ |
| 6 | `post-merge` also fires on a fast-forward `git pull`. | _pending (PR 3)_ |
| 7 | `git pull --rebase` fires `post-checkout` and then `post-rewrite`. | _pending (PR 3)_ |
| 8 | `reset --hard` fires no hook; the next trigger catches up. | _pending (PR 6)_ |
| 9 | `core.hooksPath` at `--worktree` scope beats `--local`, so husky rewriting `core.hooksPath` on every `npm install` cannot win. | _pending (PR 3)_ |
| 10 | A **relative** `core.hooksPath` resolves per worktree; in linked worktrees `.git` is a file, so no hooks run at all. The path must be absolute (`git rev-parse --path-format=absolute --git-common-dir`). | _pending (PR 3)_ |
| 11 | Chaining to the original hook passes args, stdin (pre-push) and the exit code through exactly, with a husky v9 layout. | _pending (PR 3)_ |
| 12 | A failing `post-checkout` makes `git worktree add` exit non-zero even though the worktree was created. A `post-*` hook must therefore return the chained hook's exit code, never outfit's own. | _pending (PR 3)_ |
| 13 | `git sparse-checkout disable` followed by `commit -a` **commits the symlink into history** (mode `120000`). Hence the pre-commit guard. | _pending (PR 3/6)_ |
| 14 | `git worktree add` copies the **whole** `config.worktree`, including `outfit.profile`; it also copies the sparse patterns. | _pending (PR 7)_ |
| 15 | Removing links and restoring base files must happen **before** disabling sparse checkout; otherwise status shows ` T` and `commit -a` commits `120000`. | _pending (PR 6)_ |
| 16 | `.git/info/exclude` hides nothing tracked from `git status`, so edits to swapped files still show. | _pending (PR 6)_ |
| 17 | With `/.outfit/` excluded: `rg` (including `--hidden`) finds nothing under `.outfit/`; `rg --hidden -L` finds only the canonical `AGENTS.md`; `grep -r` finds exactly one hit (the real file); `grep -R` finds both paths (accepted residual risk). Verified with ripgrep 14.1 and GNU grep 3.11. | _pending (PR 6)_ |
| 18 | `git config get` exists only from git 2.46; the floor is 2.36, so only `git config --get` / `--list` are used. | _pending (PR 1)_ |
| 19 | Ubuntu 22.04 ships git 2.34, below the floor. | _documented in the refusal message (PR 1)_ |
| 20 | After a repository tree is **copied** (every mtime changes), a cone `sparse-checkout set` left files that should have been removed, because git still saw their stat data as stale. Running `git update-index -q --refresh` after the copy fixes it. Fixtures do this once after copying. *(Observed with Apple git 2.54 on macOS.)* | `testkit` `TestOptions/cone` |
