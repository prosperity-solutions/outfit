---
format: 1
title: Edit a profile
summary: Edit the swapped file through the link, run outfit sync after an editor's atomic save, and use outfit author on to see other committed profiles.
aliases: [edit profile, outfit sync, outfit author, author mode, atomic save, edit agents.md]
tags: [profiles]
related: [swapped-files, create-a-profile]
covers: [internal/apply/**, internal/cli/cmd_sync.go, skills/topics/authoring.md]
---
1. Edit the swapped file in place (e.g. `AGENTS.md`). That edits the active profile's copy.
2. Run `outfit sync` after an editor or agent saved it by writing a new file; outfit copies
   the new bytes into the profile. Hooks also sync after checkout and merge.
3. To edit a committed profile that is not active: `outfit author on <profile>`, edit under
   `.outfit/profiles/<profile>/`, then `outfit author off`.
4. Edits to a committed profile show in `git status` as `.outfit/profiles/<name>/…`: commit them
   like any change (it is shared with the team).

## What it does not do
A local or worktree profile has no undo.
