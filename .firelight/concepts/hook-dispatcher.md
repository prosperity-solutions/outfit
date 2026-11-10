---
format: 1
title: The hook dispatcher
summary: outfit init puts small shims in front of your git hooks; the team's own hooks (husky, lefthook, .git/hooks) still run first, unchanged.
aliases: [outfit init, core.hooksPath, husky, lefthook, git hooks, hooks, dispatcher, shim]
tags: [hooks]
links: [hooks-doc]
related: [toggles, skip-a-hook]
covers: [internal/hooks/**, docs/hooks.md]
---
- `outfit init` writes a shim per git hook in `<git-common-dir>/outfit/hooks/` and points
  `core.hooksPath` there in every worktree's config.
- On each hook, outfit runs the **team's hook first**, with the same arguments, stdin and exit
  code, unless a toggle says `hook.<name>=off`. Then it re-syncs swapped files.
- `outfit use` runs `init` itself when outfit was never installed.
- `outfit uninstall` undoes it in every worktree.

## What it is not
- Not a hook manager replacing husky or lefthook. It sits in front of them.
