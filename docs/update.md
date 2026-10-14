# Updating outfit

```sh
outfit update                    # install the latest release
outfit update --check            # only say whether there is one (exit 0 either way)
outfit update --version v0.2.0   # exactly this release; going back to an older one is allowed
outfit update --json             # every command takes --json
```

`outfit update` does the work itself; it does not re-run the install script.

1. **Finds the release.** The latest one, or exactly `--version`. While the repository
   is private it needs a GitHub token that can read it, looked for in this order:
   `OUTFIT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, then `gh auth token`. With a token
   it asks the GitHub API and downloads through the asset endpoints; without one it tries
   the public `releases/latest/download/…` URLs, which start working by themselves once
   the repository is public. With neither it stops and says: `gh auth login`, or set
   `OUTFIT_GITHUB_TOKEN`. The token is never printed, never put on a command line, sent
   only to the API host over https, and not forwarded when GitHub redirects the download
   to its storage. One attempt per request, 5 seconds for the small ones: no retry loops.
2. **Verifies, and fails closed.** `checksums.txt.minisig` must be a valid minisign
   signature of `checksums.txt` by one of the keys compiled into this binary
   ([`internal/update/keys.txt`](../internal/update/keys.txt)); then the archive's
   SHA-256 must match `checksums.txt`; then the archive may hold only `outfit`,
   `LICENSE`, `README.md` and `NOTICE`, as plain files. With a token it also requires
   the archive to be the build the release is named for, so an old, validly signed build
   cannot be re-uploaded under a newer release. A development build has no version to
   compare and refuses a plain `outfit update` (it can `--version` a release).
3. **Replaces itself atomically**: a temporary file next to the executable (so the rename
   cannot cross a filesystem), mode 0755, then a rename. A running outfit keeps its old
   copy, and the hook shims point at the same path, so hooks keep working through an
   update. Symlinks are followed, so a link into a package manager's folder updates the
   file it points at. On macOS the quarantine flag is removed if there is one; the
   signature inside the release is never touched.
4. **If the folder is not writable**, nothing changes and the exact command to run is
   printed (exit 3; outfit never uses `sudo`):
   `curl -fsSL -H "Authorization: Bearer $(gh auth token)" https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | OUTFIT_INSTALL_DIR="$HOME/.local/bin" sh`.
5. **One at a time.** A second `outfit update` while one runs exits 0 with a note.

## The update banner

`Update available: 0.1.0 → 0.2.0. Run outfit update` is printed (to stderr) after the
human-facing commands (`status`, `ls`, `use`, `set`, `init`, `show`, `new`), and only when
stderr is a terminal, `--json` is off, `CI` is unset and `OUTFIT_NO_UPDATE_CHECK` is
unset. It is **never** shown, and the network is never touched, by `sync`, `get`, `hook`
or `diff`: git runs those inside hooks and they must stay fast.

The check happens at most once every 24 hours; the answer is cached in
`<user cache dir>/outfit/update.json` (`~/Library/Caches` on macOS, `$XDG_CACHE_HOME` or
`~/.cache` elsewhere). It runs in the background with an 800 ms budget: if it is not done
by the time the command is, it is skipped and tried next time. The cache is written only
after a check succeeded, so a failed one never hides an update for a day.

## Environment variables

| Variable | Effect |
|---|---|
| `OUTFIT_GITHUB_TOKEN` | A GitHub token that can read the repository (first in the lookup order above). |
| `OUTFIT_NO_UPDATE_CHECK` | Set to anything to switch the banner off. |
| `OUTFIT_BASE_URL` | Public download base. For tests; same meaning as in `install.sh`. |
| `OUTFIT_API_URL` | API base. For tests; same meaning as in `install.sh`. |

`tests/validate-install-contract.sh` fails CI if the variables the installer and the
updater share drift apart, or if the Go code reads one that no document mentions.

## Key rotation

The keys compiled in are a list, so a rotation is two releases: ship a release that adds
the new key, then switch the signing secret ([release.md](release.md)).
