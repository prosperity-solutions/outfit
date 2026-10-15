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
| Every release archive carries a signed build provenance (`actions/attest-build-provenance`, made by a separate `attest` job that alone holds `id-token: write`). `publish` verifies it before anything is public, and all four `verify` legs verify their platform's archive again, each time with an exact certificate identity (`release.yml@refs/heads/main`) and the commit | the file was built by `release.yml` on `main` for this commit; a copy of the workflow on another branch cannot produce one that passes. (Releases before the provenance was added, up to v0.7.0, have none.) | `publish` and `verify` jobs |
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

`scripts/ci/dep-age.py` (the `Dependency age` CI job) enforces it for everything pinned: `go.mod`
(requires, `replace` targets, the `toolchain` line), action SHAs, `go run tool@v`, the pip
requirements, golangci-lint and the minisign downloads in `install.sh`. A version younger than
seven days fails unless `.github/fresh-deps.txt` names it with the reason. It also fails on a
registry that answers "no such version" or "not allowed", and on any line that looks like a pin
but that it cannot read (`@latest`, a branch ref, `docker://`), so a pin written in an unusual way
cannot slip past; an unreachable registry (a timeout) fails on a pull request and only warns on a
push to `main`, because that job gates the release.

What "published" means: for Go modules it is the proxy's time, which is the tag commit's
committer date, so a maintainer *could* back-date a release; the rule stops "adopt it today", not
that. For an action it is the GitHub release's publish date (the registry's own clock), and only
for an action with no release the commit date. zizmor's online `ref-version-mismatch` audit is what
checks that a pinned SHA is the commit of the version in its comment.
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

outfit's automation is not allowed to change repository settings. The repository is **public**, which
makes the settings below matter more: anyone can open a pull request, and a workflow run is the one
place a secret could leak. **One command does the settings that need no secret**, as a repository admin:

```sh
scripts/dev/harden-public-repo.sh
```

| Setting (what the script sets) | Effect |
|---|---|
| The workflow token starts read-only (`default_workflow_permissions: read`) | each workflow asks for the permissions it needs; none inherits write by default (they all declare theirs, `zizmor` checks it) |
| SHA pinning required for actions | a workflow that uses an action by tag or branch cannot run; every action here is pinned by SHA, and the dependency-age gate keeps it so |
| The `release` environment may deploy from `main` only | a run on any other branch cannot use what the environment holds |
| Secret scanning and push protection | a committed key is blocked at push |
| Dependabot security updates | pull requests for vulnerable dependencies |
| Private vulnerability reporting | `SECURITY.md` points reporters to it |

These are **already as strict as they can be** (checked): fork pull requests need approval before a workflow
runs for an outside contributor (`all_external_contributors`), and a fork's pull request never receives a
secret or a write token. No workflow uses `pull_request_target` (`zizmor` flags one that does).

Two more are yours and cannot be scripted by the agent:

| What | Why |
|---|---|
| **Move the seven signing secrets into the `release` environment** (Settings > Environments > `release` > Secrets), then delete the repository-level copies | they are repository secrets today, which a pull request from a *branch of this repository* (a collaborator, not a fork) can read by editing a workflow. In the environment, with the branch rule above, only a run on `main` can. outfit never reads, writes or moves a secret |
| "Require review from code owners" on the `main` ruleset (the ruleset itself exists, see below) | `.github/CODEOWNERS` names who must look at workflows, `install.sh`, the update keys and the release scripts; with the setting on, a change to them waits for that person |

## The ruleset on `main`

One repository ruleset (named `main`, id 25160433, created at the end of the first build) applies to the
default branch:

- the branch cannot be deleted, and its history cannot be rewritten (no force-push);
- a change comes by **pull request**, with no approval count required (CODEOWNERS review is the owner's
  choice, see above);
- the status checks **`all-green`**, **`Ship Workflow`** and **`Conventional Commits`** must pass.
  `all-green` already needs the other two, so they are named twice on purpose: a run in which one of them
  was skipped must not satisfy the rule;
- repository admins can bypass it (`gh pr merge --admin`): the ship workflow allows that only after reading
  the `ship` job's own conclusion (`docs/ship.md`, Step 6.3).

Releases push **tags** only, so the ruleset does not touch them. Change it under Settings > Rules; to
recreate it, `gh api -X POST repos/<owner>/<repo>/rulesets` with the JSON of the existing one
(`gh api repos/<owner>/<repo>/rulesets/<id>`).

## Known limits

- **Nothing signed says which release is the latest.** The signature covers `checksums.txt` (and so the
  archive's hash and its version in the file name), not a "this is the newest release" statement. Someone
  with write access to the releases could put an older signed build under a newer release or mark an
  older release as latest: a fresh install gets that old, genuinely signed build, and `outfit update`
  says "up to date". The token path's tag comparison narrows this a little; a seal would need the
  version in the signed data. Write access to the repository is what an attacker would need, which is
  why the ruleset, the `release` environment and the stamp rules above matter.
- **The stamp is a workflow nudge, not a gate against strangers.** Its key is in the public source, and a
  pull request from a fork runs its own copy of `ci.yml`: a green `Ship Workflow` or `all-green` on a
  fork's pull request proves nothing. Merging needs write access, so the model holds through review.
- The git history carries the maintainer's commit address.
