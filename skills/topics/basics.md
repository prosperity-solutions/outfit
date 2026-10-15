# outfit: the basics

outfit gives one person a personal view of a repository: their own copies of a few
tracked files (a **profile**) and their own on/off **toggles**, with nothing changed for
the team. `git status` stays truthful.

## Read this before you edit anything

1. **A file can be a link.** With a profile active, a swapped file (say `AGENTS.md`) is a
   symlink into `.outfit/profiles/<profile>/AGENTS.md` (committed profile) or into the git
   directory (local or worktree profile). Editing it edits the *profile's* file. The
   team's file is untouched. A local or worktree profile has no history: there is no undo. `outfit status` lists every swapped path; check it first.
2. **An editor's atomic save replaces the link with a regular file.** `outfit sync` notices and
   copies the new bytes into the profile ("absorb"). If the profile's file changed too (an
   edit through the link since the last `outfit` command counts), it quarantines both under
   `.git/outfit/quarantine/` and exits 3: see `outfit skills troubleshooting`.
3. **Run `outfit sync` after editing through a link and before anything that rewrites the
   file** (an editor's atomic save, a formatter that rewrites files). Hooks run sync after
   checkout, merge and rewrite; `reset --hard` fires nothing.
4. **Never commit a swapped path or the symlink.** The pre-commit guard refuses it and
   prints the fix. Edits to a committed profile show as ` M .outfit/profiles/<name>/…`:
   that is where your edit went. A committed profile is **shared with the team**, so ask the
   person before you commit into one (a local or worktree profile is never in git).
5. **Ask for JSON.** Every command takes `--json`; failures carry `fix` (the exact command)
   and `retryable`.

## Commands

| Command | What it does |
|---|---|
| `outfit status` | active profile, where each toggle comes from, swapped files, in sync or not (exit 4 if not, 3 if a person must look) |
| `outfit sync` | make the files match the active profile; silent when nothing to do |
| `outfit use <profile> [--here]` | switch this clone's (or only this worktree's) profile, then sync |
| `outfit ls` | profiles across the three tiers, `*` marks the active one |
| `outfit show <profile> [path]` | a profile's toggles and files, or one file's content, without switching |
| `outfit get <key>` | a toggle's value; exit 1 and no output when unset |
| `outfit set <key>=<val>… [--here\|--global]` | personal toggle override; `key=` unsets |
| `outfit new <name> [--from <profile>] [--here]` | make a personal profile |
| `outfit promote <name>` | move a local profile into `.outfit/profiles/` so git can version it |
| `outfit author on\|off` | put every committed profile on disk to edit side by side, or hide them again |
| `outfit diff <path>` / `outfit ack <path>` | the base of a swapped file changed under the swap: see it, accept it |
| `outfit init [--agents] [--team]` / `outfit uninstall [--purge]` | install or remove the hook dispatcher (and the Claude Code SessionStart entry); `--team` writes a line in AGENTS.md and docs/outfit.md for teammates without outfit |
| `outfit update [--check]` | install the newest release |
| `outfit skills [topic]` | this documentation |

## Exit codes

`0` fine · `1` error (or `get` of an unset key) · `2` usage or unknown profile ·
`3` a person must look (conflict, quarantine, refusal; the fix is printed) ·
`4` `status`: out of sync or stale.

## More

- `outfit skills profiles` — tiers, layout, how a swap works
- `outfit skills toggles` — `get`/`set`, where values come from, using a toggle in a hook
- `outfit skills authoring` — edit a profile, see the inactive ones, create and promote
- `outfit skills worktrees` — linked worktrees and pinned profiles
- `outfit skills troubleshooting` — stale base, quarantine, refusals, out of sync
- `outfit skills agents` — what an agent should and should not do here
