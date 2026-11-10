---
format: 1
title: Update or uninstall outfit
summary: Install the newest signed release with outfit update, or take outfit out of a repository with outfit uninstall (--purge also deletes your profiles).
aliases: [update, upgrade, outfit update, uninstall, remove outfit, outfit uninstall, --purge]
tags: [setup]
related: [install-outfit]
covers: [internal/cli/cmd_update.go, internal/update/**, docs/update.md, skills/topics/updating.md]
---
- `outfit update` installs the newest release after checking its signature; `--check` only looks.
- `outfit uninstall` puts every file back and undoes hooks and settings in every worktree.
  Your local profiles stay.
- `outfit uninstall --purge` also deletes your local and worktree profiles.
- `outfit skills remove` removes the agent pointer skill.

## What it does not do
`outfit update` never runs inside a hook.
