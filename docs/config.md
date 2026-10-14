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
lines, control characters in a value, a file over 1 MiB) is ignored, and
`outfit status` / `outfit ls` print each one with its line number where it has one.

It also never follows a committed symlink: `.outfit/`, `.outfit/profiles/`, a
profile directory and `config`/`profile.config` must be real directories and
regular files, and symlinks inside a profile are not swap sources.

The parser reads the same syntax as git (BOM, CRLF, quoting, comments, backslash
continuation), so outfit users and teammates running
`git config -f .outfit/config --get …` see the same values; a test compares the two
over a corpus.

## Values

A toggle is a string. By convention it is exactly `on` or `off`, and hooks compare
against the literal `off`. So `outfit set` turns `OFF`, `false`, `no` and `0` into
`off` (and `ON`, `true`, `yes`, `1` into `on`); any other value (`fast`) is kept as
typed. In a committed file nothing is rewritten, because teammates without outfit
read the raw text: `ON`, `OFF` (any case other than exactly `on`/`off`), `false`,
`no`, `0`, `true`, `yes`, `1` and a bare key get a warning telling you to write `on` or `off`.

After `outfit init` (see [hooks.md](hooks.md)) `hook.<name> = off` skips that hook
for you without any change to the team's hooks. Other toggles (`biome`, `tests`…)
only affect hooks that ask for them with `outfit get` (see the snippet at the end).

## Where a value comes from

`outfit get <key>` returns the first hit, highest first:

0. the **command scope**: `git -c outfit.biome=off commit` (and `GIT_CONFIG_COUNT`),
   which hooks inherit. The way to skip a check once. A locked key still wins.
1. `--worktree` (`outfit set key=val --here`)
2. `--local` (`outfit set key=val`, this clone: the default)
3. `--global` (`outfit set key=val --global`, this machine)
4. the active profile's `profile.config`
5. `.outfit/config`

The system scope (`/etc/gitconfig`) is **ignored** (with a warning), and so is
`outfit.profile` given through `git -c` or set globally. `set` writes one key at a
time and is not atomic: if the third write fails, the first two stay.

**Locked keys** always resolve from `.outfit/config`. Every other source is ignored
and `status` warns about it. `outfit set` refuses to set a locked key. Write the lock for a hook toggle as
`locked = hook.pre-push`, not `locked = pre-push` (the parser warns about the
second spelling: it would lock a key nobody sets). A locked key
with no team value stays unset (`status` shows `(unset)`).

The **active profile** is `outfit.profile`: `--worktree`, then `--local`, else
`base`. A `--global` value is ignored with a warning (profile names are per repo).
A profile name that no longer exists is treated as `base`, with a warning.

## Commands

| Command | What it does |
|---|---|
| `outfit get <key>` | prints the value. Unset: prints nothing and exits **1** (like `git config --get`). One git process, no writes. |
| `outfit set <key>=<val>… [--here\|--global]` | personal override in git config; `key=` unsets (in that scope only: it prints which scopes still set the key). `--global` works outside a repo. `--here` needs `extensions.worktreeConfig`, which `outfit init` enables. Note that git copies a worktree's `config.worktree` into every worktree created afterwards, so `--here` in the main worktree leaks into new ones until outfit strips the copies on `worktree add`. |
| `outfit ls` | profiles across the three tiers (`committed`, `local`, `worktree`), their keys and files; `*` marks the active one. |
| `outfit init` / `outfit uninstall` | install or remove the hook dispatcher: [hooks.md](hooks.md). |
| `outfit status` | the active profile, where every toggle comes from, the files it swaps, and warnings. Enforces git ≥ 2.36. |

All of them accept `--json`. Failures carry `"retryable"` and, when there is one, the exact `"fix"` command. `ls` shows a built-in `base` row (tier `builtin`): the profile that means no swaps.

Exit codes: `0` ok, `1` error (or an unset `get`: there is no registry of known
keys, so an unset key is "not found", like `git config --get`, never "unknown"),
`2` usage or unknown profile, `3` needs a human and `4` out of sync or stale (`status` only): the last two are reserved and not emitted yet.

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

## Deviations from `docs/HANDOVER.md`

Decisions made while building, kept here so the reason outlives the PR:

| Spec | What outfit does | Why |
|---|---|---|
| §3.2 lists worktree, local, global, profile, team | adds the **command** scope (`git -c`, `GIT_CONFIG_COUNT`) on top | hooks inherit it; the natural way to skip one check once. A locked key still wins. |
| §3.1 `<git-dir>/outfit/profiles/` for worktree profiles | `<git-dir>/outfit/worktree-profiles/` | in the main worktree the git dir *is* the common dir, so the two tiers would share a directory. |
| §3.1 example `hook.pre-commit = off` in `[outfit]` | accepted, with a warning | git rejects that line and so does every teammate's `git config -f`; the subsection form is valid for both. |
| §4 exit 2 = "unknown profile/key" | `get` of an unset key exits **1** | there is no registry of keys; it mirrors `git config --get`, which the §3.0 fallback chains with `||`. |
| §2 `version` floor check | `version` warns and exits 0 on old git | `install.sh` prints it last; it must not fail after a good install. |
| §3.6 shim only what exists | a shim for **every** client hook, resolved at run time | a hook added after `init` (a new secret scan, husky created by a later `npm install`) would otherwise be skipped silently. |
| §3.6 `<git-dir>/config.worktree` via `git config --worktree` | the same files, written with `git config --file` | works for worktrees whose checkout is missing. |
| §4 uninstall removes `outfit.*` | removes only what was added after `init` | settings from before `init` are the user's; `git config --list` still comes back identical. |
| (unspecified) | a committed profile wins a name clash, even on the hot path (`get`) | per spec §3.1; `status` and `ls` warn. |
