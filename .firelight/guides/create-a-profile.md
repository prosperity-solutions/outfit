---
format: 1
title: Create your own profile and share it
summary: Make a personal profile with outfit new, put your files in it, use it, and promote it to .outfit/profiles to share with the team.
aliases: [outfit new, new profile, outfit promote, promote, share profile, personal profile, --from]
tags: [profiles]
related: [profile-tiers, edit-a-profile, profiles]
covers: [internal/profiles/manage.go, internal/cli/cmd_new.go]
---
1. `outfit new mine` (or `outfit new mine --from yolo` to start from a copy; `--here` for
   this worktree only).
2. Put your files in it at the same relative paths as the tracked files they replace
   (e.g. `AGENTS.md`).
3. `outfit use mine`.
4. To share it: `outfit promote mine`, then
   `git add --sparse -f .outfit/profiles/mine && git commit`.

## What it does not do
A local profile is never in git: no history, no undo. `outfit uninstall --purge` deletes it.
A promoted profile is not in git until you commit it.
