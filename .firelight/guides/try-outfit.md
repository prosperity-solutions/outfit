---
format: 1
title: Try outfit in 60 seconds
summary: Run init, switch a hook off, list and use a profile, see git status stay clean, and uninstall, in any repository.
aliases: [quick start, getting started, demo, try it, first steps, scripts/demo.sh]
tags: [setup]
related: [install-outfit, switch-profile, skip-a-hook]
covers: [scripts/demo.sh, README.md]
---
```sh
cd your-repo
outfit init                      # put outfit in front of your git hooks
outfit set hook.pre-commit=off   # skip the pre-commit hook, only for you
outfit ls                        # the profiles in .outfit/profiles (or: outfit new mine)
outfit use <profile>             # swap in its files
git status                       # clean
outfit uninstall                 # put everything back
```

No repository at hand? `scripts/demo.sh` in a clone of outfit builds a throwaway repo and walks
through it all in about half a minute.

## What it does not do
Nothing here changes a committed file or anything a teammate sees.
