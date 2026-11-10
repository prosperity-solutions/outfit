---
format: 1
title: Set up outfit for a team
summary: Commit team defaults and locked keys in .outfit/config, commit shared profiles, run init --team, and make team hooks honour a toggle.
aliases: [team setup, .outfit/config, locked, init --team, team defaults, shared profiles, enforce hook, docs/outfit.md]
tags: [config, hooks]
related: [toggles, profiles, hook-dispatcher]
covers: [internal/config/**, internal/cli/cmd_init.go, docs/config.md]
---
1. Commit `.outfit/config` with defaults. Add `locked = hook.pre-push` (or other keys) for
   what nobody may switch off.
2. Commit shared profiles under `.outfit/profiles/<name>/`.
3. `outfit init --team` once: a line in `AGENTS.md` and a `docs/outfit.md` for teammates
   without outfit (it commits nothing).
4. To let a team hook honour a free-form toggle like `biome`:
   ```sh
   v=$(outfit get biome 2>/dev/null || git config -f .outfit/config --get outfit.biome)
   [ "$v" = off ] && exit 0
   ```
   It works with outfit installed and without it.

## What it does not do
Nobody has to install outfit; teammates without it get the committed files.
