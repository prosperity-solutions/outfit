# Updating outfit

```sh
outfit update                    # install the latest release
outfit update --check            # only say whether there is one (exit 0 either way)
outfit update --version v0.2.0   # exactly this release; going back to an older one is allowed
outfit update --json             # every command takes --json
```

`outfit update` does the work itself; it does not re-run the install script.

1. **Finds the release.** The latest one, or exactly `--version`. The repository and its releases are
   public, so it downloads from the public `releases/latest/download/…` URLs with no login and no token.
   A token is used only when `OUTFIT_GITHUB_TOKEN` is set on purpose (a private fork or mirror, or a
   network that rate-limits anonymous downloads): then it asks the GitHub API and downloads through the
   asset endpoints. `GH_TOKEN`, `GITHUB_TOKEN` and `gh auth` are **not** looked at (a stale ambient
   token would only make a public update fail). outfit never prints the token or puts it on a command
   line, and sends it only to `https://api.github.com` (or, for tests, to this machine; an
   `OUTFIT_API_URL` naming anywhere else is refused before any request), and not forwarded when GitHub
   redirects the download to its storage or anywhere over plain http. One attempt per
   request, no retry loops: 5 seconds for the small files; the archive (3 to 6 MB) is cut
   off when **no data arrives for 15 seconds** or after 10 minutes in all, so a slow link
   that keeps delivering is not abandoned (a deliberate exception to the spec's flat 5 s).
2. **Verifies, and fails closed.** `checksums.txt.minisig` must be a valid minisign
   signature of `checksums.txt` by one of the keys compiled into this binary
   ([`internal/update/keys.txt`](../internal/update/keys.txt)); then the archive's
   SHA-256 must match `checksums.txt`; then the archive must hold a plain `outfit`
   file, and nothing that is not a plain file with a plain name (no links, directories,
   paths, duplicates). Other plain files are skipped, never extracted: an installed outfit
   cannot be changed, so a later release may add a man page without breaking every update.
   (`install.sh`, which is fetched fresh each time, is stricter.) With a token it also requires
   the archive to be the build the API says the release is named for, which narrows (does not close)
   re-uploading an old, validly signed build under a newer release: neither the release's tag
   nor "latest" is part of what the signature covers, on either path (a known limit, see `security.md`). "Is there a newer release?" is also
   answered from the signed checksum list, so an unsigned answer cannot make outfit
   believe it is up to date (the banner is the one exception: it is only a hint). A
   development build has no version to compare and refuses a plain `outfit update` (it
   can `--version` a release).
3. **Replaces itself atomically**: a temporary file next to the executable (so the rename
   cannot cross a filesystem), mode 0755, then a rename. A running outfit keeps its old
   copy, and the hook shims point at the same path, so hooks keep working through an
   update. Symlinks are followed, so a link into a package manager's folder updates the
   file it points at. On macOS the quarantine flag is removed if there is one; the
   signature inside the release is never touched.
4. **If the folder is not writable**, nothing changes and the exact command to run is
   printed (exit 3; outfit never uses `sudo`). It installs the latest release into
   `~/.local/bin`; put that folder before the old one on your PATH and run `outfit init`
   in each repository, so the hook shims point at the new copy:
   `curl -fsSL https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | OUTFIT_INSTALL_DIR="$HOME/.local/bin" sh`.
5. **One at a time.** A second `outfit update` while one runs exits 0 with a note.

## The update banner

`Update available: 0.1.0 → 0.2.0. Run outfit update` is printed (to stderr) after the
human-facing commands (`status`, `ls`, `set`, `init`; `use`, `show` and `new` join the
list when they exist: `update.BannerCommands`), and only when stderr is a terminal (not
a redirect to /dev/null), `--json` is off, `CI` is unset and `OUTFIT_NO_UPDATE_CHECK` is
unset. It is **never** shown, and the network is never touched, by `sync`, `get`, `hook`
or `diff`: git runs those inside hooks and they must stay fast.

The check happens at most once every 24 hours; the answer is cached in
`<user cache dir>/outfit/update.json` (`~/Library/Caches` on macOS, an absolute
`$XDG_CACHE_HOME` or `~/.cache` elsewhere). It runs in the background with an 800 ms
budget: if it is not done by the time the command is, it is skipped and tried next time,
and nothing is cached. A check that fails outright (offline, a rate limit, a 404) is remembered for **one hour** only as "do not ask again yet",
never as an answer, so someone who cannot reach the release does not pay a network
round trip on every command, and a real answer is never hidden for a day. (The spec
says to cache only successes; this back-off is a deliberate exception.) The banner is
also off when a git hook is running (`OUTFIT_HOOK_ACTIVE`, or the index git exports to
hooks).

## Debugging the banner

"It never shows" has a few causes, in the order `internal/cli/cmd_update.go`
(`bannerEnabled`) checks them: not a terminal (stderr redirected, even to `/dev/null`);
`--json`; `CI` or `OUTFIT_NO_UPDATE_CHECK` set; running inside a git hook
(`OUTFIT_HOOK_ACTIVE`, or the index git exports to hooks); a command that is not in
`update.BannerCommands`; a development build (no version to compare); the answer cached
less than 24 hours ago says you are current; or the last check **failed** less than an
hour ago (see above). The two files live in the cache directory, next to each other:
`update.json` (the cached answer, or the back-off marker) and `update.lock` (the update
lock). Delete the directory to reset them. The test harness sets
`OUTFIT_NO_UPDATE_CHECK=1` for every fixture (`internal/testkit/env.go`), so no
testscript run sees a banner; the banner is tested through `cli.Execute` with
`Env.IsTTY` set (`internal/cli/update_test.go`).

## Environment variables

| Variable | Effect |
|---|---|
| `OUTFIT_GITHUB_TOKEN` | A GitHub token that can read the repository. The only token outfit ever uses, and only when set (the public download needs none). |
| `OUTFIT_NO_UPDATE_CHECK` | Set to anything to switch the banner off. |
| `OUTFIT_BASE_URL` | Public download base. For tests; same meaning as in `install.sh`. |
| `OUTFIT_API_URL` | API base. For tests; same meaning as in `install.sh`. |

`tests/validate-install-contract.sh` fails CI if the variables the installer and the
updater share drift apart, or if the Go code reads one that no document mentions.

## Key rotation

The keys compiled in are a list, so a rotation is two releases: ship a release that adds
the new key, then switch the signing secret ([release.md](release.md)).
