# outfit: installing, updating, removing

```sh
curl -fsSL https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
outfit update              # newest release; --check only looks; --version vX.Y.Z for one
outfit version             # version, commit, build date, git version found (needs git 2.36+)
outfit uninstall           # puts every file back, undoes hooks and settings (your profiles stay)
outfit uninstall --purge   # ... and deletes your local and worktree profiles
```

While the repository is private, installing and updating need a GitHub token that can read it:
`OUTFIT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, or `gh auth login`. Both verify the SHA-256
and the minisign signature of the release (nothing to install first). `outfit update` never
runs in a hook and prints an update banner on human commands only.
