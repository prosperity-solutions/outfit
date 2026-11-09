---
format: 1
title: Toggles
summary: Personal key=value switches like hook.pre-commit=off or biome=off, resolved worktree, clone, machine, profile, team; locked keys always win.
aliases: [toggle, hook.pre-commit=off, outfit set, outfit get, locked, .outfit/config, config, precedence, --global]
tags: [config, hooks]
links: [config-doc]
related: [skip-a-hook, set-up-for-a-team, hook-dispatcher]
covers: [internal/config/**, docs/config.md, skills/topics/toggles.md]
---
- `hook.<name>=off` skips any git hook for you. Other keys (`biome=off`) only matter to a team
  hook that asks with `outfit get`.
- `outfit set key=value` for this clone, `--here` for this worktree, `--global` for this
  machine; `outfit set key=` removes it. `outfit get key` reads it (exit 1 when unset).
- A value comes from the first of: worktree, clone, machine, active profile, the team's
  `.outfit/config`.
- `locked = hook.pre-push` in `.outfit/config` makes a key always come from the team file.

## What it is not
- Not a way around CI: toggles change local hooks only.
- `locked` is not allowed outside `.outfit/config`.
