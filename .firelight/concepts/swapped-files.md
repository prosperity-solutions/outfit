---
format: 1
title: Swapped files
summary: How outfit puts your file where the team's was, a link plus sparse checkout plus hooks, so git status stays clean and nothing is committed.
aliases: [swap, swapped file, symlink, hard link, sparse checkout, git status clean, replace directory, replace a folder, replace, .claude/skills, skills folder, own agents.md, own claude.md]
tags: [profiles, git]
related: [profiles, stale-and-quarantine, edit-a-profile]
covers: [internal/apply/**, internal/plan/**, docs/git-behavior.md]
---
1. A **link** stands where the file was (a symlink into a committed profile, a hard link for a
   local or worktree one).
2. **Sparse checkout** makes git look away from that path, so there is no diff.
3. **Hooks** re-sync after pull, checkout and rebase; a pre-commit guard refuses committing the link.

Editing through the link edits the profile's file. An editor's atomic save leaves a plain copy;
`outfit sync` copies it back into the profile.

## Replace a whole folder
`replace = .claude/skills` in `profile.config` replaces the whole `.claude/skills` folder: the
team's files below it are hidden for you and the profile's stand in, including files the repo
does not have.

## What it is not
- Not invisible to you: `outfit status` lists every swapped path.
- Never overwrites your bytes: if both copies changed, see [quarantine](entry:stale-and-quarantine).
