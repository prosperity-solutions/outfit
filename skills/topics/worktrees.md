# outfit: linked worktrees

Each worktree has its own links and sparse patterns; a sync runs per worktree. `use` syncs the
worktree it runs in; the others follow on their next trigger or `outfit sync`.

```sh
outfit use yolo              # this clone's profile (every worktree without its own pin)
outfit use yolo --here       # only this worktree
outfit new scratch --here    # a worktree profile; removed with `git worktree remove`
```

- `git worktree add` copies the main worktree's `config.worktree`, a pinned profile included.
  On the `post-checkout` that `worktree add` fires, outfit unsets the copied `outfit.*` keys in
  the new worktree, so it follows the clone's default until `outfit use <p> --here`.
  `git worktree add --no-checkout` fires no hook: run `outfit use <profile> --here` yourself.
- Local and worktree profiles live in the git dir, outside the working tree. A worktree profile
  of *another* worktree still reserves its name.
- `outfit uninstall` works across every worktree and stops (exit 1, listing the file) if a link
  cannot be put right; rerun after fixing it.
- Syncs are serialised by an `flock` in the worktree's git dir; there is never a stale lock.
