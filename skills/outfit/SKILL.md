---
name: outfit
description: >
  Work with outfit, the personal-profile layer for a git repository: swap your own
  AGENTS.md or other tracked files in without touching git status, switch toggles
  (biome=off, hook.pre-push=off) for yourself only, keep team hooks working, and
  create, edit, promote or hide profiles. Use this skill when the repository has a
  `.outfit/` directory, when the user mentions outfit, profiles, "my own AGENTS.md",
  swapped files, toggles, `outfit sync` or `outfit status`, or when a file in the
  working tree is a symlink into `.outfit/profiles/` or a git directory. Covers every
  `outfit` command.
metadata:
  managed-by: outfit
  author: prosperity-solutions
  version: "1.0.0"
---

# outfit

outfit lets one person run a repository their own way (their `AGENTS.md`, their
hook toggles) without changing anything for teammates and without a dirty
`git status`.

**This file is only a pointer.** The documentation is carried by the `outfit` binary and
describes the outfit you actually have installed. Nothing below it enters your context
until you ask for it:

```sh
outfit skills              # the index: every topic, its size and one line
outfit skills basics       # start here: the command surface and the rules that stop you guessing wrong
outfit skills <topic>      # one topic when the task needs it
```

Before you edit a file in a repository that has `.outfit/`, run `outfit status`: a file
you are about to change may be a link into a profile, and the edit then goes to the
profile, not to the team's file. `outfit skills basics` says what that means.

If `outfit` is not installed: `curl -fsSL https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh`
(the repository is private while it is private: see `docs/install.md` for the token).
