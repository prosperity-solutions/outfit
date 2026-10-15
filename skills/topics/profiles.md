# outfit: profiles and swapped files

A **profile** changes two things on your machine only: which hook checks run (its
**toggles**) and a few **swapped files** — your own `AGENTS.md` in place of the committed
one. `base` is the profile with no swaps; with none chosen you are on `base` and get exactly
the committed repository.

## Layout

```
.outfit/
  config                          committed team defaults (toggles, `locked`)
  profiles/<name>/
    profile.config                the profile's toggles
    AGENTS.md, apps/web/AGENTS.md files it swaps in, at the same relative paths
```

A profile swaps **tracked files only**: a file the repository does not have is skipped (and
`outfit sync` says so).

## Three tiers

| Tier | Lives in | Versioned |
|---|---|---|
| committed | `.outfit/profiles/<name>/` | yes, shared with the team |
| local | `<git-common-dir>/outfit/profiles/<name>/` | never |
| worktree | `<git-dir>/outfit/worktree-profiles/<name>/` | never; goes with the worktree |

Names are unique across tiers; a committed profile wins a clash. `tier:name` (for example
`local:mine`) reaches a profile the winner hides, in `outfit show` and `outfit new --from` (other
commands take a plain name).

## How a swap works

outfit tells git to hide the committed file (a sparse-checkout pattern) and puts a symlink at
the path: relative into `.outfit/profiles/<profile>/` for a committed profile, absolute into
the git dir for a local or worktree one. So `git status` stays clean and `git add -A`,
`commit -a`, `reset --hard`, `clean`, `stash` and `rebase` leave the link alone.

- An edit through the link is an edit of the profile's file. For a committed profile it shows
  in `git status` as ` M .outfit/profiles/<profile>/AGENTS.md`; `git diff AGENTS.md` does not
  see it, `git diff .outfit` does. For a local or worktree profile git sees nothing:
  `outfit status` lists each file with when it changed, and there is no undo.
- Only the **active committed** profile is on disk. The other committed ones are in git:
  `outfit show <profile>` reads them, `outfit author on [profile]` puts them (or one) on disk.
- `.outfit/` is in `.git/info/exclude`. A consequence: `git add .outfit/…` by path refuses;
  use `git add -u .outfit`, `git add -f` (new file) or `git commit -a`. A **new profile
  directory** needs `git add --sparse -f .outfit/profiles/<name>`.
- The pre-commit guard refuses a commit that stages a swapped path or a symlink at one.

## Replacing a whole directory

A profile can own a directory: its `profile.config` says `[outfit "profile"]` / `replace = .claude/skills`,
and its files at the same relative paths replace the team's. While it is active every tracked file below
the directory is hidden for you (the team's later additions too), the profile's files at tracked paths are
linked in, and so are the profile's own files that the repository does not track (an untracked `.gitignore` in the
directory, outfit's, lists them and ignores itself: do not edit it), `git status` stays clean and the guard refuses to commit anything staged below it
(`OUTFIT_ALLOW=1 git commit` only on purpose, or `outfit use base` first). A team change there is reported
once (`outfit ack <dir>`; `outfit diff <dir>` says where to look). `outfit status` has a `dirs` line, `show`
and `use` name the replaced directories. outfit refuses to start while files below the directory are staged
or have uncommitted changes. **Do not write a file at a path the team tracks there**: git deletes it
silently on a checkout; every sync saves a copy and exits 3 until you move it. Stopping (or `uninstall`)
also stops while such a file is there.

## What `outfit sync` does per path

| What is there | What sync does |
|---|---|
| the right link | nothing |
| nothing, or a link elsewhere | makes the link |
| an atomic save replaced the link, profile file unchanged | absorbs: copies the bytes into the profile's file, relinks |
| the same, but the profile's file changed too | quarantines both, exits 3 |
| a merge conflict in the path | leaves it, exits 3; resolve the **base** file, `git add --sparse <path> && git commit`, `outfit sync` |
| the base changed since the swap | keeps the swap, one note, `status` shows `stale` (exit 4): `outfit diff`, `outfit ack` |
| the profile's own file is gone | removes the link; the base file is back |

`outfit use <profile>` needs `outfit init` first. `outfit use base` returns to the committed
files. `outfit uninstall` puts every file back.
