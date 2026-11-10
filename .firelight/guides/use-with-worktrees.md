---
format: 1
title: Use profiles in linked worktrees
summary: Pin one git worktree to its own profile with outfit use --here, or make a throwaway profile for one worktree with outfit new --here.
aliases: [worktree, git worktree, --here, worktree profile, pin profile, worktree add]
tags: [profiles, git]
related: [profile-tiers, switch-profile]
covers: [internal/hooks/**, skills/topics/worktrees.md]
---
- `outfit use yolo` sets the clone's profile for every worktree without its own pin.
- `outfit use yolo --here` pins only this worktree.
- `outfit new scratch --here` makes a worktree profile; it goes with `git worktree remove`.
- A new worktree follows the clone's profile until you pin it.
- `git worktree add --no-checkout` fires no hook: run `outfit use <profile> --here` yourself.

## What it does not do
`use` syncs only the worktree it runs in; others follow on their next hook or `outfit sync`.
