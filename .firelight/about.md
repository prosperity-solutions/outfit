# outfit

**One repo. Many outfits.** outfit swaps files in a git repository (`AGENTS.md`, `CLAUDE.md`,
a skills folder) for your own versions and switches hook checks on or off, only on your
machine. `git status` stays clean, nobody else's diff changes, and your team does not need
to use it.

How it works:

1. A **link** stands where the file was, pointing into your profile.
2. **Sparse checkout** tells git to look away from that path, so there is no diff.
3. **Hooks** re-sync after pull, checkout and rebase, and a commit guard refuses committing the link.

A single static Go binary for macOS (universal) and Linux (amd64, arm64); needs git 2.36+.
Every command takes `--json`, never prompts, can be run twice safely, and prints the exact fix
when it fails. MIT licensed.
