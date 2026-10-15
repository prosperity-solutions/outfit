# Profiles, file swaps and `outfit sync`

A **profile** changes two things on your machine only: which hook checks run (the
toggles, see [config.md](config.md) and [hooks.md](hooks.md)) and a few **swapped
files**, say your own `AGENTS.md` in place of the committed one. Nothing is changed for
teammates, and `git status` stays truthful.

## Layout

```
.outfit/
  config                              committed team defaults
  profiles/<name>/
    profile.config                    the profile's toggles
    AGENTS.md, apps/web/AGENTS.md …   files it swaps in, at the same relative paths
```

A profile swaps **tracked files only**: a file in the profile that the repository does
not have is skipped (and `outfit sync` says so). `base` is the profile with no swaps;
with no `outfit.profile` set you are on `base` and get exactly the committed repository.

## The commands

| Command | What it does |
|---|---|
| `outfit init` | installs the dispatcher (see [hooks.md](hooks.md)), hides inactive profiles, adds `/.outfit/` to `.git/info/exclude` |
| `outfit use <profile> [--here]` | makes it this clone's profile (or only this worktree's with `--here`), then syncs |
| `outfit sync` | makes the files match the active profile; silent on success. Hooks and agents run it |
| `outfit show <profile> [path]` | a profile's toggles and files, or one file's content, without switching |
| `outfit author on\|off` | puts every committed profile on disk to edit side by side, or hides them again |
| `outfit status` | the active profile, where each value comes from, and `state in sync` / `out of sync` |
| `outfit uninstall` | removes the links, restores the base files, then undoes everything else |

`use` needs `outfit init` first: without the dispatcher nothing would keep a swapped file
out of a commit. `outfit use base` returns to the committed files.

## How a swap works

For each swapped path outfit asks git to hide the committed file (a non-cone
`!/<path>` sparse-checkout pattern, with `sparse.expectFilesOutsideOfPatterns=true`),
then puts a **relative symlink** at the path pointing into
`.outfit/profiles/<profile>/`. So:

- `git status` stays clean, and `git add -A`, `git commit -a`, `reset --hard`,
  `clean -fdx`, `stash -u` and `rebase` leave the link alone;
- an edit made through the link is an edit of the profile's file, and shows in plain
  `git status` as ` M .outfit/profiles/<profile>/AGENTS.md`: that is where your edit went;
- only the **active** profile is on disk. The others are in git; look at them with
  `outfit show`, or put them all on disk with `outfit author on`;
- `.outfit/` is in `.git/info/exclude`, which keeps search tools like `rg` out of it.
  Side effect (a git rule, [git-behavior.md](git-behavior.md) row 34): `git add
  .outfit/…` by path refuses; use `git add -u .outfit` (modified files) or `git add -f`
  (new ones), or `git commit -a`.

The pre-commit guard refuses any commit that stages a swapped path or a symlink, and says
how to fix it.

## `outfit sync`

`sync` computes what the active profile wants, compares it with what is there, and fixes
the difference. It is **level-triggered**: it never reacts to the event that fired it, so
running it twice changes nothing. When nothing needs doing it spawns two git processes,
takes no lock and writes nothing. Hooks run it after `post-checkout`, `post-merge` and
`post-rewrite` (a checkout, a pull, an amend); `reset --hard` fires nothing, so the next
trigger catches up. A failing sync never changes a hook's exit code.

What it does with each swapped path:

| What is there | What sync does |
|---|---|
| the right link | nothing (it only notes that the profile's file changed through it) |
| nothing, or a link to somewhere else | makes the link |
| an editor's **atomic save** replaced the link by a regular file, and the profile's file is unchanged since | **absorbs** it: copies the new bytes into the profile's file, byte for byte, and relinks |
| the same, but the profile's file changed too | saves **both** under `.git/outfit/quarantine/<time>-<key>/{file,variant}/`, leaves everything where it is, exits **3** and prints what to compare |
| a path in the middle of a merge conflict | leaves it alone, exits 3 |
| a swapped path git forgot to hide (`git reset -- <path>`) | tells git again |
| a link the profile no longer wants | removes it, and git brings the committed file back |

outfit never overwrites bytes a person wrote. It **refuses** (exit 3, nothing changed, the
exact fix printed) to: take over a sparse checkout it did not set up (`git
sparse-checkout disable` first); hide a committed file that has uncommitted changes (stash
it); switch away from a profile whose files have uncommitted edits (commit or stash them);
turn author mode off over uncommitted edits in the hidden profiles.

Linked worktrees each have their own links and sparse patterns, so a sync runs per
worktree; `use` syncs the worktree it runs in, the others follow on their next trigger or
`outfit sync`. Syncs are serialised by a lock in the worktree's git dir (`sync.lock`); a
second one waits for the first rather than skipping, and a lock whose owner died is taken
over.

## Environment

`OUTFIT_SYNC_ACTIVE=1` is set on every git process a sync starts: a git command that
fires a hook of its own (a checkout) must not make outfit sync again from inside that
sync. Never set it yourself.

## Exit codes

`0` fine; `1` an error; `2` usage or an unknown profile; `3` a person must look (a
conflict, a quarantine, a refusal); `4` `outfit status` says the files are out of sync.
`--json` output has `in_sync`, `changed`, `needs_human`, `notes`, `refused` and `fixes`.

## Uninstalling

`outfit uninstall` removes the links and restores the base files **first**, then
switches sparse checkout off (the other order leaves ` T` in `git status` and lets
`commit -a` commit the link), then undoes the dispatcher, the settings outfit added, the
`.git/info/exclude` entry and its directories. `git config --list`, the index flags and
the working tree come back as they were. A regular file where a link was is yours: it is
left in place and reported. Your own local profiles survive.
