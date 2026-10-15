# outfit: installing, updating, removing

```sh
curl -fsSL https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
outfit update              # newest release; --check only looks; --version vX.Y.Z for one
outfit version             # version, commit, build date, git version found (needs git 2.36+)
outfit uninstall           # puts every file back, undoes hooks and settings (your profiles stay;
                           # skills put there by `outfit skills install` stay: `outfit skills remove`)
outfit uninstall --purge   # ... and deletes your local and worktree profiles
```

The repository and its releases are public: installing and updating need no login and no token.
Only `OUTFIT_GITHUB_TOKEN` is ever used, and only when you set it (a private fork or mirror, or a
rate-limited network); `GH_TOKEN`, `GITHUB_TOKEN` and `gh auth` are not looked at. Both verify the SHA-256
and the minisign signature of the release. `outfit update` does it in-process; `install.sh`
uses a `minisign` on your PATH or, when there is none, downloads a pinned copy of the
official build and checks its hash first, so nothing has to be installed beforehand. `outfit update` never
runs in a hook and prints an update banner on human commands only.
