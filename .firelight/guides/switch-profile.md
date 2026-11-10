---
format: 1
title: Switch to another profile
summary: List the profiles, look at one, and switch with outfit use; outfit use base goes back to the committed files.
aliases: [outfit use, use profile, change profile, outfit ls, outfit show, use base, my own agents.md]
tags: [profiles]
related: [profiles, profile-tiers, use-with-worktrees]
covers: [internal/cli/cmd_ls.go, internal/apply/**]
---
1. `outfit ls`: every profile, its tier, `*` on the active one.
2. `outfit show <profile>`: its toggles and files, without switching.
3. `outfit use <profile>`: swap its files in (add `--here` for this worktree only).
4. `git status` stays clean; `outfit status` lists the swapped files.
5. `outfit use base` puts back exactly the committed files.

## What it does not do
`use` refuses while a profile you are leaving has uncommitted edits or a swapped file is staged.
The error prints the fix.
