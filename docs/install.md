# Installing outfit

outfit is one static binary: Linux (amd64, arm64) and macOS (universal, so Apple
Silicon and Intel). It needs git 2.36 or newer.

## While the repository is private

The release files are only readable with a GitHub token that can read the
repository. If you use the GitHub CLI (`gh auth login` once):

```sh
curl -fsSL -H "Authorization: Bearer $(gh auth token)" \
  https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
```

That puts your token in the command line, visible to other users of the same machine
while it runs. To keep it out, let `gh` fetch the script:

```sh
gh api -H 'Accept: application/vnd.github.raw' repos/prosperity-solutions/outfit/contents/install.sh | sh
```

The first token is for fetching the script itself; the script then looks for a token
on its own, in this order: `OUTFIT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, then
`gh auth token`. It uses the first one it finds to ask the GitHub API for the
release and to download its files. The token is never printed, never placed on a
command line, sent only to the API host (not to wherever GitHub redirects the
download), and only over https.

## Once the repository is public

No token needed:

```sh
curl -fsSL https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
```

With no token the script uses the public `releases/latest/download/…` URLs.

## What the script does

1. Picks the build for this machine (`linux_amd64`, `linux_arm64` or `darwin_all`; on
   anything else it stops and says so).
2. Downloads the archive, `checksums.txt` and `checksums.txt.minisig` into a temporary
   folder that is removed on exit.
3. Checks the archive's SHA-256 against `checksums.txt`. A mismatch stops everything.
   This catches a damaged download. It does **not** catch a tampered release, because
   `checksums.txt` comes from the same place as the archive.
4. Checks the signature of `checksums.txt` against the release key built into the
   script (the same key `outfit update` uses, kept in
   [`internal/update/keys.txt`](../internal/update/keys.txt)). A bad signature stops
   everything. This is the check that catches tampering. You do not need `minisign`
   for it: with one on your PATH the script uses it, and without one it downloads
   minisign's own static build from
   [jedisct1/minisign](https://github.com/jedisct1/minisign/releases) (Linux amd64 and
   arm64, macOS arm64 and Intel), and runs it **only if its SHA-256 equals the one
   written in the script**. That download is sent no token and is not installed
   anywhere: it lives in the temporary folder and goes with it. It is run once to prove it
   works here. If it cannot be fetched or run (offline, a proxy, a folder you cannot
   run programs from), the script stops, says which of those it was, and says what to
   do: install minisign, or run again with `OUTFIT_SKIP_SIGNATURE=1` to accept a
   SHA-256 check only (it prints a warning). If the download is there but does not
   match the pinned hash, the script does not suggest skipping anything.
   With a token, the script also requires the archive to be the build the release is
   named for, so an old, validly signed build cannot be re-uploaded under a newer
   release; the public URL path cannot check that, because GitHub's "latest" redirect
   is not signed.
5. Looks inside the archive and accepts only `outfit`, `LICENSE`, `README.md` and
   `NOTICE`, as plain files.
6. Puts `outfit` next to the existing one (the folder `command -v outfit` finds), or
   in `OUTFIT_INSTALL_DIR`, or in `~/.local/bin`. If the existing folder is not
   writable, it uses `OUTFIT_INSTALL_DIR` when you set it (and tells you to put that
   folder first on your PATH), and otherwise stops and says so. It writes a temporary file in that
   folder and renames it into place, so a failed install never leaves half a binary.
   It never uses `sudo`.
7. Prints the PATH line to add if the folder is not on your PATH, then runs
   `outfit version`.

## Environment variables

| Variable | Effect |
|---|---|
| `OUTFIT_VERSION` | Install this release (`0.3.1` or `v0.3.1`) instead of the latest. |
| `OUTFIT_INSTALL_DIR` | Where to put the binary on a first install (default `~/.local/bin`). |
| `OUTFIT_GITHUB_TOKEN` | A GitHub token with read access to the repository. |
| `OUTFIT_BASE_URL` | Public download base. For tests. |
| `OUTFIT_API_URL` | API base. For tests. |
| `OUTFIT_SKIP_SIGNATURE` | `1` installs with the SHA-256 check only, when no signature checker can be had (no `minisign` on PATH and the pinned download failed). It never skips a check that can run. Prints a warning. |
| `OUTFIT_MINISIGN_PUBKEY` | Replaces the built-in signing key(s). For tests. |
| `OUTFIT_BOOTSTRAP_URL` | Replaces the download of the signature checker. For tests. |
| `OUTFIT_BOOTSTRAP_SHA256` | Its SHA-256, which must go with `OUTFIT_BOOTSTRAP_URL`. For tests. |

`tests/validate-install-contract.sh` fails CI if this table and the script's header
ever disagree.

## Checking a download by hand

```sh
# platform: linux_amd64, linux_arm64 or darwin_all (macOS)
gh release download v0.1.0 -R prosperity-solutions/outfit -p 'checksums.txt*' -p 'outfit_*_darwin_all.tar.gz'
shasum -a 256 -c --ignore-missing checksums.txt     # Linux: sha256sum -c --ignore-missing checksums.txt
minisign -Vm checksums.txt -P RWQwzESR8TzokwCh60NclFts0vEw5Co4E72Bdf8LX03rafQUTVhhKUWU
```

## Updating

`outfit update` (see [update.md](update.md)). If outfit lives in a folder you cannot
write to, it prints the install command to run instead.

## Uninstalling

`outfit uninstall` in each repository removes what `outfit init` added, then delete
the binary (`rm "$(command -v outfit)"`).
