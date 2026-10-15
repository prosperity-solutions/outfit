# Security of the build and the workflows

What protects a release from a bad dependency, a bad pull request and a bad runner, and what
only the repository owner can switch on.

## Rules the workflows follow

| Rule | Why | Checked by |
|---|---|---|
| Every action is pinned by its full commit SHA | a tag can be moved; a SHA cannot | `zizmor` (unpinned-uses) |
| `permissions: contents: read` at the top, wider only in the job that needs it, each with a comment | a compromised step gets the least it can use | `zizmor` (excessive-permissions, undocumented-permissions) |
| `persist-credentials: false` on every checkout | the token is not left in `.git/config` for a later step (or an uploaded artifact) to find | `zizmor` (artipacked) |
| Secrets only in the `build` job, which runs in the `release` environment, only on a push to `main` | a pull request can never reach them; each use is recorded | `zizmor` (secrets-outside-env) |
| The tag is created through the API with the job's token, not `git push` | no git credential is stored in the checkout | review |
| `timeout-minutes` on every job | a hung job does not hold a runner (or a secret) for six hours | review |
| No `pull_request_target`, no untrusted text in a `run:` line (values go through `env:`) | template injection | `zizmor` (dangerous-triggers, template-injection) |
| `GOTOOLCHAIN=local`, `go mod verify`, `govulncheck` | Go never fetches a toolchain on its own; modules match the checksum database; known-vulnerable code fails CI | `lint` job |
| Tools are pinned: `go run tool@vX.Y.Z`, `golangci-lint` by version, Python packages by hash (`scripts/ci/*-requirements.txt`) | nothing floats to "latest" | review |

## A new version waits seven days

A dependency, tool or action release is **not adopted the day it ships**: most supply-chain
attacks on a package are found and pulled within days. outfit takes the newest version that is
at least **seven days old**. An exception is a fix for a known vulnerability (a Wiz or
`govulncheck` finding that the newer version resolves), and it is said so in the pull request.
Example: `zizmor` 1.31.0 shipped the day 1.30.1 was pinned, so CI uses 1.30.1.

## `workflows` job

`zizmor` (pinned by hash) audits `.github/workflows/` on every pull request, with the job's
read-only token so its online checks (known-vulnerable actions, impostor commits) run. Run it
locally with `zizmor .github/workflows` (`brew install zizmor`).

## What only the owner can switch on

outfit's automation is not allowed to change repository settings. These are worth doing in
Settings of `prosperity-solutions/outfit` (all were **disabled** when checked):

| Setting | Where | Effect |
|---|---|---|
| Secret scanning and push protection | Code security | a committed key is blocked at push |
| Dependabot security updates | Code security | pull requests for vulnerable dependencies |
| The `release` environment: deployment branches = `main`, and move the signing secrets into it | Environments | the signing secrets are readable only by a run on `main` |
| Branch ruleset on `main` (pull request, `ship`, `commits`, `all-green`) | Rules | nothing lands unreviewed by CI; set up at the end of the build |
