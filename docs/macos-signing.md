# macOS signing and notarization

## Do we need it?

Not for the install path. Gatekeeper only checks files that carry the
`com.apple.quarantine` attribute, which browsers and some other apps add to what
they download. **`curl` never sets it**, so a binary installed by `install.sh` (or
replaced by `outfit update`, which downloads the same way) is never checked, signed or
not.

Notarization could not be stapled to a bare executable anyway (stapling needs an app
bundle, disk image or package). A notarized bare binary is checked online the first
time it is quarantined, which for us is never.

So the release signs and notarizes **because the owner set it up**, not because
installs need it. It gives a binary that is verifiably from the team (hardened
runtime, Developer ID, timestamp), and it keeps working if someone ever downloads
the archive with a browser.

## How it works in the release

The release's `build` job runs on **macOS** and signs the universal binary with the
native `codesign`, as a GoReleaser post hook (`universal_binaries[].hooks.post` in
`.goreleaser.yaml` runs `scripts/release/macos-sign.sh`). The script puts the
certificate in a throwaway keychain (`macos-keychain.sh`, which picks the identity by
**team ID**, so a `.p12` holding two teams cannot sign as the wrong one), signs with
the hardened runtime and a secure timestamp under the fixed identifier `outfit`
(`macos-codesign.sh`, which then asserts identifier, runtime flag, timestamp, team
and that the designated requirement pins the team, not a certificate hash), and deletes
the keychain afterwards, even on failure. Ported from veld (MIT; see NOTICE).

| Secrets present | Result |
|---|---|
| none | the darwin binary ships unsigned (the release log says why) |
| `MACOS_SIGN_P12`, `MACOS_SIGN_PASSWORD` and the variable `MACOS_TEAM_ID` | signed with Developer ID, hardened runtime, timestamp |
| plus `MACOS_NOTARY_KEY`, `MACOS_NOTARY_KEY_ID`, `MACOS_NOTARY_ISSUER_ID` | also notarized: zipped, `notarytool submit --wait` (up to 20 minutes), the log dumped on failure. Not stapled. |

`MACOS_SIGN_P12` and `MACOS_NOTARY_KEY` hold the **base64 contents** of the `.p12`
and the App Store Connect `.p8`. The `.p12` must hold the Developer ID Application
certificate, its private key and Apple's Developer ID G2 intermediate; without the
intermediate the keychain script finds no valid identity and says so. It also refuses a
certificate with less than 30 days left, so an expiry fails a release a month early.
**Check the real secret once, on a Mac**, before relying on it (the first release run
that used it was signed by a different tool, which reads `.p12` files more leniently
than `security import` and fills in a missing intermediate from its own copy):

```sh
kc=$(mktemp -d)/check.keychain-db
security create-keychain -p x "$kc"
security import devid.p12 -k "$kc" -f pkcs12 -P '<password>' -T /usr/bin/codesign
security find-identity -v -p codesigning "$kc"    # must list one "Developer ID Application: … (HZHB3JQ29L)"
security delete-keychain "$kc"
```

Export from Keychain Access (File > Export Items, `.p12`, select the certificate and
its key together) or re-wrap an existing file with
`/usr/bin/openssl pkcs12 -export` (the macOS tool); both formats `security import`
reads on current macOS. The secret value is `base64 -i file.p12 | pbcopy`, the notary
key's `base64 -i AuthKey_XXXX.p8 | pbcopy`.

When the signing secrets exist, the macOS smoke legs check the built binary **before
anything is published**, and a `verify-signing` job on `macos-latest` reads the published
file back afterwards. Both run `scripts/release/check-macos-signature.sh`:
`codesign --verify --strict`, a Developer ID authority, the hardened-runtime flag, a
secure timestamp, a `TeamIdentifier` equal to the repository variable `MACOS_TEAM_ID`,
and a designated requirement that names `outfit` and pins the team (not a hash). A
failed check fails the release. Notarization is checked once, in the build hook: the
submission must come back `Accepted`, or the build fails and the log is dumped.

The signing path is exercised on **every pull request** without real secrets: the
`signing` job (one short macOS job) runs the release's own GoReleaser snapshot and hook
with a throwaway certificate (named like a Developer ID one, trusted on the runner) and
checks that both slices verify with the hardened runtime, that the checksum signature
verifies, and that the signed universal binary **runs** (`scripts/ci/signing-smoke.sh`).
Apple's timestamp server, the Apple-issued team and notarization are the parts it
cannot exercise with a throwaway certificate: the build hook's own assertions and
`check-macos-signature.sh` cover the timestamp and team on the real one. The
notarization branch is exercised with a stub `notarytool` (accepted, and rejected with
its log dumped), so its parsing is tested even though Apple is not.

The hardened runtime means macOS removes `DYLD_*` variables from what a signed process
starts. outfit runs your hooks as children, so a hook that relies on `DYLD_LIBRARY_PATH`
would not see it; set it inside the hook if you need it.

## Why not quill

GoReleaser's `notarize.macos` (quill) signs from Linux, and it was the first choice:
its signature verified on the build and in the smoke checks. But on a real Developer ID
certificate it writes the designated requirement
`… and certificate root[field.1.2.840.113635.100.6.2.6] …` (the Developer ID CA marker
on the **root**, where Apple's own signatures put it on the intermediate), which no
chain satisfies. `codesign --verify` says "does not satisfy its designated Requirement"
and macOS kills the binary on launch (`Killed: 9`, exit 137). The release's smoke legs
(macos-latest and macos-15-intel, which install and run the signed artifact) caught it
before anything was published. The PR smoke job runs the signed binary too, but with a
throwaway certificate that has no Apple chain, so it cannot reproduce a failure that
depends on one: it catches everything else, and **the release smoke legs are the
guard for the Apple-chain part**.
