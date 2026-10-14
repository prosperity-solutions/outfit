# How a release happens

Every push to `main` runs [`release.yml`](../.github/workflows/release.yml). Nothing
is ever committed back to `main`: the version comes from the tag alone.

```
version → build → smoke (4 runners) ┐
        └→ wait for CI (all-green)  ┴→ publish → verify (4 runners) + verify-signing
```

1. **version.** `svu next --v0` reads the Conventional Commits since the last tag and
   names the next version. With no tag yet it is `v0.1.0`. `--v0` keeps a breaking
   change from jumping to 1.0. If nothing is releasable (only `docs:`, `chore:`…
   commits), the run stops here, green.
   If the commit is already tagged but its release was never published (a failed
   publish), the run resumes that version instead of stopping. `previous` is the latest
   *published* release.
2. **build** (ubuntu). Creates the tag **locally**, then `goreleaser release --clean
   --skip=publish`: linux amd64/arm64 and a macOS universal binary
   (signed/notarized when the secrets exist, see [macos-signing.md](macos-signing.md)),
   `checksums.txt`, and `checksums.txt.minisig`. Then it checks the signature against
   `internal/update/keys.txt` with the same Go code `outfit update` uses, so a signing
   secret that does not match the committed key fails *here*, before anything ships.
   It also checks the signature against the keys of the *previous release* (read from
   its tag), because installed clients only trust what they were built with.
3. **smoke.** On `ubuntu-24.04`, `ubuntu-24.04-arm`, `macos-latest` and
   `macos-15-intel`: serve the build from disk, run `install.sh` against it, run
   `outfit version` and `scripts/demo.sh --quick`. On the macOS legs it also checks the
   Developer ID signature (`scripts/release/check-macos-signature.sh`), so a bad
   signature stops the release *before* it is public. Every native architecture
   executes the real artifact before anyone else can. The fake server serves
   `releases/latest/download/<file>` and `releases/download/v<ver>/<file>` (no API, no
   redirects), the layout `install.sh` and `outfit update` read without a token;
   `OUTFIT_BASE_URL` points at it.
   **Wait for CI** runs alongside: it polls the `all-green` check that `ci.yml` reports
   for the same commit (the full test matrix, every git version and both macOS legs
   run there on a push to `main`) and fails if it is not green, so a red `main` never
   releases.
4. **publish.** Needs smoke and CI. Re-checks the files (`sha256sum -c`, and the
   signature against `keys.txt`) since they sat in artifact storage while four
   runners ran third-party packages in the same run (any job of a run can replace its
   artifacts), then pushes the tag and runs [`scripts/release/publish.sh`](../scripts/release/publish.sh):
   a *draft* release, upload with retries, check every asset by name, size and
   state, flip to published, read it back. Any failure leaves a draft, which users
   cannot see; re-running the job tops it up.
5. **verify.** On the same four runners, the real install exactly as a teammate runs
   it (the script downloaded from raw.githubusercontent.com with the workflow token, its
   SHA-256 logged, then run; the `curl | sh` pipe form is covered by `tests/install-test.sh`),
   retried every minute for up to ten minutes because raw.githubusercontent.com caches
   for about five. It asserts `outfit version` equals the tag.

Two quick merges cannot collide: the workflow runs under
`concurrency: {group: release, cancel-in-progress: false}` with a full checkout.

## Secrets and variables

Only `release.yml` references them, and only it runs with them (push to `main`).

| Name | Kind | Used for |
|---|---|---|
| `MINISIGN_SECRET_KEY`, `MINISIGN_PASSWORD` | secrets | signing `checksums.txt` |
| `MINISIGN_PUBLIC_KEY` | variable | must be one of the keys in `internal/update/keys.txt` (the `.pub` file, or just its second line) |
| `ALLOW_UNTRUSTED_PREVIOUS_KEYS` | variable | break glass for a leaked key only (below); normally absent |
| `MACOS_SIGN_P12`, `MACOS_SIGN_PASSWORD` | secrets | Developer ID signing |
| `MACOS_NOTARY_KEY`, `MACOS_NOTARY_KEY_ID`, `MACOS_NOTARY_ISSUER_ID` | secrets | notarization |
| `MACOS_TEAM_ID` | variable | the team the signature must show |

The minisign key is written to a `0600` temporary file for the one signing step and
deleted afterwards, even when the step fails. Secret values are never printed.

## Creating a key pair (step 0)

```sh
go install aead.dev/minisign/cmd/minisign@v0.3.0     # or: brew install minisign
minisign -G -p outfit.pub -s outfit.key              # asks for a password
```

- `MINISIGN_SECRET_KEY`: the **whole contents** of `outfit.key` (both lines).
- `MINISIGN_PASSWORD`: its password.
- `MINISIGN_PUBLIC_KEY` (variable): `outfit.pub`, or just its second line.
- `internal/update/keys.txt` and the `PUBKEYS` line of `install.sh`: the same
  second line (the contract test checks they agree).

Keep the secret key and its password in a password manager, not on disk.

## Rotating the update key

Clients trust the keys compiled into them, so a rotation is **two releases**:

1. Add the new public key as an extra line in `internal/update/keys.txt` **and** in the
   `PUBKEYS` line of `install.sh` (space-separated). Merge it with a **`fix:` or
   `feat:`** commit: a `chore:`, `ci:` or `docs:` commit makes no release, so no client
   would ever learn the key. This release is still signed with the old key; every
   client that updates to it learns the new key.
2. Once clients have updated, switch `MINISIGN_SECRET_KEY`, `MINISIGN_PASSWORD` and
   `MINISIGN_PUBLIC_KEY` to the new pair. The next release is signed with it.
3. Later, drop the old key from both files in another release, and change `releaseKeyID`
   in `internal/update/verify_test.go` to the new key's ID (the test only requires the
   release key to be among the embedded ones, so adding a key in step 1 needs no edit).

Doing step 2 first would ship a release no installed client can verify. The release job
refuses that: it verifies the fresh signature against the keys of the previous release
(`git show <previous tag>:internal/update/keys.txt`) and fails if they do not accept it.

### If the signing key leaks

Do not wait for the order above; it would keep trusting the leaked key for another
release. The previous-release key check would also refuse the replacement, so it has a
break-glass switch. In one go:

1. Generate a new pair (step 0) and set `MINISIGN_SECRET_KEY`, `MINISIGN_PASSWORD` and
   `MINISIGN_PUBLIC_KEY` to it. Keep the old values until the release below is out.
2. Set the repository variable `ALLOW_UNTRUSTED_PREVIOUS_KEYS` to `true`.
3. In **one commit**, replace the leaked key with the new one in `internal/update/keys.txt`,
   the `PUBKEYS` line of `install.sh` (the contract test fails if only one changes) and
   `releaseKeyID` in `internal/update/verify_test.go`, titled `fix!: …`. Merge it.
4. When the release is published, delete the variable. New installs trust only the new
   key as soon as `install.sh` on `main` is served (raw.githubusercontent.com caches it for
   about five minutes). Clients that only know the leaked key cannot verify the new release
   and must reinstall with `install.sh`; say so in the release notes.

## When a release run fails

- Before **publish**: nothing is public. Fix forward in a new PR; the next merge
  retries with the same version (no tag was pushed).
- During **publish**: the tag may be pushed and a draft may exist. Re-run the failed
  job, or the whole run: the version job sees the tag on this commit without a published
  release and resumes it. The draft's assets are replaced, not trusted.
- After **publish**, in **verify**: the release is public but an install check failed.
  The job log says on which runner and why; fix forward.
