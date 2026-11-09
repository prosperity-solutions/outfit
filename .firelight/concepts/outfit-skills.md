---
format: 1
title: outfit for coding agents
summary: The outfit binary carries its own agent docs (outfit skills); the installed skill is only a pointer, and init --agents syncs at session start.
aliases: [agent skills, outfit skills, outfit skills install, init --agents, claude code, coding agent, skill]
tags: [agents]
related: [hook-dispatcher, swapped-files]
covers: [skills/**, internal/agents/**, docs/skills.md]
---
- `outfit skills` lists eight topics; `outfit skills basics` is the one to read first.
- `outfit skills install` writes a pointer skill to `~/.claude/skills/outfit/` and
  `~/.agents/skills/outfit/` (`--project`: this worktree only). `outfit skills remove` takes it away.
- `outfit init --agents` adds a Claude Code SessionStart entry that runs `outfit sync`.

## What it is not
- The pointer holds no docs, so it never goes stale: it reads the outfit you have installed.
