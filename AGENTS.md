# AGENTS.md — outfit

`outfit` is a small, very fast Go CLI that lets each developer, and each coding
agent, run a git monorepo under a **profile**: which hook checks run, and a few
swapped files. The full spec is [docs/HANDOVER.md](docs/HANDOVER.md); how git
actually behaves, as pinned by tests, is [docs/git-behavior.md](docs/git-behavior.md).

## Before you change anything

Is the task a **question** or a **change**? A question (where is X, how does Y
work) needs no process: read and answer. A change — anything that leaves a diff —
goes through the ship workflow: read [docs/ship.md](docs/ship.md) in full, then
follow it from the top. Claude Code users invoke the `ship` skill instead.

CI's `ship` job goes red for a pull request whose head commit has no valid
`Ship-Stamp:` trailer, and stays red. Do not try to satisfy it without running
the workflow: the stamp is derived, not chosen.

## Agent Skills

| Skill | Where | Use it for |
|---|---|---|
| `ship` | `.agents/skills/ship/SKILL.md` (also `.claude/skills/ship`, `docs/ship.md`) | every change, from empty diff to merged PR |

The review methodology `ship` runs is [docs/agentic-review.md](docs/agentic-review.md).
The agent roster (`code-reviewer`, `explorer`, `researcher`, `builder`) is in
`.claude/agents/`; its three reviewer checkpoints are in [CLAUDE.md](CLAUDE.md).

## PR workflow

1. Branch from `origin/main`. Never commit to `main` (the one bootstrap commit
   was the only exception).
2. Conventional Commit subjects: `feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert(scope)?!?: …`.
   A breaking change gets `!`. The squash commit's subject is what `svu` reads to
   cut the next version, so the PR title must be a valid subject too.
3. Stamp the head commit (`ship` Step 5) and push.
4. Open a **draft** PR, finish the local review, then `gh pr ready`.
5. Wait for the ready run's jobs to be green, then
   `gh pr merge --squash --delete-branch`.

Local pre-pass, before every push (lefthook runs it on `pre-push`):

```sh
gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...
```

## Pushing

- `origin` is `github.com/prosperity-solutions/outfit`, a **private** repo. Pushes
  go over HTTPS with your `gh` login (`gh auth login`, scopes `repo` and
  `workflow`). If your `origin` is an SSH URL and you have no key loaded, run once
  per clone:

  ```sh
  git config --local url."https://github.com/".insteadOf "git@github.com:"
  git config --local credential.https://github.com.helper ''
  git config --local --add credential.https://github.com.helper '!gh auth git-credential'
  ```

  Nothing global changes, and it never touches the remote's visibility.
- **Direct pushes to `main`:** the one bootstrap commit was the only one. Every
  other change is a PR, merged with `gh pr merge --squash --delete-branch`. Once
  the `main` ruleset exists (PR 11 of `docs/HANDOVER.md`), a direct push needs an
  admin bypass: don't.
- Never force-push `main`. Releases push **tags** only; nothing is committed to
  `main` by CI.

## CI cost and the draft convention

- **CI does not run on draft PRs.** Every job carries
  `if: github.event_name != 'pull_request' || github.event.pull_request.draft == false`
  (or an accepted variant), and `ready_for_review` is in the trigger types.
  `python3 tests/validate-workflow-gates.py` enforces this; run it whenever you
  touch `.github/workflows/`.
- **A skipped job looks green.** A draft's jobs all skip, `gh pr checks` reports
  them as `skipping`, exits 0 and prints "All checks were successful". Read the
  ready run's jobs (`ship` Step 6.3), never the rollup.
- macOS minutes count about 10× against a private repo's quota. On PRs macOS runs
  only `unit` and one `e2e` leg; the full macOS matrix runs on push to `main` and
  in `release.yml`.
- `all-green` is the single check the ruleset requires. Matrix jobs report
  expanded names (`e2e (ubuntu-24.04, 2.36)`), so a check called `e2e` never
  reports.

## Documentation checklist

A change that adds or alters a command, flag, config key, output shape or
user-visible behaviour updates **all** of:

- [ ] `README.md`
- [ ] the command's `--help` text
- [ ] the relevant file in `docs/`
- [ ] the header of `install.sh` (when an `OUTFIT_*` variable changes)
- [ ] `docs/git-behavior.md` (when you learned something about how git behaves)

A purely internal change says so in the PR body.

## Conventions

- **Go, standard library first.** `flag` with a subcommand switch; no cobra. Extra
  dependencies are limited to `rogpeppe/go-internal` (testscript),
  `pgregory.net/rapid`, `google/go-cmp` and `aead.dev/minisign`; anything else
  needs a justification line in the PR body.
- **Git only through `internal/gitx`**, which shells out to the `git` binary,
  counts calls, and sets `GIT_OPTIONAL_LOCKS=0` on reads. Never go-git or libgit2.
  Floor is git 2.36; don't use `git config get` (2.46+).
- **Hot paths are budgeted:** a no-op `sync` spawns ≤ 2 git processes and makes 0
  writes; `get` spawns ≤ 1. Both are CI gates.
- **Every command** is idempotent, non-interactive, supports `--json`, and uses
  the stable exit codes (0 ok, 1 error, 2 usage/unknown, 3 needs a human, 4 out of
  sync/stale). `--json` errors carry `"fix"` and `"retryable"`. Error text names
  the exact fix.
- **Never overwrite user bytes.** Quarantine, don't delete.
- **Never `include.path` a committed file**; parse committed config yourself and
  accept only `outfit.*` keys.
- **`plan` is pure.** `Plan(Desired, Actual) -> []Action`, no I/O.
- Tests are hermetic (fixed `HOME`, `GIT_CONFIG_*`, dates, `TZ`, `LC_ALL`); see
  `docs/HANDOVER.md` §7.2. When git disagrees with the docs, write the failing
  test, adapt the code, record it in `docs/git-behavior.md`.
- Never disable a test to get to green.
- macOS and Linux only. MIT licence. Private repo: install and update authenticate
  with a GitHub token.
