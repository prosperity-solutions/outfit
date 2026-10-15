# Handover: build `outfit`, fully autonomous

> **Update, 2026-10-15:** the owner made the repository **public** after the build. This document is the
> spec the build followed and is otherwise unedited, so where it says the repository is private (the
> "stays private" rule, the token lookup in §5 and §8.5, the install command "while the repo is
> private", the macOS minutes cost) read it as history. Today: no token is needed to install or update
> (`OUTFIT_GITHUB_TOKEN` is used only when set), macOS runners are free, and the settings that matter
> for a public repository are in [security.md](security.md). §12 also no longer holds: a profile can
> replace a whole directory ([profiles.md](profiles.md)).

## For you, the human: 10 minutes before you paste

1. **The repo `prosperity-solutions/outfit` already exists and is PRIVATE. It stays private.** The agent must never change its visibility. Everything below works against a private repo: install and update authenticate with a GitHub token (§5, §8.5).
2. **Run [`setup-outfit.sh`](setup-outfit.sh) on your Mac, from the folder with your Apple signing files.** It does all of the following, idempotently, and prints nothing secret:
   - logs `gh` in with the `workflow` scope, which the first push needs because it contains `.github/workflows/`
   - runs `gh auth setup-git`, so plain `git push` uses the same credentials
   - checks that the repo is **PRIVATE** and never changes it
   - sets every secret and variable in the table under step 4, and checks that the `.p12` password opens the `.p12`
   - generates the minisign key, proves it signs and verifies, then uploads it

   Also needed on the machine that runs the session: git ≥ 2.36 and Go. The agent installs everything else.
3. **The owner is `prosperity-solutions`**, the same org as veld. It's already filled in everywhere.
4. **The release secrets `setup-outfit.sh` sets.** This is for reference; the script does all of it. The agent never generates, sees or writes a signing secret.

   | Name | Kind | What to put in it |
   |---|---|---|
   | `MACOS_SIGN_P12` | secret | Your **Developer ID Application** certificate plus its private key, exported from Keychain Access as `.p12`, **base64-encoded**: `base64 -i devid.p12 \| tr -d '\n'` |
   | `MACOS_SIGN_PASSWORD` | secret | The password you set when exporting that `.p12` |
   | `MACOS_NOTARY_KEY` | secret | An App Store Connect API key (*Users and Access → Integrations → Team Keys*, role *Developer*): the downloaded `AuthKey_XXXX.p8`, **base64-encoded** |
   | `MACOS_NOTARY_KEY_ID` | secret | That key's ID, the `XXXX` in the filename |
   | `MACOS_NOTARY_ISSUER_ID` | secret | The Issuer ID UUID shown on the same page |
   | `MINISIGN_SECRET_KEY` | secret | The update-signing secret key: the contents of `outfit.key` (see below) |
   | `MINISIGN_PASSWORD` | secret | The password you typed when generating it |
   | `MINISIGN_PUBLIC_KEY` | **variable** | The contents of `outfit.pub`. The agent reads it and commits it into the binary |
   | `MACOS_TEAM_ID` | **variable** | Your Apple Team ID. The release's signing check asserts that `codesign -dv` reports this `TeamIdentifier` |

   Keep the signing folder, including the `outfit.key` the script creates, in your password manager, never inside a git repo.

   The names follow GoReleaser's own `notarize.macos` (quill) convention. The `MACOS_*` secrets turn on signing **and** notarization; if any of them is missing, that step is skipped and the release ships unsigned. The minisign secrets are **required**: without them no release is cut.

   **Cost note:** in a private repo, macOS runner minutes count roughly 10× against your Actions quota. §8.1 limits macOS to what's necessary on PRs.
5. **Everything lives in the repo directory.** Unzip `outfit-repo-kit.zip` into your checkout (`unzip -o outfit-repo-kit.zip -d /path/to/outfit`). It adds three things:
   - `HANDOVER.md`, this file
   - `.claude/agents/` with the four agents below. Project agents are found automatically, and the bootstrap commits them
   - `.git/claude-session.json`: model, effort, the Haiku default for subagents, and the auto-mode rules from step 6. It sits inside `.git/`, so it is never committed, and it is passed with `--settings`. Auto mode ignores `autoMode` in the repo's own `.claude/settings*.json`, but it does read it from `--settings`

   The four agents, for reference:

   **`.claude/agents/code-reviewer.md`**
   ```markdown
   ---
   name: code-reviewer
   description: Opus on call. Read-only review of a plan or a diff at exactly three moments: before a multi-file plan locks, when the same test or compiler error fails twice, and before declaring done or opening a PR. Hand it paths, the diff, the failing output and the task text — never your conclusions.
   tools: Read, Grep, Glob, Bash
   model: opus
   effort: high
   ---
   You are the reviewer on call. You never edit. Read the actual code, not the summary you were given.
   - Plan review: missing invariants, data-loss paths, contracts (CLI output, config format, state.json), irreversible choices.
   - Repeated failure: is this the root cause or a rabbit hole? Name the wrong assumption.
   - Final diff: regressions, broken pre-flight rules (gofmt, vet, lint, tests), behaviour the task did not ask for, missing tests.
   Use Bash only for read-only commands (git diff, git log, go test, go vet).
   Return: a verdict line, then a `path:line | severity | finding | evidence` table. At most 60 lines. No file dumps.
   ```

   **`.claude/agents/explorer.md`**
   ```markdown
   ---
   name: explorer
   description: Haiku swarm for fast codebase search. Where is X, what calls Y, which files touch Z. Use instead of reading many files in the main session.
   tools: Read, Grep, Glob
   model: haiku
   ---
   Find and report. Return a `path:line | what` table plus a one-line answer. Never paste whole files.
   ```

   **`.claude/agents/researcher.md`**
   ```markdown
   ---
   name: researcher
   description: Haiku swarm for external documentation and spec lookup (Go stdlib, git docs, GitHub Actions, GoReleaser, minisign, svu).
   tools: Read, WebFetch, WebSearch
   model: haiku
   ---
   Answer the exact question from primary sources. Return the answer, the exact flag or field names, the source URL and the version it applies to. Say "not found" rather than guessing.
   ```

   **`.claude/agents/builder.md`**
   ```markdown
   ---
   name: builder
   description: Sonnet builder for bounded pieces with a clear contract — a function, a test file, a mechanical sweep — that edits code or runs test suites. Works in its own worktree.
   model: sonnet
   effort: medium
   isolation: worktree
   ---
   Build exactly the contract you are given, with tests. Run gofmt, go vet and go test for the touched packages. The worktree branches from the default branch: check your base with `git log -1` first. Return: a summary of at most 15 lines, the commands you ran with pass/fail, and the commit SHA or patch path. No file dumps.
   ```

   **Don't set `CLAUDE_CODE_SUBAGENT_MODEL_FORCE=1`.** It forces the subagent model onto *every* subagent, which would quietly turn the Opus reviewer into Haiku. Plain `CLAUDE_CODE_SUBAGENT_MODEL=haiku` already makes Haiku the default for any agent without its own `model` line.
6. **What `.git/claude-session.json` allows.** By default auto mode blocks merging a PR no human approved, committing a workflow that passes secrets to new steps, and `curl | sh`. Three blocks in a row pause the session until someone answers. The file pre-approves exactly this run's actions:
   - merge-on-green for Claude's own PRs
   - the single bootstrap push to `main`
   - `release.yml` handing the owner-set secrets to its own signing steps
   - the ruleset at the end
   - testing `install.sh`
   - porting veld's ship workflow
   
   Nothing else is widened. `$defaults` keeps every built-in rule.
7. **Start Claude Code from inside the checkout.** Auto mode trusts only the git remotes that exist when the session starts.
   ```sh
   cd /path/to/outfit
   claude --settings .git/claude-session.json --permission-mode auto
   ```
   Then type: *"Read HANDOVER.md from '# Mission' to the end and execute it. The human setup is done. Move HANDOVER.md to docs/HANDOVER.md in the bootstrap commit and treat it as the spec."*

The pinned issue **"🌅 Run report"** is created early and updated after every merge, so it's current whenever you look.

---

# Mission

You are building **`outfit`** from an empty directory to a released, installable, self-updating, heavily tested CLI. That includes:
- the full code
- the full CI and release pipeline
- the install script
- a self-update system
- an adapted copy of veld's `ship` workflow, which every change after the first commit goes through

Repo: `github.com/prosperity-solutions/outfit`. Work as fast as quality allows. Nothing here is gated on time of day.

## 0. Autonomy contract: read this twice

**You are working alone.** The maintainer handed this over and will test the result when you're done. So:

- **Never ask questions and never wait for approval.** Do not call `AskUserQuestion`; nobody will answer it, and the run stalls. Every decision this document doesn't make is yours. Record it under **"Decisions made unattended"** in the PR body, then move on.
- **The maintainer explicitly authorizes these exact actions on `github.com/prosperity-solutions/outfit`, a PRIVATE repository, and on nothing else:**
  - Push the single bootstrap commit directly to `main`.
  - Open PRs, and merge each PR you opened with `gh pr merge --squash --delete-branch` once its CI run is green, with no human approval (merge-on-green).
  - Create the labels `no-ship` and `needs-maintainer`.
  - Read the repo variable `MINISIGN_PUBLIC_KEY` with `gh variable get` and commit it to `internal/update/keys.txt`.
  - Commit `.github/workflows/release.yml`, whose release job passes the owner-set `MINISIGN_*` and `MACOS_*` secrets to its own signing and notarization steps.
  - Push release tags and publish GitHub Releases.
  - Create one ruleset on `main` at the end (§10, PR 11).
  - Port MIT-licensed ship-workflow files from `github.com/prosperity-solutions/veld` with attribution in `NOTICE`.
- **Never:**
  - force-push or rewrite `main`'s history
  - delete the repo or touch any other repo
  - print secret values
  - commit secrets or private keys
  - disable a test to get to green
  - edit `~/.claude/settings.json`, `.git/claude-session.json` or any other auto-mode configuration
  - **change the repo's visibility.** It stays private
  - create, read or overwrite any secret. The owner set them all up front
- **When auto mode blocks an action:**
  - Don't retry it, rephrase it, or route around it. Three blocks in a row pause the whole session until a human answers.
  - Record the action and the block reason in `notes/blocked.md` and in the `needs-maintainer` issue.
  - Switch to different work immediately.
  - If it blocks a merge, leave the PR green and ready, then continue with the next independent PR.
- **If something genuinely blocks a PR:**
  - What counts: a product decision this doc can't answer, an irreversible action, or credentials you can't obtain.
  - Leave the PR open and ready.
  - Write the question, with your recommendation, into the PR body, the `needs-maintainer` issue and `notes/blocked.md`.
  - Continue with the next PR that doesn't depend on it. One stop never ends the run.
- **Your memory across context compaction:** `notes/progress.md` (gitignored). Update it after every merged PR: what's done, what's next, open threads. Re-read it whenever you lose track. Use the task list too.
- **Hierarchy (§9.5). Opus reviews, Sonnet builds, Haiku swarms.**
  - **You** (Sonnet, high effort) hold decisions, write the core diffs and run the suites. Keep your context small.
  - **`explorer` and `researcher`** (Haiku) do every lookup that takes more than two greps, and every docs question. Fan them out in parallel.
  - **`builder`** (Sonnet, own worktree) takes bounded pieces and test files. Give it the contract and review what comes back.
  - **`code-reviewer`** (Opus) is called at exactly three moments: before a multi-file plan locks, when the same test or compiler error fails twice, and before you declare a PR done. Hand it code, diffs and failing output, never your conclusions.
  - The ship skill's review loop (§9) still runs on every PR, with its own model routing.
- **Go fast by running two tracks in parallel** once PR 1 is merged (§10):
  - Track **A** is the product: PRs 2 → 3 → 6 → 7 → 8 → 9.
  - Track **B** is delivery: PRs 4 → 5.
  - Track B pieces go to `builder` in its own worktree. You stay the engineer of record for every PR, and each one still goes through ship.
- **The report is rolling.** Create the 🌅 issue (§11) right after the bootstrap commit and update it after every merge.
- **Slip order**, only if something proves intractable after the reviewer has looked at it twice:
  1. depth of the §7.5 property tests
  2. hyperfine soft gate
  3. `author`
  4. `promote`
  5. macOS scenario 15
  6. Apple signing

  **Never slip:** scenarios 1–5, the pre-commit guard, install.sh, signature verification, the default variant (§3.0). Every slipped item goes in the report under **"Slipped"**, with an issue.
- **The run ends** when §10 is merged and released, the report is final, and the release has been installed from the real raw.githubusercontent URL, with a token, on all four runner types. Stop then. Don't invent extra features.

## 1. What outfit is

A small, very fast CLI that lets each developer, and each coding agent, run a git monorepo under a **profile**. A profile changes two things:
- **behavior**: which hook checks run.
- **a few swapped files**: e.g. `AGENTS.md` and `CLAUDE.md`.

None of it affects teammates, and `git status` stays truthful.

It has three users:
- **The experimenter** tries their own agent docs and hook setup in the team monorepo, then commits it as an opt-in profile that colleagues can switch to.
- **The escapee** wants a permanent personal setup, including a `yolo` profile that skips most local checks.
- **The small-Mac teammate** wants to turn off one heavy hook (a Biome run using ~5 GB RAM) on their machine only.

Ground truths:
- Local hooks are a **convenience**, not enforcement. Anyone can `--no-verify`, and CI is the real gate, so `yolo` is legitimate.
- The only exception is keys the team marks `locked` (e.g. a pre-push secret scan). A leaked secret can't be un-pushed.
- There is no governance, analytics, daemon, MCP server or profile inheritance.

Everything below marked *verified* was reproduced against real git 2.43 on Linux. macOS was **not** tested. Treat every git claim as a hypothesis your tests must pin. When git disagrees with this document:
1. Write the failing test that captures the real behavior.
2. Adapt the code.
3. Record it in `docs/git-behavior.md`.

## 2. Non-negotiables

- **Language and libraries:**
  - Go, latest stable, pinned via the `toolchain` line in `go.mod`.
  - Standard library first. No cobra: use `flag` with a subcommand switch.
  - Allowed extra dependencies: `rogpeppe/go-internal` (testscript), `pgregory.net/rapid`, `google/go-cmp`, and a minisign verifier (`aead.dev/minisign`). Anything else needs a line of justification in the PR body.
- **Multi-arch is required.** Builds are `CGO_ENABLED=0` static binaries:
  - Linux: amd64 and arm64.
  - macOS: a **universal** binary covering arm64 and amd64.
  - Every artifact is **executed on its native architecture in CI** before it's published (§8.2). Building it is not enough.
- **Git:** shell out to the `git` binary; never use go-git, libgit2 or gitoxide.
  - Hard floor is git **≥ 2.36**. Refuse to run below it, with a clear message that names the fix (Ubuntu 22.04 ships 2.34).
  - Must work with Apple git (2.39).
  - Don't use `git config get` (2.46+).
  - The `git --version` floor check runs only in `init`, `use`, `status` and `version`. Hot paths (`sync`, `get`, `hook`) trust the git version recorded in `state.json` at init, so the call budgets below hold.
- **Speed:**
  - A no-op `outfit sync` spawns **≤ 2 git processes** and makes **0 writes**. `get` spawns ≤ 1.
  - Both are hard CI gates via call counting.
  - Wall-clock target: < 10 ms on Linux.
- **Every command:** idempotent, non-interactive, supports `--json`, and uses stable exit codes.
- **Platforms:** macOS and Linux only.
- **Licence:** MIT.

## 3. The model

### 3.0 The default variant: what people without outfit get

**Non-users and not-yet-installed teammates get exactly the committed repo, with nothing changed.** That's the rule everything else must respect.

- **Files:** the committed base files are the default. There is no team-default profile; `outfit.profile` unset means `base`, which means no swaps. If a team default swapped files, outfit users and non-users would silently disagree about what `AGENTS.md` says.
- **Toggles:** the committed `.outfit/config` values are everyone's defaults, users and non-users alike. Hooks that opt in read them with a fallback that needs no outfit install:
  ```sh
  v=$(outfit get biome 2>/dev/null || git config -f .outfit/config --get outfit.biome)
  [ "$v" = off ] && exit 0
  ```
  `git config -f` reads only that one file. It doesn't apply its settings and doesn't follow includes, so this is safe even though the file is committed.
- **Discoverability:** `outfit init --team` (run once by whoever introduces outfit) adds:
  - one line to base `AGENTS.md`: *"Optional: this repo supports outfit profiles; see docs/outfit.md to install."* `docs/outfit.md` carries the install command for the repo's current visibility (§8.5).
  - the snippet above to `docs/outfit.md`

  Nothing else changes for non-users.
- **Test:** an e2e scenario clones a repo that has `.outfit/` committed, on a machine **without** outfit, and asserts that every hook runs, the files are the base files, `git status` is clean and the fallback snippet returns the committed default.

### 3.1 Layout

```
repo/                                   (the user's monorepo)
  .outfit/
    config                              # committed team defaults
    profiles/<name>/
      profile.config                    # this profile's toggles
      AGENTS.md, apps/web/AGENTS.md…    # files this profile swaps in (same relative paths; FILES only)
<git-common-dir>/outfit/
  hooks/                                # generated dispatcher shims, shared by all worktrees
  profiles/<name>/                      # LOCAL profiles: never versioned, same shape as committed ones
<per-worktree git dir>/outfit/
  profiles/<name>/                      # WORKTREE profiles: deleted automatically by `git worktree remove`
  state.json                            # schema-versioned: recorded base blobs + variant hashes
  sync.lock
  quarantine/<ts>/
```

Both committed files use git-config syntax, so the `[outfit]` section is required:

```ini
# .outfit/config
[outfit]
    locked  = secrets-scan, hook.pre-push
    secrets-scan = on

# .outfit/profiles/yolo/profile.config
[outfit]
    biome = off
    tests = off
    hook.pre-commit = off          # dispatcher skips the chained team pre-commit entirely
```

- `profile` and `locked` are reserved key names, and `hook.<name>` is a reserved prefix. `profile` is **not allowed** in `.outfit/config` (§3.0); it gets a warning and is ignored.
- `base` is a reserved profile name meaning "no swaps".
- **Profile names are unique across all three tiers.** `outfit new` refuses to shadow an existing name, and `ls` shows each profile's tier.
  - If a teammate later commits a profile with the same name as your local one, the committed profile wins, `status` warns, and the fix it prints is `outfit new <other-name> --from <local>`.

### 3.2 State lives in git config, namespace `outfit.*`

**Active profile** is `outfit.profile`, with this precedence:
1. **`--worktree`** (needs `extensions.worktreeConfig=true`).
2. **`--local`** is the user's default for this clone. Never use `--global` for the profile: profile names are per repo.
3. Otherwise `base`: no swaps, the same as a non-user (§3.0).

**Toggle resolution** for `outfit get <key>`, first hit wins:
1. `--worktree`
2. `--local`
3. `--global` (machine-wide; this is where "biome off on my small Mac" lives)
4. the active profile's `profile.config`
5. `.outfit/config`

**`locked` keys** always resolve from `.outfit/config`. Every other source is ignored, and `status` shows a warning.

**Never `include.path` a committed file.** A committed config can set `core.fsmonitor`, `core.pager`, `core.hooksPath` or `includeIf`, which means code execution on every teammate's machine. outfit parses the committed files itself and **accepts only `outfit.*` keys**; anything else gets a warning and is ignored.

### 3.3 Behavior toggles

There are two ways to use toggles:
1. **Without changing the team's hooks:** `outfit.hook.<name> = off` makes the dispatcher skip the chained team hook entirely, unless that key is locked. This alone serves the escapee and the small-Mac user.
2. **Fine-grained, if the team opts in:** team hooks gate themselves. Teammates without outfit installed are unaffected:
   ```sh
   command -v outfit >/dev/null && [ "$(outfit get biome)" = off ] && exit 0
   ```

**How `get` finds the repo without calling git:**
- Use `GIT_DIR` when set (it's set inside hooks).
- Otherwise walk up from cwd to `.git`.
  - If it's a directory, that's the git dir.
  - If it's a file, parse its `gitdir:` line, then read `commondir` inside that dir to find the common dir.
- Read `.outfit/config` and the active `profile.config` from disk.
- The only git call is `git config --list --show-scope -z`.

### 3.4 File swaps: sparse checkout plus a symlink (verified)

For each path the active profile swaps:
1. Add the non-cone sparse pattern `!/<path>` and set `sparse.expectFilesOutsideOfPatterns=true`.
   - Git marks the tracked file skip-worktree, `status` stays clean, and plain `git add <path>` refuses.
   - It survives `stash -u`, `reset --hard`, `clean -fdx`, `rebase` and `checkout`.
2. Run `mkdir -p` on the parent directory, then put a **relative symlink** at `<path>` pointing to the variant.
   - *Verified:* sparse checkout removes a directory whose only file got hidden, so `ln` fails without the `mkdir`.
   - **Committed profile:** `apps/web/AGENTS.md → ../../.outfit/profiles/p/apps/web/AGENTS.md`. Edits through the link show as ` M .outfit/profiles/p/…` in plain `git status`, so the answer to "where did my edit go?" is always visible. That's why symlinks win over copies.
   - **Local and worktree profiles:** an absolute link into the git dir. Those edits don't show in `git status`, by design, so `outfit status` lists them.
3. **Hide inactive committed profiles from disk** (*verified*). The managed sparse file is:
   ```
   # outfit-managed (do not edit; run `outfit sync`)
   /*
   !/AGENTS.md
   !/.outfit/profiles/*
   /.outfit/profiles/<active>/
   ```
   Only the active variant exists on disk. The others are still in git: view them with `outfit show <profile>`, which uses `git show HEAD:…`. `outfit author on|off` writes all of them to disk while you edit profiles side by side.
4. **Keep search tools out.** Add `/.outfit/` to `.git/info/exclude`, which is per clone and invisible to the team. *Verified* with ripgrep 14.1 and GNU grep 3.11:

   | Tool | What it finds |
   |---|---|
   | `rg`, including `--hidden` | nothing under `.outfit/` |
   | `rg --hidden -L` | only the canonical `AGENTS.md` |
   | `grep -r` | exactly one hit: the real file |
   | `grep -R` | both paths (accepted residual risk) |

   `.git/info/exclude` hides nothing tracked from `git status`, so edits still show.
5. **Write sparse state through git, never by editing the file directly:** `git sparse-checkout set --no-cone --stdin`. That updates the patterns, the index bits and the working tree together.
   - The patterns live per worktree in `info/sparse-checkout`.
   - The skip-worktree bits live in each worktree's index.
   - `git worktree add` copies the patterns (*verified*).
6. **Refuse foreign sparse checkout.** If sparse checkout is active and the file lacks the outfit marker (cone or non-cone), refuse with a clear error.
   - **Sparse checkout stays on** whenever committed profiles exist and author mode is off, even on `base`, because inactive profiles are always hidden. Only `uninstall` disables it, after removing all links.
7. **Unswap if the variant is missing.** If the active profile's variant for a path doesn't exist at HEAD (e.g. an old branch from before the profile), remove the pattern, restore the base file and report it. Never leave a dangling link that sync keeps recreating.
8. **Refuse to switch over uncommitted edits.** If `use` would remove a committed variant with uncommitted edits, refuse and print the fix: commit or stash first. Git won't delete a modified file on a pattern change, so switching anyway would leave the tree half-switched.

### 3.5 `sync`: a level-triggered, idempotent reconciler

Every run computes the desired state, diffs it against actual, fixes the difference, and stops. It never reacts to the specific event that triggered it.

- **The no-op path is exactly 2 git calls and 0 writes:**
  1. `git config --list --show-scope -z`
  2. `git ls-files -s -t -- <swapped paths>`, which prints `S 100644 <sha> 0\tAGENTS.md` (*verified* on 2.43; re-verify on 2.36 in CI). One call gives the skip-worktree tag, unmerged stages and the base blob.
  
  Never call `rev-parse` on this path.
- **Plan without the lock.** Only if the plan is non-empty: take `sync.lock` (`O_EXCL`, with stale-pid recovery), re-plan, then apply.
- All writes are atomic: write to a temp file, then rename.

How each path is classified:

| Actual state | Action |
|---|---|
| Correct link | nothing |
| Missing link, or dangling link | create the link |
| **Regular file where a link belongs** (an atomic-rename save by an editor or agent; git can't see it, so outfit is the only thing that catches it) | **absorb**: if the variant is unchanged since linking (hash in `state.json`), copy the file into the variant, then relink. If both changed, copy both to `quarantine/<ts>/`, leave the file in place, exit 3. **Never overwrite user bytes.** |
| **Unmerged entry or conflict markers** (on a conflicting merge git replaces the link with a regular file full of markers; it does *not* write through the link, *verified*) | don't absorb. Report the conflict, print the fix (`git add --sparse <path> && git commit`, then `outfit sync`), exit 3 |
| **Stale base** (`HEAD:<path>` blob ≠ the one recorded in `state.json`) | keep the swap. `sync` prints one stderr line and exits 0. `status` flags it and exits 4. `outfit diff <path>` shows what changed upstream, and `outfit ack <path>` re-records the base |

**Triggers** (installed by `init`):
- `post-checkout`: also fires on `git worktree add`, with an all-zero old sha (*verified*).
- `post-merge`: also fires on a fast-forward `git pull` (*verified*).
- `post-rewrite`: `git pull --rebase` fires `post-checkout` and then `post-rewrite` (*verified*).
- A Claude Code `SessionStart` hook entry, added by `init --agents` when `.claude/` exists.

`reset --hard` fires nothing; the next trigger catches up. There is no daemon.

### 3.6 The hook dispatcher

- **Placement:** set `core.hooksPath` at **`--worktree`** scope to the **absolute** path `<git-common-dir>/outfit/hooks`, computed at `init` with `git rev-parse --path-format=absolute --git-common-dir`. `sync` repairs it if the repo moves.
  - *Verified:* worktree scope beats `--local`, so husky rewriting `core.hooksPath` on every `npm install` can't win.
  - *Verified bug if you get this wrong:* a relative hooksPath resolves per worktree. In linked worktrees `.git` is a file, so no hooks run at all.
- **Shims are tiny POSIX sh.** Each one runs `outfit hook <name> "$@"`, using the absolute binary path recorded at init and falling back to `PATH`.
  - **If outfit is gone, the shim must still run the team's original hook directly** and print one warning. Uninstalling the binary must never silently disable team hooks.
- **Chaining:** `outfit hook` first runs the original hook from the effective non-outfit `core.hooksPath` (`--local`, then `--global`, then `--system`), or else from `<git-common-dir>/hooks`.
  - It passes args, stdin (pre-push) and the exit code through exactly (*verified* with a husky v9 layout).
  - It skips the original hook if `hook.<name>=off` and the key isn't locked.
  - Then it runs outfit's own logic: sync on `post-*`, guards on pre-commit.
- **Exit codes:** a `post-*` hook returns the chained hook's exit code. **outfit's own result never changes it**; problems go to stderr.
  - *Verified:* a failing post-checkout makes `git worktree add` exit non-zero even though the worktree was created.
- **The pre-commit guard rejects a commit when:**
  - any swapped path is staged as mode `120000`
  - any swapped path is staged at all. Two exceptions: `OUTFIT_ALLOW=1`, or a merge in progress (`MERGE_HEAD` exists) with a regular-file blob staged
  - the expected sparse config is missing
  
  Every rejection message prints the exact fix.
  - *Verified:* `git sparse-checkout disable` followed by `commit -a` **committed the symlink into history**. That's why the guard exists.
- **New worktrees:** `worktree add` copies the **whole** `config.worktree` (*verified*), including `outfit.profile`.
  - On the all-zero-sha `post-checkout`, run `git config --worktree --unset-all` for each copied `outfit.*` key, so the new worktree follows `--local`.
  - Keep the copied `core.hooksPath` and the sparse settings.

## 4. CLI

| Command | Purpose |
|---|---|
| `outfit init [--agents] [--dry-run]` | install the dispatcher, worktreeConfig, the info/exclude entry, and the SessionStart entry |
| `outfit use <profile> [--here]` | set the clone's profile, or with `--here` this worktree's, then sync. Warns if a worktree override shadows the clone default here |
| `outfit set <key>=<val>… [--here\|--global]` | personal toggle override; `key=` unsets |
| `outfit status` | the active profile, where every value comes from, the tier of each file, and sync/stale/conflict state |
| `outfit sync` | reconcile; silent on success. Hooks and agents call it |
| `outfit ls` | list profiles: name, tier (committed/local/worktree), keys, files |
| `outfit new <name> [--from <profile>] [--here]` | create a local profile, or a worktree profile with `--here`, starting from base or another profile. **No versioning, no snapshots, no history**: plain files, edited in place |
| `outfit promote <name>` | copy a local profile into `.outfit/profiles/<name>/` so git versions it; the local copy is removed |
| `outfit show <profile> [path]` | print an inactive profile's files from `HEAD` without switching |
| `outfit author on\|off` | write all committed profiles to disk for side-by-side editing, or hide them again |
| `outfit update [--version vX] [--check]` | self-update (§5) |
| `outfit uninstall` | restore everything (see below) |
| `outfit version` | version, commit, build date, git version found |

**`uninstall` order matters:**
1. Remove links and restore base files **before** disabling sparse checkout (*verified*: otherwise you get a ` T` status and `commit -a` commits `120000`).
2. Then remove hooksPath, the `outfit.*` config, the info/exclude entry and the `outfit/` dirs.

It is golden-tested. Local profiles survive unless `--purge`.

Plumbing commands: `get <key>`, `diff <path>`, `ack <path>`, `hook <name>`.

Zero to "biome off on my machine" is `outfit init` followed by `outfit set biome=off --global`.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | ok |
| 1 | error |
| 2 | usage, or unknown profile/key |
| 3 | needs a human (conflict or quarantine) |
| 4 | out of sync or stale (`status` only) |

`--json` errors carry `"fix": "<exact command>"` and `"retryable": bool`. Agents follow error text, so make the error text the guide.

```
profile  yolo          (worktree; clone default: lean)
toggles  biome=off*    tests=off   secrets-scan=on (locked)
files    AGENTS.md ← yolo (committed)   CLAUDE.md ← mine (local, edited 2h ago)
state    in sync
*  set --global
```

## 5. Self-update (modelled on veld, plus the signature layer veld lacks)

veld's `update` re-runs its install script. Do it **natively in Go** instead:

1. **Resolve the target version.**
   - With `--version`: that exact version, downgrades allowed.
   - Otherwise ask the GitHub API for the latest release.
   - **The repo is private, so authentication is the normal path.** Find a token in this order: `OUTFIT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, then the output of `gh auth token`.
   - Download assets through the API's asset endpoint (`/repos/prosperity-solutions/outfit/releases/assets/<id>` with `Accept: application/octet-stream`), which works for private repos.
   - With no token, try the anonymous public URLs. This path starts working on its own once the repo becomes public.
   - With no token and no public access, fail with an error naming the fix: `gh auth login`, or set `OUTFIT_GITHUB_TOKEN`.
   - Never log the token.
   - One attempt, 5 s timeout. No retry loops.
2. **Download and verify.**
   - Download `outfit_<ver>_<os>_<arch>.tar.gz`, `checksums.txt` and `checksums.txt.minisig`. macOS uses `darwin_all`.
   - **Verify the minisign signature** against public keys compiled in from `internal/update/keys.txt` (committed, seeded from the repo variable `MINISIGN_PUBLIC_KEY`). It's a list, so a key rotation ships as a release that adds the new key before any release is signed with it.
   - Then verify the archive's SHA-256.
   - **Fail closed.** A dev build with no keys refuses to update; the error names the fix.
   - Check the tar entries against an allow-list.
3. **Replace the binary atomically.**
   - `os.Executable()` → `EvalSymlinks` → write a temp file in **the same directory** → `chmod 0755` → `os.Rename`.
   - If that directory isn't writable, print the exact `curl … | OUTFIT_INSTALL_DIR=… sh` command instead.
   - On macOS, best-effort `xattr -d com.apple.quarantine`. **Never re-sign.**
4. **Keep hooks working across the update.** The shims point at the same path, so they keep working. `state.json` carries a schema version and new binaries migrate it forward.
5. **Update banner** (`Update available: X → Y. Run outfit update`).
   - Shown **only** on human-facing commands (`status`, `ls`, `use`, `set`, `init`, `show`, `new`), and only when stderr is a TTY, `--json` is off, `CI` is unset and `OUTFIT_NO_UPDATE_CHECK` is unset.
   - **Never** in `sync`, `get`, `hook` or `diff`, because those run inside git hooks and must stay fast.
   - Check at most every 24 h, with the result cached in `os.UserCacheDir()/outfit/update.json`.
   - The check runs in a goroutine with an 800 ms budget. If it isn't done by then, skip it and try next time. Write the cache only after a successful check.
6. **Concurrency:** take an update lock file; a second `update` exits 0 with a note.

Tests:
- Use a fake release server (`httptest`) for: happy path, bad checksum, bad signature, unknown key, missing asset, downgrade, read-only install dir, and a banner that never appears on hook commands.
- On real native runners: update from the previous real release to the new one (§8.2).

## 6. Code layout

```
cmd/outfit/main.go
internal/
  cli/       flags, output (human/json), exit codes
  gitx/      the ONLY place that execs git; counts calls; GIT_OPTIONAL_LOCKS=0 on reads
  repo/      git-dir discovery without exec (§3.3)
  config/    committed-config parser (outfit.* whitelist), precedence, locked
  plan/      PURE: Plan(Desired, Actual) -> []Action. No I/O.
  apply/     symlinks, sparse via gitx, quarantine, state.json, lock
  hooks/     shims, dispatcher, chaining, guards
  profiles/  tiers, new/promote/show/author
  update/    resolver, minisign verification, atomic replace, banner cache, keys.txt
internal/testkit/        fixture factory
testdata/script/*.txtar
scripts/demo.sh          the 5-minute walkthrough the maintainer runs to test it (also a CI job)
install.sh
docs/                    git-behavior.md, macos-signing.md, release.md, architecture.md
```

## 7. Tests: the bulk of the work

### 7.1 Pyramid

- **Unit, ~70%.** `plan`, `config` and `update` with a fake fs and a fake git state.
- **Integration, ~20%.** Real git in `t.TempDir()`.
- **E2E, ~10%.** `testscript`, with these custom commands:
  - `atomicsave <file> <text>` (write to a temp file, then rename)
  - `islink`
  - `blobsha`
  - `gitver >= X`
  - `hooklog`

### 7.2 Hermetic environment for every test

```
HOME=$WORK/home
GIT_CONFIG_GLOBAL=$HOME/.gitconfig
GIT_CONFIG_NOSYSTEM=1
GIT_AUTHOR_NAME, GIT_AUTHOR_EMAIL, GIT_COMMITTER_NAME, GIT_COMMITTER_EMAIL   fixed
GIT_AUTHOR_DATE=@1700000000 +0000
GIT_COMMITTER_DATE=@1700000000 +0000
TZ=UTC
LC_ALL=C
GIT_TERMINAL_PROMPT=0
init.defaultBranch=main
OUTFIT_NO_UPDATE_CHECK=1
```

### 7.3 Fixture factory

The call looks like `testkit.New(t, Monorepo, WithHusky(), WithLefthook(), WithUpstream(), WithConflictBranch(), WithWorktrees(n), WithCone())`.
- History is built from one `git fast-import` stream.
- Fixtures are cached per (recipe, git version) and copied per test.
- Fake husky and lefthook hooks are stub scripts that log their argv and stdin.

### 7.4 Scenarios, data loss first

Each scenario is an e2e test:
1. An atomic-rename save is absorbed byte-identically and the path is relinked.
2. When file and variant both changed, both are quarantined and the exit is 3.
3. Conflict markers are never absorbed; the exit is 3.
4. A clean pull that changes the base gives a stale status (exit 4), with the variant untouched.
5. None of `commit -a`, `add -A`, `add --sparse <path>`, or `sparse-checkout disable` followed by `commit -a` can commit a swapped path or mode `120000`.
6. `reset --hard`, `checkout -f`, `clean -fdx`, `stash -u`/`pop` and `rebase -i` never destroy variant bytes.
7. Killed at every injected fault point and rerun, sync converges with nothing lost.
8. 10 parallel syncs across a worktree farm are serialized and end valid.
9. `worktree add` gets its links, the copied `outfit.profile` is stripped, and `use --here` still overrides.
10. `uninstall` restores identical `git config --list` output, sparse state and working tree.
11. Husky, lefthook and plain hooks: args, stdin and exit codes pass through, a husky hooksPath reset doesn't win, and **with the binary deleted, the shims still run the team hooks**.
12. A malicious committed `.outfit/config` (`core.fsmonitor`, `include.path`, `includeIf`, `core.hooksPath`) is ignored with a warning and nothing executes.
13. A `locked` key can't be overridden from any scope or profile.
14. Foreign sparse checkout is refused, and a commit in a **linked worktree** is still guarded.
15. macOS: case-insensitive APFS, spaces in paths, NFD Unicode, and a subdirectory whose only file was swapped.
16. A variant missing at HEAD is unswapped, with no dangling link and no loop.
17. A failing sync inside `post-checkout` doesn't change git's exit codes.
18. Inactive profiles aren't on disk, `rg --hidden` finds nothing under `.outfit/`, and `author on/off` round-trips.
19. Local and worktree profiles: `new`, edits, `promote`, `git worktree remove` deletes worktree profiles, and name clashes are refused.
20. `use` with uncommitted edits in the active committed variant is refused, with nothing half-switched.
21. **The default variant (§3.0):** a clone with `.outfit/` committed, on a machine without outfit, has every hook running, the base files, a clean status, and the fallback snippet returning the committed toggle value.

### 7.5 Property tests and fuzzing

- **`rapid` model-based tests** over random sequences of: use, set, new, edit-through-link, atomicsave, pull, merge, worktree add/remove, stash/pop, rebase, sync. Invariants:
  - no user byte is ever lost
  - sync run twice gives an empty plan and touches no mtimes
  - no swapped path is ever staged
  - a clean status stays clean
- **Go fuzz targets:**
  - the config parser
  - the sparse block round-trip
  - the key whitelist
  - `Plan`
  - the tar allow-list

### 7.6 Performance gates

- **Hard:** exec counting (sync ≤ 2, get ≤ 1) on every OS.
- **Soft, Linux only:** `hyperfine` comparing `outfit sync` against `git rev-parse --git-dir`. Fail if the medians differ by more than 10 ms.

## 8. CI, release, signing, install

Copy veld's conventions, and read veld's own files for the details (§9.1).

### 8.1 `ci.yml`, on pull requests and on pushes to `main`

- **Draft PRs run nothing.** Every job has `if: github.event_name != 'pull_request' || github.event.pull_request.draft == false`, and `ready_for_review` is in the trigger types.
  - Port veld's draft-guard linter (`tests/validate-workflow-gates.py`).
  - Remember: **a skipped job looks green.**
- **Jobs:**
  - `commits`: Conventional Commit subjects.
  - `ship`: §9.
  - `lint`: gofmt, `go vet`, golangci-lint, govulncheck, shellcheck `-s sh` on install.sh and the shims, actionlint, and `goreleaser check` once `.goreleaser.yaml` exists. Skip that step while the file is absent, so the bootstrap can go green.
  - `unit`: `-race`, on Linux and macOS.
  - `integration`/`e2e` matrix:

    | Runner | Git versions |
    |---|---|
    | `ubuntu-24.04` | **2.36** and latest, built from source and cached |
    | `ubuntu-24.04-arm` | latest |
    | `macos-latest` (arm64) | Apple git and Homebrew git |
    | `macos-15-intel` | Apple git |
  - `fuzz`: short runs.
  - `perf`.
  - `demo`: runs `scripts/demo.sh`.
  - **`all-green`**: `needs:` every job above, with `if: always()`. It fails unless each needed job **succeeded**. This is the single name the ruleset requires; matrix jobs report expanded names like `e2e (ubuntu-24.04, 2.36)`, so a check called `e2e` never reports.
  - `install`: install.sh against a fake local release on Linux and macOS: happy path, pinned version, bad checksum (must abort and leave nothing installed), unsupported arch via a stubbed `uname`, and a dir not on PATH.
- **Target:** under 12 minutes of wall time.
- **Private-repo minutes.** macOS minutes count about 10× against the quota.
  - On PRs, macOS runs only `unit` and one `e2e` leg (Apple git on `macos-latest`).
  - The full macOS matrix (Homebrew git, `macos-15-intel`) runs on push to `main` and in `release.yml`.
  - Linux arm64 standard runners are available to private repos.

### 8.2 `release.yml`, on push to `main`. The artifacts are tested before they're published

The whole workflow runs with `concurrency: {group: release, cancel-in-progress: false}` and checks out with `fetch-depth: 0`, so two quick merges can't compute the same tag.

1. **`version`.** `svu next --v0` (caarlos0/svu) computes the next version from Conventional Commits since the last tag.
   - `--v0` keeps `feat!` from jumping to 1.0.
   - With no tags yet it starts from 0.0.0; make the first release `v0.1.0`.
   - If nothing is releasable, stop.
   - Nothing is ever committed to `main`: the version comes from the tag only, via ldflags.
2. **`build`** (ubuntu): create the tag **locally**, then run `goreleaser release --clean --skip=publish`. GoReleaser only requires the tag on a clean HEAD, not pushed.
   - Targets: linux amd64/arm64, plus **darwin universal** (`universal_binaries`, `replace: true`).
   - Assets: `outfit_<ver>_<os>_<arch>.tar.gz` with `darwin_all`, `checksums.txt`, and `checksums.txt.minisig` (signed with minisign; §8.4).
   - Signing per §8.3.
   - Upload `dist/` as a workflow artifact.
3. **`smoke`.** A matrix on `ubuntu-24.04`, `ubuntu-24.04-arm`, `macos-latest` and `macos-15-intel`:
   - download `dist/`
   - serve it locally
   - run **install.sh** against it
   - run `outfit version`
   - run `scripts/demo.sh --quick`
   - run `outfit update --check` against the fake server
   
   Every native architecture executes the real artifact here.
4. **`publish`.** Push the tag, then `gh release create` with the exact files from `dist/`. veld hand-rolls this because `softprops/action-gh-release` left releases stuck as drafts. Create the release as a draft, upload with retries, verify names and sizes, then publish it (it stays visible only to repo members, because the repo is private).
5. **`verify`.** On the same four runners:
   - Run the real install with the workflow token, exactly as a teammate would:
     ```sh
     curl -fsSL -H "Authorization: Bearer $GH_TOKEN" https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
     ```
     Set `GH_TOKEN: ${{ github.token }}` and `permissions: contents: read`. Retry every 60 s for up to 10 min, because raw.githubusercontent caches for about 5 min. Assert that `outfit version` equals the tag.
   - Run `outfit update` from the previous release to this one, but **only once the previous release itself contains `outfit update`**. Until then, list this check under "Not verified" in the report.

### 8.3 macOS signing and notarization: both optional, both enabled by secrets

veld's finding, which you should keep:
- **curl never sets `com.apple.quarantine`, so Gatekeeper never checks a curl-installed CLI.** Notarization isn't needed for this install path.
- A bare Mach-O binary can't be stapled anyway.
- Write this argument into `docs/macos-signing.md`.

The pipeline must also work **without** the `MACOS_*` secrets: unsigned darwin binaries and a log line explaining why. The owner intends to set them, so signing and notarization are expected to run.

**When `MACOS_SIGN_P12` exists:**
- Sign with Developer ID, hardened runtime and a timestamp.
- Prefer GoReleaser's built-in `notarize.macos` (quill), which signs from Linux. If it can't sign the universal binary, sign on a macOS runner with `codesign`, as veld's `scripts/release/macos-codesign.sh` and `macos-keychain.sh` do: temporary keychain, identity picked by team ID, assertions on identifier, runtime flag and timestamp, keychain deleted afterwards.

**When the `MACOS_NOTARY_*` secrets also exist:** notarize. With quill, set GoReleaser's `notarize.macos.notarize` with `wait: true` and `timeout: 20m`. With the native fallback, zip it, run `notarytool submit --wait`, and dump the log on failure. Don't staple.

Both `MACOS_SIGN_P12` and `MACOS_NOTARY_KEY` are **base64 contents**, which quill accepts directly. A `verify-signing` step on `macos-latest` checks the result: `codesign --verify --strict`, `spctl -a -t install` (or `codesign -dv` showing the Developer ID authority), and a `TeamIdentifier` equal to the repo variable `MACOS_TEAM_ID` when that variable is set. It runs whenever the secrets exist, and a failed check fails the release.

Port veld's signing smoke test, which uses a throwaway self-signed certificate, so the signing path is exercised on every PR even without real secrets.

All secrets are **repo secrets**, and only `release.yml`, which runs on push to `main`, references them. Gate quill's notarize step with `enabled: '{{ isEnvSet "MACOS_SIGN_P12" }}'`; quill handles universal binaries.

### 8.4 Update signing key: set up by the owner, used by you

- **The owner created the keypair and set `MINISIGN_SECRET_KEY`, `MINISIGN_PASSWORD` and the variable `MINISIGN_PUBLIC_KEY` up front.** You never generate, read or write a secret.
- **Bootstrap step:** read the public key with `gh variable get MINISIGN_PUBLIC_KEY` and commit it to `internal/update/keys.txt`.
  - The value is the standard two-line minisign `.pub` format: an `untrusted comment: minisign public key 93E83CF19144CC30` line, then the base64 key `RWQwzESR8T…`.
  - The parser skips comment lines and accepts one or more key lines. A test asserts that the committed file parses to key ID `93E83CF19144CC30`.
  - If the variable is missing, keep building. Raise it in the `needs-maintainer` issue and leave the release job failing with a message that names the missing variable.
- **In CI, sign `checksums.txt` with aead's minisign CLI** (`go install aead.dev/minisign/cmd/minisign@latest`). It's compatible with keys made by the reference `minisign` and reads the password from non-TTY stdin, so sign through GoReleaser:
  ```yaml
  signs:
    - cmd: minisign
      stdin: '{{ .Env.MINISIGN_PASSWORD }}'
      args: ["-S", "-s", "{{ .Env.MINISIGN_KEY_FILE }}", "-m", "${artifact}", "-x", "${signature}"]
      artifacts: checksum
      signature: '${artifact}.minisig'
  ```
  Write the secret to a `mktemp` file with mode 600, export its path as `MINISIGN_KEY_FILE`, and delete it in an `always()` step. Verify the exact flags against that CLI's help first.
- **Add a CI check that signing actually works:** after `build`, verify `checksums.txt.minisig` with the committed `keys.txt`, using the same Go verifier the updater uses.
- **Key rotation** is documented in `docs/release.md` as two releases: add the new public key to `keys.txt` and ship that release, then switch the secret.

### 8.5 `install.sh`, about 150 lines of POSIX sh, modelled on veld's

- **Structure:**
  - A header listing every env var: `OUTFIT_VERSION`, `OUTFIT_INSTALL_DIR`, `OUTFIT_GITHUB_TOKEN`, `OUTFIT_BASE_URL` and `OUTFIT_API_URL` (both for tests).
  - The whole body is wrapped in `main() { … }; main "$@"`.
  - `set -eu`.
  - curl flags: `--retry 3 --retry-delay 2`.
- **Platform:** `uname -s`/`-m` gives `linux_amd64`, `linux_arm64` or `darwin_all`. Anything else fails with a clear message.
- **Version:** latest, or pinned with `OUTFIT_VERSION`.
- **Private-repo first.** Find a token in the same order as the updater: `OUTFIT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, then `gh auth token`.
  - **With a token:** resolve the release and its asset IDs through the API, and download with `Accept: application/octet-stream`.
  - **Without a token:** fall back to the public `releases/latest/download/…` URLs, which work only once the repo is public.
  - Never echo the token.
- **The install command while the repo is private.** `docs/outfit.md` and the README show this:
  ```sh
  curl -fsSL -H "Authorization: Bearer $(gh auth token)" https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
  ```
  It works for anyone with read access to the repo. Also document the token-free command, labelled *"once public"*.
- **Download and verify:**
  - Use a `mktemp -d` with an EXIT trap.
  - Check SHA-256 (`shasum -a 256` on macOS, `sha256sum` on Linux). **Fail closed.**
  - If a `minisign` binary is on PATH, also verify the signature against the key embedded in the script.
  - Check the tar entries against an allow-list.
- **Install location:** reuse the directory of an existing `command -v outfit`, else `OUTFIT_INSTALL_DIR`, else `~/.local/bin`. Never use sudo.
- **Atomic install:** stage the binary in the destination directory, then `mv -f`.
- **PATH:** if the directory isn't on PATH, print the exact line to add.
- **Finish** by printing `outfit version`.
- **Contract test,** as veld's `validate-install-contract.sh` does: every `OUTFIT_*` var the Go code and docs mention is read by install.sh, and vice versa.

## 9. The ship workflow: port veld's, then live by it

### 9.1 Source

Clone `https://github.com/prosperity-solutions/veld` into a **scratch directory outside the repo** and record the commit SHA in `docs/ship.md`. Read the following before writing anything:
- `.agents/skills/ship/SKILL.md`
- `docs/agentic-review.md`
- `CLAUDE.md` and `AGENTS.md` (the structure, not the content)
- `scripts/dev/prmeta.sh`, `scripts/dev/ship-nag.sh`
- `tests/validate-ship-stamp.sh`
- the `ship` and `commits` jobs in `.github/workflows/ci.yml`
- `.claude/settings.json`, `lefthook.yml`
- `tests/validate-workflow-gates.py`
- `install.sh`, `.github/workflows/release.yml`, `scripts/release/*`, `docs/macos-signing.md`

### 9.2 What to port

| veld | outfit |
|---|---|
| `.agents/skills/ship/SKILL.md` | **adapt**. Keep: MINDSET (question everything, no parity, no follow-ups, don't play human), model tiering, the gate text, "If you are not Claude Code", Step 0 (minus the lock), Steps 1, 4, 5, 6, "Shipping means fixing what you hit on the way", "When to involve the human". Drop: icons, Mantine, the dev stack, the desktop app, the website, promotions, extensions-vision, keyboard shortcuts, cargo. The pre-pass becomes `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`. The stale check becomes `git fetch origin && git rev-list --count HEAD..origin/main`. Keep `metadata.internal: true` |
| `docs/ship.md`, `.claude/skills/ship` | symlinks to the skill, as in veld |
| `docs/agentic-review.md` | **adapt**:<br>§1: dependency sources live under `$(go env GOMODCACHE)`.<br>§1.1: the Go pre-pass plus actionlint and shellcheck.<br>§3.1: "schema migration" means `state.json`, the config format, or `--json` output shapes.<br>§3.3: the stakes paths are `internal/apply`, `internal/hooks`, `internal/update`, `install.sh`, `release.yml` and the config whitelist; any data-loss path gets the full loop.<br>Everything else is copied as-is. |
| `CLAUDE.md` | copy the structure: question vs change; changes invoke `ship` first |
| `AGENTS.md` | write a new short one: "Before you change anything", Agent Skills (ship), the PR workflow, the CI cost and draft convention (including the trap that a skipped job looks green), a docs checklist (README, `--help` text, `docs/`, the `install.sh` header, `docs/git-behavior.md`) and the conventions |
| `scripts/dev/prmeta.sh` | **adapt**. Pick a new passphrase literal of the form `word-NNNN`, set `_EXPECT` to its sha256, generate a new HMAC key for `_FOLD`, and change the message prefix to `outfit-ship:v1:<branch>`. The stamp is a tripwire, not a secret, and the passphrase literal goes in the skill text |
| `scripts/dev/ship-nag.sh`, `tests/validate-ship-stamp.sh` (with `--selftest`) | copy, with `.veld-ship.json` renamed to `.outfit-ship.json` and the new passphrase |
| `.claude/settings.json` | copy the `UserPromptSubmit` ship-nag hook and the `find /` guard. Drop the lock hook |
| `lefthook.yml` | `pre-push`: ship-marker (priority 1) plus the Go pre-pass |
| lock system | **drop** |
| semantic-release | **drop**: the tag-only release in §8.2 never pushes to `main` |

`.gitignore` adds `/notes/`, `.outfit-ship.json` and `/dist/`. Create the `no-ship` and `needs-maintainer` labels.

**When porting, remove everything in veld's skill that would stall an unattended run:**
- Rewrite Step 0: *"Answers stated by the maintainer in the request or in `HANDOVER.md` §9.4 count as given; ask only for what neither covers."* Also drop the "stop and ask" bullet about kickoff answers.
- Delete the icon, website and promotion questions, and the ~10k-line stop.
- Step 6 merges with `gh pr merge --squash --delete-branch`. Use `--admin` only once a ruleset exists, and only after reading the `ship` job's own conclusion.

A skill file created mid-session may not register as `/ship` until a restart. If invoking it fails, read `docs/ship.md` in full and follow it; it's the same text.

### 9.3 Bootstrap: the only commit that skips ship

The ship gate verifies with the helper from the PR's base commit, so the very first commit can't pass through it. Push **one** commit straight to `main`, titled `chore: bootstrap repository and ship workflow`, containing:
- the ported ship system
- `CLAUDE.md` and `AGENTS.md`
- `go.mod` and `cmd/outfit` with `outfit version` only
- `ci.yml` with the `commits`, `ship`, `lint` and `unit` jobs
- `.gitignore`, `LICENSE`, a stub `README.md`, `lefthook.yml`
- `docs/git-behavior.md` seeded with every *verified* claim from this document
- `.claude/agents/` with project copies of the four agents (§9.5)
- `NOTICE` crediting veld (MIT, with its copyright line) for the ported ship workflow

Run `bash tests/validate-ship-stamp.sh --selftest` locally before pushing.

**Every change after that goes through `/ship`.**

### 9.4 Kickoff answers for every PR in this run, pre-authorized by the maintainer

Write these into `.outfit-ship.json` with the current branch at each kickoff. Don't ask.

```json
{ "branch": "<current>", "review_depth": "standard", "checkpoints": "none",
  "merge_policy": "bypass-on-green", "docs_scope": "per-checklist" }
```

- **Review depth** is `standard` for everything. Diffs under 50 lines with no stakes paths may use the trivia clause. Stakes paths always get the full loop.
- **Checkpoints: none.** Instead of a hand-over, drive the CLI yourself against a fixture repo and paste the terminal transcript into the PR body under **"Driven by hand"**.
- **Merge:** no branch protection exists until PR 11, so `gh pr merge --squash --delete-branch` (no `--admin`) once the ready run's CI jobs are **actually** green. Check the jobs with `gh run view` as veld's Step 6 does, never the rollup.
- **Vision calls:** this document is the spec. Where it's silent, pick, and record the choice under "Decisions made unattended".
- **The ~10k-line stop** is waived for this run.

### 9.5 The agent roster, committed with the repo

The roster is `code-reviewer`, `explorer`, `researcher` and `builder`; their exact content is in the setup steps above.
- **The agents are already in `.claude/agents/`,** placed there by the human. Commit them unchanged in the bootstrap, so the repo carries its own autonomy, the way veld does.
- **Write the three checkpoints into the repo's `CLAUDE.md`**, next to the "invoke `ship` before your first edit" rule:
  1. Run `code-reviewer` before a multi-file plan locks.
  2. Run `code-reviewer` when the same test or compiler error fails twice.
  3. Run `code-reviewer` on the full diff before declaring done or staging the final commit.
- **Fit the roster into the ported ship skill's "Who does the work":**
  - Haiku explorer → `explorer`.
  - Sonnet builder → `builder`, which already has `isolation: worktree`.
  - Opus's three moments → `code-reviewer`.
  - The review loop in `docs/agentic-review.md` keeps its own per-angle model routing, set with the per-invocation `model` parameter.
- **Subagents return structured summaries only:** tables, verdicts, SHAs. Never file dumps. Your main context stays small, and high-volume work (reading many files, running suites) happens in subagent contexts.
- **`isolation: worktree` branches from the default branch,** not from your HEAD. Commit what a builder needs before you spawn it, and take its work back as a commit or a patch.

## 10. This run's PRs, in order

Each PR is merged before the next one starts, unless it's blocked; in that case skip ahead per §0.

| # | PR | Contents |
|---|---|---|
| 0 | bootstrap | §9.3, pushed directly |
| 1 | `feat: test harness and git layer` | `gitx` with exec counting, `repo` discovery, the version floor check, `testkit` and the hermetic env, testscript wiring. The CI integration matrix with git builds from source |
| 2 | `feat: config and toggles` | parser and whitelist, precedence, `locked`, `get`/`set`/`ls`/`status` (toggles) |
| 3 | `feat: hook dispatcher` | `init`/`uninstall` (hooks only), shims with the binary-missing fallback, chaining for husky, lefthook and plain hooks, `hook.<name>=off`, the pre-commit guard. *After this PR, "biome off" and `yolo` work for real* |
| 4 | `ci: release pipeline and install.sh` | §8.2–8.4, `install.sh` and its tests, the signing smoke test. Merging it cuts **v0.1.0**, which already ships working toggles and hooks |
| 5 | `feat: self-update` | §5. From the next release on, the real update leg in `verify` runs |
| 6 | `feat: profile file swaps` | sparse block, symlinks, `use`, plan/apply, absorb, quarantine, lock, inactive-profile hiding, info/exclude, the default-variant scenario (§3.0), `author`, `show` |
| 7 | `feat: conflict, staleness, worktrees` | conflict and stale detection, `diff`/`ack`, missing-variant unswap, worktree-add stripping, SessionStart, the uncommitted-edit refusal |
| 8 | `feat: local and worktree profiles` | `new`, `promote`, the tiers in `ls`/`status`, worktree-remove cleanup, the name-clash rule |
| 9 | `test: properties, fuzzing, perf gates` | §7.5 and §7.6, plus the full scenario list |
| 10 | `docs: readme, demo, docs` | README (one-line install first, then a 60-second demo), `scripts/demo.sh`, `docs/*`, `init --team`, and the `--help` text reviewed |
| 11 | ruleset | Add a ruleset on `main` requiring a PR and the `ship`, `commits` and `all-green` checks. Use the exact names `gh pr checks` shows on the last PR, with an admin bypass; releases only push tags, so that's safe. Rulesets on a **private** repo need a paid GitHub plan. If the API refuses, skip this PR and note it under "Slipped", with the exact `gh api` call to run once the repo is public or the plan allows it. **Then open a no-op `chore:` PR through `/ship` to prove merging still works**, and keep it as the last merge of the run |

## 11. The run report

A GitHub issue titled **"🌅 Run report"**, created right after the bootstrap commit, pinned, and updated after every merge. A copy lives in `notes/REPORT.md`. Its final form contains:

1. **Try it in 5 minutes:** the install one-liner, then `scripts/demo.sh`. The demo creates a throwaway monorepo with a fake husky setup and a deliberately slow fake "biome" hook, then walks through:
   - `init`
   - `set biome=off --global`
   - `use yolo`
   - editing through the link
   - an atomic save
   - `worktree add`
   - `new` and `promote`
   - `update --check`
   - `uninstall`
   
   Each step is printed before it runs.
2. **What shipped:** every PR with its link, the release versions, and the CI status of the last run.
3. **Decisions made unattended:** collected from all PR bodies.
4. **Blocked or needs you:** with links. Empty is the goal.
5. **Slipped:** each item with its issue (§0).
6. **Secrets created:** the minisign key's location, and the instruction to move it to a password manager and delete `~/.config/outfit-release/`.
7. **Not verified:** anything that only a real Mac, Apple credentials or a human can confirm.

## 12. Out of scope

- profile inheritance and aspect composition
- a daemon or file watcher
- MCP
- analytics
- generating per-tool instruction files
- swapping directories
- `outfit env`/`schema`
- Windows
- repos that already use sparse checkout
- Homebrew (v1.1)
- versioning or history for local profiles

## 13. Prior art, for context only

- repoverlay, clank, layer and monodev do additive untracked overlays via `.git/info/exclude`.
- Sapling sparse profiles do role-based hiding, but only in Sapling.
- Claude Code `worktree.sparsePaths` gives directory-level sparse checkout.

None of them swap **tracked** files per profile while also toggling behavior. That gap is outfit.
