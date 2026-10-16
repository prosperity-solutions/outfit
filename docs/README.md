# outfit documentation

outfit lets you run a git repository your own way (your `AGENTS.md`, your skills, your hook checks)
without changing anything for the team: nobody else's diff moves and `git status` stays clean.
Start with the [project README](../README.md) for the one-minute version.

## Using it

| Read | For |
|---|---|
| [install.md](install.md) | installing outfit (one line, no token), what the script checks |
| [profiles.md](profiles.md) | profiles, swapping files, replacing a whole folder, `outfit sync` |
| [config.md](config.md) | toggles, `.outfit/config`, where a value comes from, locked keys |
| [hooks.md](hooks.md) | how outfit sits in front of your git hooks, `init --team` |
| [update.md](update.md) | `outfit update`: what it verifies before it replaces itself |
| [skills.md](skills.md) | `outfit skills`: how coding agents learn to use outfit |
| [website.md](website.md) | the website in `site/`: how to preview it and how it goes live |

## Trust

| Read | For |
|---|---|
| [security.md](security.md) | what protects the build and the workflows, known limits |
| [release.md](release.md) | how a release is made, signed and checked, key rotation |
| [macos-signing.md](macos-signing.md) | Apple signing and notarization |

## How it works inside

| Read | For |
|---|---|
| [git-behavior.md](git-behavior.md) | what git really does (sparse checkout, skip-worktree, hooks), each row pinned by a test |
| [testing.md](testing.md) | the test layers: scripts, properties, fault injection, fuzzing |
| [HANDOVER.md](HANDOVER.md) | the original plan the CLI was built from |

## Contributing

| Read | For |
|---|---|
| [ship.md](ship.md) | the workflow every change goes through |
| [agentic-review.md](agentic-review.md) | the review loop behind it |
| [../AGENTS.md](../AGENTS.md) | conventions, the documentation checklist |
