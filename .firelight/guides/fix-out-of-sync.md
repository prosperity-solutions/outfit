---
format: 1
title: Fix out of sync, stale or quarantine
summary: What to do when outfit status exits 3 or 4, says stale or quarantine, or files don't match the profile after git reset --hard.
aliases: [outfit status, out of sync, stale, quarantine, exit 3, exit 4, troubleshoot, broken, symlink staged, reset --hard]
tags: [troubleshooting]
related: [stale-and-quarantine, swapped-files]
covers: [internal/cli/cmd_status.go, internal/cli/cmd_stale.go, skills/topics/troubleshooting.md]
---
Start with `outfit status`. Every failure prints the exact `fix`.

| Symptom | Fix |
|---|---|
| `stale` | `outfit diff <path>` to see the team's change, `outfit ack <path>` to accept it |
| exit 3, `quarantine` | compare the copies in `.git/outfit/quarantine/…`, put what you keep in the profile's copy, remove the swapped file, `outfit sync` |
| exit 3, merge conflict in a swapped path | resolve the base file, `git add --sparse <path> && git commit`, `outfit sync` |
| files wrong after `git reset --hard` | `outfit sync` (no hook fires for it) |
| symlink staged or path shows ` T` | `git reset -q && outfit sync` |

## What it does not do
Never delete `.git/outfit/` by hand: `outfit uninstall` restores every base file first.
