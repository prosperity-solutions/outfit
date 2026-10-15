# outfit: working in a repository as an agent

- **Check before you edit.** `outfit status --json` lists swapped paths. An edit to one goes to
  the profile. That is usually what the person wants (it is *their* AGENTS.md); if you meant to
  change the team's file, edit it with `outfit use base` active or in a worktree on `base`, and
  say so.
- **Do not hand-edit state.** Never touch `.git/outfit/`, `.git/info/sparse-checkout` or the
  symlinks; use `outfit sync`, `use`, `author`.
- **Inactive profiles are hidden.** To read one use `outfit show <profile> [path]`. To edit or
  check files out of several, `outfit author on` (and `outfit author off` when done).
- **Commits.** Stage by path, not `git add -A` on swapped files; the guard refuses a swapped
  path. Profile edits are committed as `.outfit/profiles/<name>/…`.
- **Toggles.** To skip one check once: `git -c outfit.<key>=off <command>`. To change your own
  default: `outfit set <key>=off`. Do not edit `.outfit/config` to do it unless the team should
  change.
- **Session start.** `outfit init --agents` adds a Claude Code SessionStart entry that runs
  `outfit sync` (personal `.claude/settings.local.json`, kept out of commits).
- **This documentation.** `outfit skills install` puts a small pointer skill where Claude Code
  and other agents read skills; `outfit skills remove` takes it away. The pointer never goes
  stale: the text you are reading is carried by the binary.
