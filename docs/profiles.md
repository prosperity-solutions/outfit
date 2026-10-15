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

## Three tiers: committed, local, worktree

| Tier | Lives in | Versioned | Gone when |
|---|---|---|---|
| committed | `.outfit/profiles/<name>/` | by git, shared with the team | a commit removes it |
| local | `<git-common-dir>/outfit/profiles/<name>/` | never | `outfit uninstall --purge`, or you delete it |
| worktree | `<git-dir>/outfit/worktree-profiles/<name>/` | never | `git worktree remove` (the directory is inside that worktree's git dir) |

Names are unique across all three: `outfit new` refuses a name any tier already uses, and
`outfit ls` shows each profile's tier. The links for a committed profile are relative
(`.outfit/profiles/<name>/AGENTS.md`), so an edit through the link shows in plain `git
status` as an edit of the profile. The links for a local or worktree profile are
**absolute** into the git dir, and edits to those files do not show in `git status`, by
design (they are not in git): `outfit status` lists each file with when it last changed, e.g.
`AGENTS.md ← mine (local, edited 2h ago)`. There is no history and no snapshot: plain files,
edited in place.

```sh
outfit new mine                 # an empty local profile (profile.config + room for files)
# then put files in it at the same relative paths as the tracked files they replace
outfit new mine3 --from yolo    # a copy of a committed (or local) profile, hidden ones included
outfit new scratch --here       # only for this worktree; deleted with it
outfit use mine                 # ...then it behaves like any other profile
outfit promote mine             # move it into .outfit/profiles/mine/, then commit it
```

`promote` copies first and removes the local copy only after the whole copy worked: a link
or special file in the local profile (never copied, since profiles hold plain files) makes
it refuse, naming the file, and leave everything as it was. Editor junk (`.DS_Store`, `*~`,
`*.swp`, …) is never copied. Other worktrees that use the profile (clone-wide, or pinned)
have no copy of the new directory until it is committed and they have the commit, so they
behave as base until then: `promote` lists them. Git does not have the new profile until you
commit it, and `git clean -x` would delete it, so `status` and `use` warn (for every
profile directory git does not have, active or not) and `promote` prints the command: `git add --sparse -f .outfit/profiles/<name> && git
commit` (`--sparse` because git's patterns hide profile directories). If a profile of the
same name is committed later by a teammate, **the committed one wins**: `ls` and `status`
warn, and the fix is `outfit new <other-name> --from local:<name>` (a `tier:name` reaches a
profile the winner hides; `outfit show local:<name>` shows it, and `outfit ls --json` lists
it under `shadowed`), then delete the old directory. A worktree profile of **another**
worktree counts too: `new` refuses its name, because a local profile of the same name would
replace it there on that worktree's next sync. Directories with names outfit does not accept
(made by hand) are listed with a warning, never silently ignored.

`new` and `promote` never overwrite: the destination is created with a plain `mkdir`, and a
copy is limited to 2000 files, 32 MiB per file and 64 MiB in all.

## The commands

| Command | What it does |
|---|---|
| `outfit init` | installs the dispatcher (see [hooks.md](hooks.md)), hides inactive profiles, adds `/.outfit/` to `.git/info/exclude` |
| `outfit use <profile> [--here]` | makes it this clone's profile (or only this worktree's with `--here`), then syncs |
| `outfit sync` | makes the files match the active profile; silent on success. Hooks and agents run it |
| `outfit new <name> [--from <profile>] [--here]` | makes a **personal** profile: local (this clone, never versioned), or with `--here` a worktree profile that goes away with the worktree; empty, or a copy of another profile |
| `outfit promote <name>` | moves a local profile into `.outfit/profiles/<name>/` so git can version it |
| `outfit show <profile> [path]` | a profile's toggles and files, or one file's content, without switching |
| `outfit author on\|off` | puts every committed profile on disk to edit side by side, or hides them again |
| `outfit status` | the active profile, where each value comes from, and `state in sync` / `out of sync` |
| `outfit diff <path>` / `outfit ack <path>` | the base of a swapped file changed under it: see what changed, accept it (below) |
| `outfit uninstall [--purge]` | removes the links, restores the base files, then undoes everything else; your local and worktree profiles are kept unless `--purge`. If a link cannot be removed it stops there (exit 1, nothing else undone, no profile deleted) and the rerun continues |

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
  `git status` as ` M .outfit/profiles/<profile>/AGENTS.md`: that is where your edit went.
  `git diff AGENTS.md` and `git restore AGENTS.md` do not see it; use
  `git diff .outfit` and `git restore .outfit/profiles/<profile>/AGENTS.md`;
- only the **active** profile is on disk. The others are in git; look at them with
  `outfit show`, or put them all on disk with `outfit author on`;
- `.outfit/` is in `.git/info/exclude`, which keeps search tools like `rg` out of it.
  Side effect (a git rule, [git-behavior.md](git-behavior.md) row 34): `git add
  .outfit/…` by path refuses; use `git add -u .outfit` (modified files), `git add -f` (a
  new file in a profile that is on disk), or `git commit -a`. A **new profile directory**
  is not on disk as far as git's sparse patterns go: run `git add --sparse -f
  .outfit/profiles/<name>`, then commit. Files you
  staged but have not committed are never taken off disk: `outfit` refuses to hide them
  and says to commit first.

A profile directory git does not have yet (made by hand, never committed) exists only
in your working tree: `git clean -x` deletes it. `outfit use`/`status` warn while there are
such files; commit them as described above. Finder and editor leftovers (`.DS_Store`,
`*~`, `*.swp`, `*.orig`, …) are never profile files. If the base file is executable and the
profile's copy is not (or the other way round), that path is not swapped and a note says
how to make the two agree.

The pre-commit guard refuses any commit that stages a swapped path, or a symlink at a swapped path, and says
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
| the same, but the profile's file changed too (this includes an edit you made through the link since the last `outfit` command: nothing runs when a file is edited, so sync cannot tell it from a change made by git; run `outfit sync` after editing and before an editor saves, or take the printed fix) | saves **both** under `.git/outfit/quarantine/<time>-<key>/{file,variant}/`, leaves everything where it is, exits **3** and prints what to compare |
| a path in the middle of a merge conflict (git replaced the link with a regular file full of markers) | leaves it alone: never absorbs the markers, keeps the patterns as they are, exits 3 and prints the fix. Resolve the **base** file, `git add --sparse <path> && git commit`, then `outfit sync` links it again |
| the base file is different from when it was swapped (a pull, a merge, a checkout) | keeps the swap and the profile's copy as they are, prints **one note** on stderr (the reason and the fix), exits 0. `outfit status` shows `state stale` and exits 4; see below |
| the profile's own file is gone (removed upstream, or an older branch) | removes the link, the base file is back, one line says so; checking the newer branch out again swaps it again |
| a swapped path git forgot to hide (`git reset -- <path>`) | tells git again |
| a link the profile no longer wants | removes it, and git brings the committed file back |

### The base moved under a swap: `outfit diff`, `outfit ack`

A swapped path shows the profile's copy, so a clean `git pull` that changes the **base**
file does not change what you see. outfit remembers the base's blob from when it made the
link and compares it on every sync. When they differ it keeps the swap, says so on every
sync, and `outfit status` exits **4** with `state stale`. (`sync --json` still says
`in_sync: true`: there is nothing left for sync to do; the paths are in `stale`.) A pull
where **one commit** changes the base and the profile's own file together is the profile's
maintainer adapting it to the new base: nothing is stale, the record follows. Anything
looser would silently drop a base change nobody looked at (an unrelated profile fix pulled
in next to it, or your own profile commit made after the note appeared), so those stay
stale until you `outfit ack`. A branch switch changes the base too: acknowledged bases are
remembered (the last eight per file), so switching back and forth between branches you
have looked at does not nag.

```sh
outfit diff AGENTS.md    # what changed in the base (a git diff; --json has it too)
git show :AGENTS.md      # the whole new base file (the link shows the profile's copy)
outfit ack AGENTS.md     # the profile's copy is fine as it is: record the new base
```

Neither touches the profile's copy. If the new base deserves a change in the profile, edit
the profile's file (that is the file the link shows) and `outfit ack` it: **committing**
that edit alone does not clear the note. Paths are relative to where you are.
Acknowledging a base that did not change prints "nothing to acknowledge". If the version
outfit swapped over is gone from the repository (a deleted branch, a `gc`), `diff` says so
and `ack` still works.

### New worktrees

`git worktree add` copies the main worktree's whole `config.worktree` into the new one,
a pinned profile (`outfit use x --here`) included. On the `post-checkout` that
`worktree add` fires (the old sha is all zeros, the flag is 1) in a linked worktree outfit
has not synced yet, outfit unsets every copied `outfit.*` key, so the new worktree follows
the clone's default until `outfit use <p> --here` says otherwise. The copied
`core.hooksPath` and sparse settings stay, and its links are made by the same sync. A
worktree outfit has already synced keeps its settings, and so does the main worktree.
`git worktree add --no-checkout` fires no hook, so nothing is stripped there: run
`outfit use <profile> --here` (or unset `outfit.profile --worktree`) yourself.

### Agent sessions: `outfit init --agents`

`reset --hard` fires no hook, so a coding agent's session can start with the files out of
step. `outfit init --agents` adds a Claude Code `SessionStart` entry that runs
`outfit sync` (quiet when it works, one line when it does not, never failing the session) to `.claude/settings.local.json` in
every worktree that has a `.claude` directory, and in worktrees added later (the main
worktree's entry is copied when the new one has a `.claude` directory): the **personal**
settings file, kept out of `git add -A` by an entry in `.git/info/exclude` (unless git
already ignores it; running `init --agents` again repairs it), so teammates who do not use outfit are not affected. A settings file
that git **tracks**, or that is a link, is never edited (exit 3 and a message). The entry is
recognised by a trailing `# outfit-managed` comment on the command; every other setting
and hook is kept (the file is rewritten with its keys sorted and re-indented), a hook of
your own inside the same group stays, a file outfit cannot read as JSON is not touched,
`outfit status` shows an `agents` line while the entry is installed (and `agents_session_start`
in `--json`). When a session starts and sync cannot finish cleanly (a conflict, a
quarantine, outfit missing) the command prints one line, which Claude Code adds to the
session; when it works it prints nothing. A new worktree only gets the entry when it has
a `.claude` directory (git creates one only when something under it is tracked). A
repository whose main worktree path outfit cannot know (a submodule, `--separate-git-dir`)
is handled for the worktree you run it in. To remove the entry alone, delete the hook whose
command ends in `# outfit-managed` (or run `outfit uninstall`, which takes the entry and the exclude line away without failing over a
file it never wrote (a settings file that held nothing else goes with it)).

outfit never overwrites bytes a person wrote. It **refuses** (exit 3, nothing changed, the
exact fix printed) to: take over a sparse checkout it did not set up (`git
sparse-checkout disable` first); hide a committed file that has uncommitted changes (stash
it); switch away from a profile whose files have uncommitted edits (commit or stash them);
turn author mode off over uncommitted edits in the hidden profiles.

Linked worktrees each have their own links and sparse patterns, so a sync runs per
worktree; `use` syncs the worktree it runs in, the others follow on their next trigger or
`outfit sync`. Syncs are serialised by a lock in the worktree's git dir (`sync.lock`); a
second one waits for the first rather than skipping. It is an `flock`, so the system
frees it when its owner dies; there is never a stale lock to clean up.

## Environment

`OUTFIT_SYNC_ACTIVE=1` is set on every git process a sync starts: a git command that
fires a hook of its own (a checkout) must not make outfit sync again from inside that
sync. Never set it yourself.

## Exit codes

`0` fine; `1` an error; `2` usage or an unknown profile; `3` a person must look (a
conflict, a quarantine, a refusal); `4` `outfit status` says the files are out of sync, or stale (a base file differs from when it was swapped).
`--json` output has `in_sync`, `changed`, `needs_human`, `notes`, `refused`, `fixes` and
`stale` (the paths whose base moved).

## Uninstalling

`outfit uninstall` removes the links and restores the base files **first**, then
switches sparse checkout off (the other order leaves ` T` in `git status` and lets
`commit -a` commit the link), then undoes the dispatcher, the settings outfit added, the
`.git/info/exclude` entry and its directories. `git config --list`, the index flags and
the working tree come back as they were. A regular file where a link was is yours: it is
left in place and reported. Your own local profiles survive.
