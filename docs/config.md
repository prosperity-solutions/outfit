# Configuration and toggles

outfit has two kinds of configuration: **toggles** (behaviour switches such as
`biome = off`) and the **active profile**. Both are plain git config for you, and
two small committed files for the team.

## The committed files

Both use git-config syntax, so the `[outfit]` section is required:

```ini
# .outfit/config: team defaults, for users and non-users alike
[outfit]
    locked = secrets-scan, hook.pre-push
    secrets-scan = on
    biome = on
[outfit "hook"]
    pre-push = on

# .outfit/profiles/yolo/profile.config: this profile's toggles
[outfit]
    biome = off
    tests = off
[outfit "hook"]
    pre-commit = off      # the dispatcher skips the chained team pre-commit entirely
```

- **Write hook toggles as `[outfit "hook"]`.** outfit also accepts
  `hook.pre-commit = off` inside `[outfit]`, but git itself rejects that line, and
  `git config -f .outfit/config --get …` (what teammates *without* outfit run) then
  fails for the whole file.
- `profile` and `locked` are reserved. `profile` is never allowed in a committed
  file (the active profile is personal). `locked` is only valid in `.outfit/config`.
- `base` is the reserved profile that means "no swaps".

### Committed config is untrusted

outfit **never** `include.path`s a committed file: a committed git config could set
`core.fsmonitor`, `core.pager`, `core.hooksPath` or `includeIf`, which is code
execution on every teammate's machine. outfit parses the two files itself and
accepts **only** `outfit.*` keys. Everything else (other sections, includes, bad
lines, a symlink or FIFO instead of a regular file, a file over 1 MiB) is ignored,
and `outfit status` prints each one with its line number.

## Where a value comes from

`outfit get <key>` returns the first hit, highest first:

1. `--worktree` (`outfit set key=val --here`)
2. `--local` (`outfit set key=val`, this clone: the default)
3. `--global` (`outfit set key=val --global`, this machine)
4. the active profile's `profile.config`
5. `.outfit/config`

**Locked keys** always resolve from `.outfit/config`. Every other source is ignored
and `status` warns about it. `outfit set` refuses to set a locked key.

The **active profile** is `outfit.profile`: `--worktree`, then `--local`, else
`base`. A `--global` value is ignored with a warning (profile names are per repo).
A profile name that no longer exists is treated as `base`, with a warning.

## Commands

| Command | What it does |
|---|---|
| `outfit get <key>` | prints the value. Unset: prints nothing and exits **1** (like `git config --get`). One git process, no writes. |
| `outfit set <key>=<val>… [--here\|--global]` | personal override in git config; `key=` unsets. `--here` needs `extensions.worktreeConfig` (`outfit init` enables it). |
| `outfit ls` | profiles across the three tiers (`committed`, `local`, `worktree`), their keys and files; `*` marks the active one. |
| `outfit status` | the active profile, where every toggle comes from, the files it swaps, and warnings. Enforces git ≥ 2.36. |

All of them accept `--json`. Failures carry `"fix"` and `"retryable"`.

Exit codes: `0` ok, `1` error (or an unset `get`), `2` usage or unknown
profile/key, `3` needs a human, `4` out of sync or stale (`status` only).

## Profile tiers

| Tier | Lives in | Versioned |
|---|---|---|
| committed | `.outfit/profiles/<name>/` | yes |
| local | `<git-common-dir>/outfit/profiles/<name>/` | never |
| worktree | `<git-dir>/outfit/worktree-profiles/<name>/` | never; removed with the worktree |

Profile names are unique across tiers; a committed profile wins a clash.

## Toggling from a team hook

```sh
v=$(outfit get biome 2>/dev/null || git config -f .outfit/config --get outfit.biome)
[ "$v" = off ] && exit 0
```

Works with outfit installed and without it.
