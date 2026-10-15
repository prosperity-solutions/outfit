# outfit: authoring and seeing profiles

## See and edit inactive profiles

Inactive committed profiles are hidden from the working tree on purpose (they would show up in
every search). When you or an agent must read or edit them in place:

```sh
outfit author on     # every committed profile is on disk under .outfit/profiles/, editable and diffable
outfit author off    # hide the inactive ones again
outfit show yolo     # without switching and without author mode: toggles + files of any profile
outfit show yolo AGENTS.md   # one file's content
```

`author off` refuses while the hidden profiles hold uncommitted edits (commit them with
`git add --sparse -u .outfit && git commit`, or stash). Edits made in author mode are
ordinary git edits of `.outfit/profiles/<name>/…`.

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

`new` and `promote` never overwrite: an existing name is refused. A profile can only swap files
the repository tracks. `.DS_Store`, `*~`, `*.swp` are never profile files.

## Editing the active profile's files

Edit the swapped path as usual (it is a link). Then `outfit sync` (absorbs an atomic save) and,
for a committed profile, commit the profile's file (it is the team's: ask the person first). To undo an edit, `git checkout -- AGENTS.md`
and `git restore AGENTS.md` do not work (git hides that path and answers "did not match any
file"); use `git restore .outfit/profiles/<profile>/AGENTS.md` for a committed profile. A local
or worktree profile has no history to restore from.
