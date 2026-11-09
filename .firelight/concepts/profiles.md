---
format: 1
title: Profiles
summary: A profile is your own agent instructions and settings (AGENTS.md, CLAUDE.md, skills, toggles), used only locally on your machine; base is the team's files.
aliases: [profile, outfit profile, outfit, base, yolo, .outfit/profiles, profile.config, personal agents.md, own agent instructions, personal profile, private agent instructions]
tags: [profiles]
links: [profiles-doc]
related: [profile-tiers, swapped-files, toggles, switch-profile]
covers: [internal/profiles/**, docs/profiles.md, skills/topics/profiles.md]
---
```
.outfit/
  config                       team defaults (committed)
  profiles/<name>/
    profile.config             the profile's toggles
    AGENTS.md, apps/web/AGENTS.md …   files it swaps in, at the same relative paths
```

- `outfit ls` lists profiles; `outfit show <p>` shows one; `outfit use <p>` switches.
- `base` is the profile with no swaps: exactly the committed repository.
- `replace = <dir>` in `profile.config` replaces a whole directory, e.g. `.claude/skills`.

## What it is not
- Not a change for the team: committed files never change.
- Not for new files: a profile swaps **tracked** files only. A file the repo does not have is
  skipped, except below a `replace` directory.
