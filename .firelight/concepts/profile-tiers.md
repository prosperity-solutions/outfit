---
format: 1
title: Committed, local and worktree profiles
summary: Where a profile lives decides who has it, the team (committed in .outfit/profiles), this clone only (local), or one worktree.
aliases: [tier, tiers, local profile, committed profile, worktree profile, promote, shared profile, private profile]
tags: [profiles]
related: [profiles, create-a-profile, use-with-worktrees]
covers: [internal/profiles/**, docs/profiles.md]
---
| Tier | Lives in | Versioned |
|---|---|---|
| committed | `.outfit/profiles/<name>/` | yes, shared with the team |
| local | `<git-common-dir>/outfit/profiles/<name>/` | never |
| worktree | `<git-dir>/outfit/worktree-profiles/<name>/` | never; gone with `git worktree remove` |

- Names are unique across tiers. If a teammate later commits the same name, the committed one wins.
- `outfit promote <name>` moves a local profile into `.outfit/profiles/` so you can commit it.
- Committed profiles are symlinked; local and worktree ones are hard-linked.

## What it is not
- Local and worktree profiles have no history and no undo: plain files, edited in place.
