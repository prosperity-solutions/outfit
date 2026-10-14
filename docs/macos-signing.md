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

GoReleaser's `notarize.macos` (which uses [quill](https://github.com/anchore/quill))
signs the macOS universal binary **from Linux**, so the release needs no Mac for the
build. Two entries in `.goreleaser.yaml`, exactly one of which is enabled:

| Secrets present | Result |
|---|---|
| none | the darwin binary ships unsigned (the release log says why) |
| `MACOS_SIGN_P12` and `MACOS_SIGN_PASSWORD` | signed with Developer ID, hardened runtime, timestamp |
| plus `MACOS_NOTARY_KEY`, `MACOS_NOTARY_KEY_ID`, `MACOS_NOTARY_ISSUER_ID` | also notarized (waits up to 20 minutes) |

`MACOS_SIGN_P12` and `MACOS_NOTARY_KEY` hold the **base64 contents** of the `.p12`
and the App Store Connect `.p8`. The `.p12` must include the certificate chain, not
just the leaf: export the Developer ID certificate together with Apple's
intermediate. If signing fails with "unable to find remaining chain certificates", the
chain is what is missing.

When the signing secrets exist, a `verify-signing` job on `macos-latest` checks the
published result: `codesign --verify --strict`, a Developer ID authority, and a
`TeamIdentifier` equal to the repository variable `MACOS_TEAM_ID` when that is set. A
failed check fails the release.

The signing path is exercised on **every pull request** without real secrets: the
`signing-smoke` job builds the universal binary with a throwaway certificate chain and
checks, with `quill describe`, that both architectures carry a hardened-runtime,
timestamped signature with the right team ID (`scripts/ci/signing-smoke.sh`).

## If quill ever stops working

The fallback is signing on a macOS runner with `codesign`, in a temporary keychain,
then `notarytool submit --wait`. veld's `scripts/release/macos-codesign.sh` and
`macos-keychain.sh` are the pattern (MIT; see NOTICE). We chose quill because the
smoke test proved it handles the universal binary, and it keeps the build on one cheap
Linux runner.
