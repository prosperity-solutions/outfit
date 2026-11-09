---
format: 1
title: Install outfit
summary: Install the outfit binary with one curl line, no sudo and no token; it verifies the checksum and the release signature.
aliases: [install, installation, curl install, install.sh, OUTFIT_VERSION, setup]
tags: [setup]
links: [install-script, releases]
related: [try-outfit, update-or-uninstall]
covers: [install.sh, docs/install.md]
---
1. `curl -fsSL https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh`
2. It puts `outfit` in `~/.local/bin`. On a Mac that folder may not be on your `PATH`; the
   installer prints the line to add to `~/.zshrc`.
3. Check with `outfit version` (it also shows the git version; outfit needs git 2.36+).

`OUTFIT_VERSION=0.3.1` installs one release. `OUTFIT_GITHUB_TOKEN` is only for a private fork
or mirror; the public download needs no token.

## What it does not do
No sudo, no login. It does not touch any repository until you run `outfit init` or `outfit use` there.
