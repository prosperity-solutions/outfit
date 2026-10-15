# Security of the build and the workflows

What protects a release from a bad dependency, a bad pull request and a bad runner, and what
only the repository owner can switch on.

## Rules the workflows follow

| Rule | Why | Checked by |
|---|---|---|
| Every action is pinned by its full commit SHA | a tag can be moved; a SHA cannot | `zizmor` (unpinned-uses) |
| `permissions: contents: read` at the top, wider only in the job that needs it, each with a comment | a compromised step gets the least it can use | `zizmor` (excessive-permissions, undocumented-permissions) |
| `persist-credentials: false` on every checkout | the token is not left in `.git/config` for a later step (or an uploaded artifact) to find | `zizmor` (artipacked) |
| Secrets are referenced only by the `build` job, which runs in the `release` environment on a push to `main` | the later jobs learn "is macOS signed" from build's outputs, not from the secrets. **This does not keep a pull request away from them yet:** see "What only the owner can switch on" | `zizmor` (secrets-outside-env) |
| Every release archive carries a signed build provenance (`actions/attest-build-provenance`, made by the publish job, checked by `gh attestation verify` in all four `verify` legs) | a file that did not come from this workflow run on this commit has no valid attestation, whatever its checksum says | the `verify` job |
| The tag is created through the API with the job's token, not `git push` | no git credential is stored in the checkout | review |
| `timeout-minutes` on every job | a hung job does not hold a runner (or a secret) for six hours | review |
| No `pull_request_target`, no untrusted text in a `run:` line (values go through `env:`) | template injection | `zizmor` (dangerous-triggers, template-injection) |
| `go mod verify`, `govulncheck`; Go's toolchain comes from `go.mod` through setup-go (which also sets `GOTOOLCHAIN=local`, so Go never fetches another one) | modules match the checksum database; known-vulnerable code fails CI; the release is built with the Go version CI tested | `lint` job |
| Tools are pinned: `go run tool@vX.Y.Z`, `golangci-lint` by version, Python packages by hash (`scripts/ci/*-requirements.txt`) | nothing floats to "latest" | review |

These checks are hygiene for honest changes. They run from the pull request's own copy of the
workflow, so a hostile PR can delete the step, add a `# zizmor: ignore`, or drop the job from
`all-green`. What stops a hostile PR is review, the branch ruleset and CODEOWNERS on `.github/`,
and above all the secrets not being readable by a pull request at all (below).

## A new version waits seven days

A dependency, tool or action release is **not adopted the day it ships**: most supply-chain
attacks on a package are found and pulled within days. outfit takes the newest version that is
at least **seven days old**. An exception is a fix for a known vulnerability (a Wiz or
`govulncheck` finding that the newer version resolves), and it is said so in the pull request.
Examples: `zizmor` 1.31.0 shipped the day 1.30.1 was pinned, so CI uses 1.30.1; GoReleaser 2.18.3
(six days old) and `govulncheck` 1.9.0 (four) were not taken, 2.18.2 and 1.8.0 are.

`scripts/ci/dep-age.py` (the `Dependency age` CI job) enforces it for everything pinned: `go.mod`,
action SHAs, `go run tool@v`, the pip requirements and the minisign downloads in `install.sh`. A
version younger than seven days fails unless `.github/fresh-deps.txt` names it with the reason.
`.github/dependabot.yml` sets `cooldown: default-days: 7` so Dependabot waits too (security
updates are not delayed). The pip pins and `go run tool@v` lines are updated by hand, following the
same rule; the job will say when one is too new. Example found while adding it: `golang.org/x/crypto`
0.58.0 and `x/tools` 0.52.0 were five and six days old, so go.mod went back to 0.57.0 and 0.51.0
(0.57.0 is above the 0.56.0 floor Wiz asked for).

## `workflows` job

`zizmor` (pinned by hash) audits `.github/workflows/` on every pull request, with the job's
read-only token so its online checks (known-vulnerable actions, impostor commits) run. On a
push to `main` it runs offline: the job gates the release, and a network hiccup or a new advisory
against an action that is already pinned must not block it. Run it
locally with `zizmor .github/workflows` (`brew install zizmor`).

## What only the owner can switch on

outfit's automation is not allowed to change repository settings. These are worth doing in
Settings of `prosperity-solutions/outfit` (all were **disabled** when checked):

| Setting | Where | Effect |
|---|---|---|
| Secret scanning and push protection | Code security | a committed key is blocked at push |
| Dependabot security updates | Code security | pull requests for vulnerable dependencies |
| **First:** the `release` environment (it exists after the first release run): deployment branches = `main`, then move the seven signing secrets from the repository into it | Environments | until then they are repository secrets, which a pull request from a branch of this repository can read by editing a workflow (a fork's pull request cannot). After, only a run on `main` can |
| Branch ruleset on `main` (pull request, `ship`, `commits`, `all-green`), optionally "require review from code owners" | Rules | nothing lands unreviewed by CI; `.github/CODEOWNERS` names who must look at workflows, `install.sh`, the update keys and the release scripts. Set up at the end of the build |
