# outfit: toggles

A **toggle** is a string behaviour switch (`biome = off`, `hook.pre-push = off`). By
convention `on` or `off`; hooks compare against the literal `off`. `outfit set` turns `OFF`,
`false`, `no`, `0` into `off` and `ON`, `true`, `yes`, `1` into `on`; anything else is kept as
typed.

```sh
outfit get biome                    # prints the value; unset: prints nothing, exit 1
outfit set biome=off                # this clone (default scope)
outfit set biome=off --here         # this worktree only
outfit set biome=off --global       # this machine (works outside a repo)
outfit set biome=                   # unset (in that scope only; it says which scopes still set it)
git -c outfit.biome=off commit      # once: the command scope, hooks inherit it
```

## Where a value comes from (first hit wins)

0. command scope (`git -c outfit.k=v …`, `GIT_CONFIG_COUNT`)
1. `--worktree` (`outfit set … --here`)
2. `--local` (`outfit set …`)
3. `--global` (`outfit set … --global`)
4. the active profile's `profile.config`
5. `.outfit/config` (team defaults)

`/etc/gitconfig` is ignored. **Locked keys** (`locked = …` in `.outfit/config`) always resolve
from the team file; `outfit set` refuses them and `status` warns about overrides.
`outfit status` shows every key with its source.

## Hook toggles

`hook.<name> = off` skips that hook for you after `outfit init`, with no change to the team's
hooks. Write it in committed files as `[outfit "hook"]` with `pre-commit = off`, not
`hook.pre-commit = off` inside `[outfit]` (git itself rejects that line).

## Using a toggle in a team hook (works with and without outfit)

```sh
v=$(outfit get biome 2>/dev/null || git config -f .outfit/config --get outfit.biome)
[ "$v" = off ] && exit 0
```

## Files

Both use git-config syntax and the `[outfit]` section. Committed config is **untrusted**: only
`outfit.*` keys are read, includes are never followed, `profile` is not allowed in a committed
file, and symlinks inside `.outfit/` are ignored. In committed files write exactly `on` or
`off`: `ON`, `false`, `0` … get a warning because teammates read the raw text.
