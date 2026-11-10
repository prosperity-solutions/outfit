---
format: 1
title: Skip a git hook only for yourself
summary: Turn off a slow pre-commit or pre-push hook on your machine with outfit set hook.<name>=off, per worktree, clone or machine.
aliases: [skip pre-commit, disable hook, hook.pre-commit=off, hook.pre-push=off, slow hook, turn off hook, no-verify]
tags: [hooks]
related: [toggles, hook-dispatcher]
covers: [internal/config/**, internal/hooks/**, internal/cli/cmd_set.go]
---
1. `outfit init` once in the repository (if you have not used outfit there yet).
2. `outfit set hook.pre-commit=off` for this clone. `--here` for this worktree only,
   `--global` for every repo on this machine.
3. Check with `outfit get hook.pre-commit` or `outfit status`.
4. Undo: `outfit set hook.pre-commit=`.

Works for any git hook name, e.g. `hook.pre-push=off`.

## What it does not do
It cannot skip a key the team marked `locked` in `.outfit/config`, and it does not skip CI.
