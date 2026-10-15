# outfit: when something looks wrong

Start with `outfit status` (`--json` for a script). It names the state and exits 0 (fine), 3 (a
person must look) or 4 (out of sync, stale, or the hook dispatcher is broken).

| Symptom | Meaning and fix |
|---|---|
| `state stale`, a note on every sync | a pull/merge/checkout changed a swapped **base** file. Your swap is kept. `outfit diff <path>` shows the change, `git show :<path>` the new base; `outfit ack <path>` accepts it. Committing alone does not clear the note |
| exit 3, `quarantine` | a swapped file and its profile copy both changed. Both copies are in `.git/outfit/quarantine/<time>-<key>/{file,variant}/`. Compare the two, put what you want to keep into the profile's copy, then **remove the swapped file (`rm AGENTS.md`) or make the two identical**, and `outfit sync`. Syncing without that step quarantines again (exit 3, a new folder each time) |
| exit 3, merge conflict in a swapped path | resolve the **base** file, `git add --sparse <path> && git commit`, `outfit sync` |
| `outfit use` refuses | a profile you are leaving has uncommitted edits (commit or stash), or a staged swapped file (commit first), or a sparse checkout outfit did not set up (`git sparse-checkout disable`) |
| `git add .outfit/…` refuses | `.outfit/` is in `.git/info/exclude`: `git add -u .outfit`, `git add -f <file>`, `git add --sparse -f .outfit/profiles/<name>` for a new directory, or `git commit -a` |
| a swapped path shows ` T` or the symlink is staged | something disabled sparse checkout. `git reset -q && outfit sync` |
| files not matching the profile after `git reset --hard` | no hook fires for it: `outfit sync` |
| `outfit: sync did not finish cleanly` at session start | run `outfit status` for the reason |
| a profile of a teammate has the same name as your local one | the committed one wins; `outfit new <other> --from local:<name>` keeps yours |

| exit 3, `is a file at a path the team tracks inside <dir>` | the profile replaces `<dir>` and a file of yours stands at a hidden team path: git may delete it. A copy is saved under `.git/outfit/quarantine/`. Move the file out of `<dir>`, then `outfit sync` |
| `is the profile's own file in <dir>, which the profile replaces, but it is not linked: …` | something is in the way of a file only the profile has (the note says what): a file of yours at that path, a `.gitignore` there that is not outfit's or the team tracks, a name that clashes with a tracked path. Remove or rename what is in the way, then `outfit sync` |
| `<dir>: the team changed files below it` | a pull changed the team's files in a directory your profile replaces. `outfit ack <dir>` once you are fine with it (`git log --stat -- <dir>` shows what changed) |
| commit refused: `is below <dir>, which your active profile replaces` | something below a replaced directory is staged: `git reset -q -- <path>`; on purpose, `OUTFIT_ALLOW=1 git commit` or `outfit use base` first |
| `use` refused: files below `<dir>` are staged / have uncommitted changes | replacing the directory would take them off disk: commit or stash them first |

Everything a failing command prints ends with the exact `fix`; `--json` has it as `fix`.
Never delete `.git/outfit/` by hand: `outfit uninstall` is the supported way out, and it restores
every base file first.
