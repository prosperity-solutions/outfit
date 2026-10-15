# Agent skills: `outfit skills`

Coding agents (Claude Code, Codex, Cursor, …) read "skills": a folder with a `SKILL.md`
that says when to use it. outfit carries its agent documentation **inside the binary**, so
what an agent reads always describes the outfit that is installed, and a skill file that was
copied somewhere months ago cannot go stale.

## Two parts

| Part | Where | Size | What it is |
|---|---|---|---|
| the pointer skill | `skills/outfit/SKILL.md` in this repository, embedded in the binary | about 2 KB | frontmatter (when to use it), a paragraph, and "run `outfit skills`" |
| the topics | `skills/topics/*.md`, embedded in the binary | 1 to 4 KB each | the documentation: `basics`, `profiles`, `toggles`, `authoring`, `worktrees`, `troubleshooting`, `agents`, `updating` |

A skill's whole body enters an agent's context when it matches, so the pointer stays small
and nothing else is loaded until the agent asks for a topic. The index shows each topic's
size so the agent can choose before it spends the call.

## Commands

```sh
outfit skills                      # the index: topic, size, one line
outfit skills basics               # one topic (stdout only, ready to pipe into a context)
outfit skills --json               # the index as JSON; with a topic: {"name","summary","body"}
outfit skills install              # the pointer into ~/.claude/skills/outfit and ~/.agents/skills/outfit
outfit skills install --project    # ... into this worktree's .claude/skills and .agents/skills
outfit skills remove [--project]   # take it away again
outfit skills install --dry-run    # say what would change
```

Reading needs no repository, no `outfit init` and no git: an agent asking how outfit works may
be standing anywhere. A wrong topic name lists the topics and suggests the nearest.

`install` and `remove`:

- write only a `SKILL.md` whose frontmatter carries `managed-by: outfit`. A `SKILL.md` you
  wrote yourself is never replaced or removed (exit 3, the file named); nothing is written
  until every target has been checked;
- never write through a link outside the folder (an absolute link, or one that leaves it, is
  refused with exit 3); inside a worktree (`--project`) any link on the way is refused, because
  a repository can commit `.claude` as a link and git does not look through links; in your home
  directory a relative link that stays inside it (a dotfiles setup) is followed. The file is
  written beside its place, created exclusively (a leftover temp file or a planted link there is
  removed, never followed) and renamed into place, so a reader never sees half a file;
- are idempotent: a second `install` says `unchanged`; an older pointer of outfit's own is
  `updated`;
- with `--project`, keep the files out of `git add -A` through `.git/info/exclude` (the skill is
  personal, like the SessionStart entry), take that entry away on `remove`, and refuse a skill
  folder git tracks (the team's own);
- on `remove`, check every target first (a foreign `SKILL.md` anywhere means nothing is removed),
  delete the `outfit` and `skills` folders only while they are empty real directories (never a
  link, never `.claude` or `.agents` themselves), and keep the shared `.git/info/exclude` entry
  while another worktree still has the skill;
- are separate from `outfit uninstall`, which does not remove skills: `outfit skills remove`
  does (run it in each worktree you used `--project` in).

## Keeping the documentation true

A topic that names a flag the binary does not have is a bug that ships. `go test ./internal/cli`
reads every `outfit <command> …` inside code in the topics and the pointer, and fails when the
command does not exist or does not take a flag written beside it. `go test ./skills` checks
that every topic file is listed and every listed topic exists, and that the pointer stays small
and carries the marker. When you change a command or a flag, change its topic in the same
pull request (the documentation checklist in `AGENTS.md` lists it).
