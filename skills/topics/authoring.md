# outfit: authoring and seeing profiles

## See and edit inactive profiles

Inactive committed profiles are hidden from the working tree on purpose (they would show up in
every search). When you or an agent must read or edit them in place, show them:

```sh
outfit author                # what is on disk now (default: only the active profile)
outfit author on             # every committed profile is on disk under .outfit/profiles/
outfit author on lean web    # or just these (names must be committed profiles: outfit ls)
outfit author off lean       # hide one again; `outfit author off` hides every inactive one
outfit show yolo             # without switching and without showing it: toggles + files of any profile
outfit show yolo AGENTS.md   # one file's content
```

`author off` refuses while a profile it would hide holds uncommitted edits or new files. To keep them:
`git add --sparse -u .outfit` (edited files) or `git add --sparse -f .outfit/profiles/<name>` (new
ones; a plain `git add <path>` refuses because `.outfit/` is excluded), then `git commit` (a committed
profile is the team's: ask the person first); or remove them. The active profile is always on disk.
`outfit ls --json` says per profile whether it is `on_disk`. Edits made there are ordinary git
edits of `.outfit/profiles/<name>/…`; local and worktree profiles are never hidden (they live in
the git directory: `outfit show <profile>` prints where).

## Make a profile

```sh
outfit new mine                 # empty local profile (this clone, never versioned)
outfit new mine --from yolo     # a copy of any profile (hidden ones too)
outfit new scratch --here       # only for this worktree; deleted with it
# put files in it at the same relative paths as the tracked files they replace; list them in
# the profile's directory (outfit show mine prints it)
outfit use mine                 # then it behaves like any other profile
outfit promote mine             # move it into .outfit/profiles/mine/ so git versions it
git add --sparse -f .outfit/profiles/mine && git commit
```

`new` and `promote` never overwrite: an existing name is refused. Outside a directory it replaces
(`replace = <dir>`, see `outfit skills profiles`), a profile can only swap files the repository tracks.
`.DS_Store`, `*~`, `*.swp` are never profile files.

## Editing the active profile's files

Edit the swapped path as usual (it is a link). Then `outfit sync` (absorbs an atomic save) and,
for a committed profile, commit the profile's file (it is the team's: ask the person first). To undo an edit, `git checkout -- AGENTS.md`
and `git restore AGENTS.md` do not work (git hides that path and answers "did not match any
file"); use `git restore .outfit/profiles/<profile>/AGENTS.md` for a committed profile. A local
or worktree profile has no history to restore from.
