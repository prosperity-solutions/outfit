---
name: ship
description: >
  Carry a change to the outfit repo from empty diff to merged PR the way this
  project expects — autonomous implementation, adversarial review rounds, draft
  PR, mark ready only once the local review is done (CI does not run on drafts),
  wait for green CI, and merge on green. For a reported bug it first measures
  whether the reported gap is real. Use when the maintainer says "ship this",
  "build and merge X", "implement and open a PR", "take this to merge", or hands
  over a feature/fix to carry all the way to main. Not for one-off edits with no
  PR.
metadata:
  # Contributor-only dev tool. `npx skills` scans `.agents/skills/` (where this
  # file lives) and `.claude/skills/` (which symlinks to it), so without this
  # flag it would be installed into unrelated projects. `internal: true` is the
  # CLI's opt-out in every one of those discovery paths.
  internal: true
---

# ship — carry an outfit change to merge

You are the engineer of record for this change. Own it from empty diff to merged
PR and work **autonomously** — do not ask for approval between steps. The only
reasons to stop and ask are in **When to involve the human** below.

Read [AGENTS.md](AGENTS.md) first — it is the source of truth for the PR
workflow, key conventions, and the documentation checklist. This skill is the
operational wrapper around it, not a replacement. Lean on sub-agents throughout,
tiered by model as **Who does the work** below sets out — a second opinion is
cheap, and being your own strongest critic is the job.

## MINDSET

**QUESTION EVERYTHING.** AGENTS WROTE ALL OF THIS REPO, BUT THEY WROTE IT THE
WAY HUMANS WORK. Every rule, layout and convention — this file included — is
*physics* (true because of how git, the filesystem, the network or the security
model works) or *habit* (true because that is how humans did it). Physics stays.
Habit dies. "That is how it is done" is not a reason, and neither is "that is how
the industry does it".

**NO PARITY.** BREAKING CHANGES ARE FINE WHEN THE VISION NEEDS THEM. Do not keep
an old flag, command or shape alive just so nothing changes.

**NO FOLLOW-UPS.** BUILD THE WHOLE THING. No half shipped with a plan for the
rest, no "phase 2" for somebody else. If the task needs it, it is in this PR.

**DO NOT PLAY HUMAN.** No time estimates, no "this would take weeks", no
handoffs between imaginary departments. There is no QA team or ops team
downstream. You are all of them, and the work is done when it merges.

Where those four stop:

- **Change a rule in a PR, never by ignoring it mid-run.** The stamp and Step 0's
  answers hold until the maintainer or a merged change says otherwise. Delete a
  habit in the PR whose task needs it gone, under its own heading in the PR body;
  one you merely noticed goes in the body's list of things outside this task.
- **A red test is evidence, not habit.** Change it only when the behaviour it
  pins is the one you meant to change — never just to get green. Never disable a
  test to get to green.
- **The task or the maintainer says when the vision needs a break.** A break the
  task did not ask for is a **Vision call**: record it under "Decisions made
  unattended" in the PR body, with the option you rejected.
- **Mark a break with `!` in the commit and the PR title** (`feat(cli)!: …`).
  The title becomes the squash commit, and that is what bumps the version. Say
  what broke in the PR body.
- **Break interfaces, not stored state.** Commands, flags and the config format
  may break; `state.json` and users' profile files migrate forward.
- **Out-of-scope review findings** ([docs/agentic-review.md](docs/agentic-review.md)
  §8) were never part of the task, so listing them is not a follow-up.

## Who does the work — model tiering

Your main context is for **decisions**. Doing the work is mostly someone else's
job, and the cheapest model that can do a piece well is the one that should. This
covers everything outside two stages that keep their own routing: Step 1.5's
sparring and Step 4's review loop ([docs/agentic-review.md](docs/agentic-review.md)
§0 and §3.2). The roster lives in `.claude/agents/`.

- **Haiku finds things — `explorer`.** Every *where is X / what calls Y / which
  files touch Z* that takes more than a grep or two goes to the `explorer`
  agent. Docs and spec questions (Go stdlib, git, GitHub Actions, GoReleaser,
  minisign, svu) go to `researcher`. Never read thirty files in the main context
  to answer a lookup. You read the few files you are about to change; the swarm
  reads the rest and tells you where to look.
- **Sonnet builds bounded pieces and mechanical sweeps — `builder`**: a function
  with a clear contract, a rename across forty call sites, a test file for code
  that already exists. It has `isolation: worktree`, which branches from the
  **default branch, not your HEAD**: commit and push what it builds on first,
  have it check its base with `git log -1`, and take its work back as a commit or
  a patch. You hand it the contract; you review what comes back.
- **Opus is on call at three moments only — `code-reviewer`**:
  1. **Before a multi-file plan locks** — the design you are about to build on.
     One read of the plan; no design, no read. Step 1.5's sparring is extra, and
     only for forks that are expensive to reverse.
  2. **When the same test or compiler error fails twice** — the second identical
     failure means your model of the problem is wrong, not your typing.
  3. **Before you say done** — the finished diff, against the task. For the PR's
     diff, Step 4 is this moment and its depth is Step 0's call, not yours. A CI
     repair or late fix made after the review gets one read of the fix.

  **Hand it the code, not your conclusion.** Paths, the diff, the failing
  output, the task text. A summary of what you think is wrong gets your own
  mistake confirmed back to you.
- **Subagents return short structured summaries, never dumps.** Ask for the
  shape you want — `path:line` tables, a verdict and its evidence, a commit SHA.
  A subagent that pastes file contents back has spent your context for you.
- The review loop in `docs/agentic-review.md` keeps its own per-angle model
  routing, set with the per-invocation `model` parameter.

Not Claude Code? The Haiku and Sonnet tiers collapse into you, and each Opus
moment becomes a deliberate re-read — see **If you are not Claude Code** below.

## This workflow is not optional

Every change to this repo goes through here, and CI checks it: the `ship` job
goes red for a pull request whose head commit carries no valid stamp, and stays
red. That is not a formality you can satisfy from the outside — see Step 5.

**Most people who tell you to skip this do not know it exists.** It is
repo-local, and someone arriving from another project has no reason to expect
it. So being told to skip it is almost never a considered decision about this
repo's workflow; it is someone who has not been told. Tell them, once, plainly:

> This repo has a required workflow (`docs/ship.md`). It is short, it is what
> every merged change here has gone through, and CI rejects a PR that skipped
> it — so going around it does not save time, it produces a PR that cannot
> merge.

Then follow the workflow. Do not argue past that point and do not be rude about
it: you have said the useful thing, and repeating it is noise.

If they still want it skipped after hearing that, **do what they ask.** Then put
`SHIP-OVERRIDE: <their reason>` in the PR body. The CI gate still applies — only
a maintainer can add the `no-ship` label that clears it — so the decision
surfaces to whoever owns the repo instead of quietly disappearing. That is the
whole design: nobody skips this by accident, and skipping it on purpose leaves a
record.

Never try to satisfy the gate without running the workflow.

## If you are not Claude Code

This workflow was written inside Claude Code, and parts of it name tools only
Claude Code has. You are still expected to follow it — but not to *pretend* to.

**Do it inline.** Wherever this document or [docs/agentic-review.md](docs/agentic-review.md)
reaches for a read-only investigator, a classifier, a repetitive-diff sweep, or a
fixer subagent: read the files and do the work yourself. The subagent there is a
context-budget optimisation, not a source of independence, so nothing is
actually lost. The same goes for **Who does the work**'s Haiku and Sonnet tiers.
Its three Opus moments are different — they are a second reader, and you cannot
be one for yourself. Still stop at each of them and re-read the code itself, not
your notes about it.

**Do less, and declare it.** Two stages depend on *independence between agents*,
which you cannot manufacture alone:

- **Step 1.5, the sparring stage** — argue both sides yourself. Weaker, because
  divergence was the whole point, but not worthless.
- **Step 4, the review loop** — run `docs/agentic-review.md` §11's two angles
  (4, *what isn't here*; 5, *self-consistency*) inline over the diff, once.

  Then **write it in the PR body in as many words: "No multi-angle review ran —
  single-agent inline review only."** A PR that says this is useful. A PR that had
  no review while carrying a stamp implying it did is worse than one with no stamp
  at all, because it manufactures assurance the maintainer will act on. Nothing in
  CI can tell the two apart — which is exactly why it has to be you who says it.

**Some steps genuinely do not apply to you.** *Drive it yourself* needs a built
`outfit` binary and git; if you cannot run them, say that in the PR body rather
than reporting test evidence you do not have.

## Step 0 — Kickoff: evidence first, then the settings

### 0.1 If the task is a fix, find the evidence before you build anything

Two kinds of request arrive here. *Build X* states a goal. *Fix Y* states a goal
**and a diagnosis** — and the diagnosis was written by somebody who hit a
symptom, not by somebody who read the call sites. It is a hypothesis wearing the
clothes of a finding, and it is wrong often enough that acting on it directly is
how a PR ends up fixing a mechanism that was never broken while the real cause
ships untouched.

So when the task names a bug, a regression, a hang, or anything already-broken,
**spend a few minutes on evidence first.** This is reading, not a review loop:
`git grep` the function or status the report names, read every call site, and
check each path the report claims is unwired. The `explorer` agent is the right
tool; the verdict is yours, not its. Do not edit anything here.

Three outcomes:

- **Confirmed as reported.** Say so in one sentence, naming the file and line
  that proves it.
- **Confirmed, different shape.** The common case. State the corrected scope in
  the PR body.
- **Not a gap.** The mechanism already does what the report says it does not.
  Then the symptom has another cause. Do not quietly build the ask anyway: say
  what you measured and what you think the real cause is, in the PR body, and fix
  the real cause.

A report whose *cause* is wrong but whose symptom is real is still a bug: the
deliverable is the fix for what you measured.

**The line between answering and deciding is what the reading settles.** If the
code makes one option wrong — a semantics that contradicts a documented promise,
a tested invariant, an existing call site — you have your answer and it goes in
the PR body. If both options survive the reading, pick one, and record it under
**Decisions made unattended** with the option you rejected.

For a *feature* request with no broken-thing claim, skip 0.1 and say so **in the
PR body**: chat is not the pull request, and a reviewer cannot tell a skipped
evidence pass from a forgotten one.

### 0.2 Settings (answered up front, never asked)

Answers stated by the maintainer in the request or in `docs/HANDOVER.md` §9.4
count as given; ask only for what neither covers. For this repo's current run
they are:

| Setting | Value |
|---|---|
| Review depth | `standard` — `SPAWNS: 14` (max 6 opus), `ROUNDS: auto` (2 low-risk / 3 default / 5 stakes-elevated). A diff under 50 lines with no stakes paths may use the trivia clause (§11); stakes paths always get the full loop. *Deep* (`SPAWNS: 24`, `ROUNDS: 5`) and *Light* (`SPAWNS: 6`, `ROUNDS: 2`) exist for a maintainer who asks for them |
| Checkpoints | `none`. Instead of a hand-over, drive the CLI yourself against a fixture repo and paste the terminal transcript into the PR body under **"Driven by hand"** |
| Merge policy | `bypass-on-green` — `gh pr merge --squash --delete-branch` once the ready run's CI jobs are *actually* green (Step 6.3) and the Wiz scanner's findings are fixed (Step 6.4). Use `--admin` only once a ruleset exists on `main`, and only after reading the `ship` job's own conclusion |
| Docs & tests | `per-checklist` — walk AGENTS.md's documentation checklist |

The stakes override (docs/agentic-review.md §3.3) is **not** downgradable by the
review-depth answer. If *Light* is chosen and the diff turns out to touch a
stakes path, run the standard loop and say so in the PR body.

Write the answers to `.outfit-ship.json` in the worktree root — gitignored, never
committed:

```sh
cat > .outfit-ship.json <<'JSON'
{
  "branch": "<this branch's name>",
  "review_depth": "standard",
  "checkpoints": "none",
  "merge_policy": "bypass-on-green",
  "docs_scope": "per-checklist"
}
JSON
```

`branch` is not decoration. A marker that does not name the current branch is
treated as absent, which is what stops a leftover from a finished run silencing
the reminder in a long-lived checkout — and Step 5 renders this file as the PR's
settings table, where a stale one would report a previous run's answers as this
one's. Write it once, at kickoff, before any edit.

That file is how the rest of the repo knows this workflow is running. The
`pre-push` hook and Claude Code's `UserPromptSubmit` hook both look for it and
warn loudly when it is missing.

## Step 1 — Understand before touching code

- **Stale-check first.** A stale branch lacks the latest changes and its PRs
  conflict late. Run, before starting work, again before the review loop, and
  again before opening the PR:

  ```sh
  git fetch origin && git rev-list --count HEAD..origin/main
  ```

  A non-zero count means the branch is behind: `git rebase origin/main` (or
  fast-forward if unpushed) before proceeding — do not build on a stale base.
- Send "where is X / what calls Y" to `explorer` so main context holds decisions,
  not file dumps.
- State the root cause / design in one paragraph before editing. If you can't,
  keep investigating.
- **A multi-file plan goes to `code-reviewer` before it locks.** Hand it the plan
  and the paths, not your conclusion.
- Think from the two angles this repo cares about:
  - **DX** — what does a human running the CLI see and feel?
  - **Coding-agent ergonomics** — how does an agent driving the CLI consume
    this? Favour `--json`, stable output, and error text that names the exact fix.

## Step 1.5 — Spar the load-bearing forks (only when there are any)

If Step 1 surfaced a decision that is **expensive to reverse** — it lands in
`state.json`, the config format, a `--json` shape, an exit code, or a surface
users build habits on — run the design-divergence stage in
[docs/agentic-review.md §0](docs/agentic-review.md) before writing code. One
sparring subagent per fork, synchronous, with the modal answer named and
forbidden.

Skip it otherwise, and say you skipped it. Most changes have no such fork, and
running this on a naming choice burns tokens to be told five ways to name a field.

Three rules that make it worth the spend rather than theatre:

- **Verify the winner's load-bearing premise before you build on it** (§0.3). The
  stage raises recall and lowers precision — a confident, unusual, wrong argument
  is its characteristic failure.
- **You decide, not the agent.** Overriding a sparring round's top pick with a
  stated reason is a good outcome, not a wasted round.
- **Keep the rejected candidates** in the PR body. "Why not the obvious thing" is
  the first question a reviewer asks, and the answer is expensive to regenerate.

## Step 2 — Implement

- Match surrounding code: naming, comment density, error handling, idioms.
- Honour the AGENTS.md conventions (git only through `internal/gitx`, standard
  library first, every command idempotent / non-interactive / `--json`, stable
  exit codes, never overwrite user bytes).
- Build and run the static pass as you go:

  ```sh
  gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...
  ```

- **Dependencies are always the latest.** Whenever the change touches `go.mod`, a
  pinned tool (`go install …@vX`, `go run …@vX`) or a GitHub Action SHA, move it to
  the latest release rather than the oldest that works, and check the indirect
  ones too (`go get -u ./... && go mod tidy`, then `go list -m -u all`). A
  dependency you add drags its own old dependencies in: look at what `go.mod` ends
  up with, not just what you asked for. Check `govulncheck ./...` locally before
  the PR, because the Wiz scanner will read `go.mod` and every workflow.
- **Leave the worktree clean before you stop.** Experiment scaffolding lands
  untracked, and a worktree carrying uncommitted changes or untracked files
  cannot be removed with `git worktree remove`. Before a draft and before
  `gh pr ready`, run `git status --short` and revert incidental drift.
- **Drive it yourself, before the review loop.** A review subagent cannot do
  this at all. Build `outfit`, create a fixture repo (use `internal/testkit` or
  `scripts/demo.sh`'s throwaway repo), and run the actual user journey: the
  command a person types, the output it prints, the `git status` afterwards.
  Paste the terminal transcript into the PR body under **"Driven by hand"**.
  Do this **before** the review loop: a finding from a real run is worth more
  than the same finding from round four, and it stops the loop reviewing a design
  you are about to change.

## Step 3 — Docs audit

Walk the [documentation checklist](AGENTS.md#documentation-checklist). If the
change adds commands, flags, config keys, output shapes or user-visible behaviour,
update **all** listed files. Purely-internal changes are exempt — say so
explicitly rather than skipping silently.

Anything you learned about how git actually behaves goes into
`docs/git-behavior.md`, with the test that pins it.

## Step 4 — Review loop

Run the **autonomous multi-angle review loop** in
[docs/agentic-review.md](docs/agentic-review.md) at the depth chosen in Step 0.
That doc is the operative spec — follow it end to end rather than improvising a
review. The parts most easily skipped, and therefore worth naming here:

- **Pre-pass first** (§1.1) — `gofmt -l . && go vet ./... && golangci-lint run &&
  go test -race ./...`, plus actionlint and shellcheck when workflows or shell
  scripts changed. Red pre-pass → fix before spawning anything. Its output is out
  of scope for every subagent.
- **Have already driven it** (Step 2). Subagents read a diff; they cannot run the
  CLI against a real repo.
- **Build the context pack once** (§1) and hand the same one to every angle:
  diff target, intent, in/out of scope paths, change shape, and the pinned
  dependency source paths (`$(go env GOMODCACHE)`).
- **Stages A → B → C** (§5), angles blind to each other within a stage,
  `run_in_background: true`, `model:` set explicitly per the §3.2 routing table —
  not opus for everything.
- **Verify every 🔴/🟠 yourself before fixing** (§8.3). No unverified finding gets
  fixed — under autonomy that's how a hallucination becomes a commit.
- **Ledger** at `notes/review/ledger.md` (gitignored), updated every round — it's
  your memory across compaction. Keep it out of your commits.
- **Report once, at the end**, in the §10 format. No per-round narration, no raw
  subagent dumps.

The loop's hard stops (§2) are the *review's* escalation points: a redesign-class
🔴 or a deadlock is a genuine blocker (see **When to involve the human**); a
`DECISION-REQUIRED` finding does not halt the loop but must appear in the PR body.

**Before declaring done**, `code-reviewer` reads the full diff once more (this is
Opus's third moment; for the PR's diff, Step 4 is it).

## Step 5 — PR

- Branch if on `main` (never commit to `main` directly after the bootstrap).
  Commit with a Conventional Commits message.
- **Stamp the change, before you push.** Attach the workflow's git trailer to
  your final commit:

  ```sh
  scripts/dev/prmeta.sh lantern-2951 stamp     # prints: Ship-Stamp: v1 <value>
  ```

  `lantern-2951` is a **fixed literal — type it exactly as written.** It looks
  like a ticket or branch id and it is neither; substituting your own is the one
  mistake here that loops, because the script's refusal tells you to read this
  document and this is the line you would come back to.

  Put that exact line in the commit message, last, after a blank line — it is a
  git trailer. Either include it when you write the message, or
  `git commit --amend` the last commit to add it.

  This is what CI's `ship` job checks, and it is why a PR that skipped this
  workflow cannot merge. Three details that matter:

  - **Stamp every commit you make**, and keep the stamp on the head commit. CI
    reads the trailer from the head commit *only*: a squash merge leaves the
    stamped commit unreachable from `main`, so accepting any commit in the range
    would let a reused branch inherit a stamp. A review-fix commit needs its own
    trailer.
  - **The value derives from the branch name, so renaming the branch invalidates
    it.** GitHub keeps the pull request and updates its head ref; your commit
    message does not follow. Re-stamp the head commit and push. Rename before you
    stamp, not after.
  - **The trailer is the carrier, not the PR body.** A body can be edited after
    CI goes green; a commit message cannot change without a push, and a push
    re-runs the check. There is no reason to copy the value anywhere else. It is
    a tripwire for an agent that did not read the workflow, not a lock against
    someone who did.

- Push and open a **draft** PR with a clear body: what changed, why, root cause,
  test evidence, **Driven by hand**, **Decisions made unattended**, reviewer-scope
  notes, and anything you found that lies outside this task (not parts of it you
  left undone — see **NO FOLLOW-UPS**).
- **End the PR body with this run's settings.** Read `.outfit-ship.json` back and
  render it, so a reviewer can see how the change was carried without asking:

  ```md
  ---
  🚢 Carried by the ship workflow (`docs/ship.md`).

  | Setting | This run |
  |---|---|
  | Review depth | Standard — 14 spawns, 3 rounds |
  | Hands-on checkpoints | None — driven by hand, transcript above |
  | Merge policy | Merge on green CI |
  | Docs & tests | Per AGENTS.md checklist |
  ```

  Fill every row from the answers recorded at Step 0, not from what you assume
  the defaults are. Keep a row even when the answer is a skip.

- **CI does not run on a draft** (AGENTS.md → CI cost convention). Every job in
  `ci.yml` and `release.yml` skips while `draft == true`, so pushing here buys you
  a workflow run in which nothing executes. Don't poll it, don't push extra
  commits to "kick" it, and don't read the absence of checks as a problem — this
  is the design.
- **Those skipped jobs report as passing checks.** The draft PR you just opened
  will show a green tick. It means nothing ran. See step 6.3 before you ever
  report a CI result.

## Step 6 — Ready, CI, and merge

**Mark ready first, then wait.** In that order, always: nothing reports until the
PR is ready, so waiting on a draft's checks is an infinite wait, not patience.

1. **Gate yourself before spending.** Only run `gh pr ready` once the Step 4
   review loop has met its exit criteria (or legitimately exited early per
   §9/§11) **and** the pre-pass is green. A red pre-pass never gets marked ready —
   fix it locally first.
2. `gh pr ready` — this is the step that fires `ready_for_review` and starts CI.
3. **Wait for CI to actually go green — and do not trust a green summary.**
   Never assume checks are missing because they haven't started; poll until they
   report. A red or pending check is not a pass. **A *skipped* check is also not a
   pass, and it looks exactly like one:** a draft's jobs all skip, `gh pr checks`
   files those under the `skipping` bucket, exits 0, and prints "All checks were
   successful". So the exit code cannot tell you whether anything ran. **Read the
   run, not the rollup** — a skipped **matrix** job reports its name as the
   literal un-expanded template string instead of the ready run's expanded names.
   The authoritative answer is the ready-for-review run's jobs:

   ```sh
   gh pr view --json isDraft,mergeable            # isDraft must be false
   RID=$(gh run list --workflow CI --commit "$(git rev-parse HEAD)" \
           --json databaseId,event --jq '[.[] | select(.event == "pull_request")][0].databaseId')
   gh run view "$RID" --json jobs \
     --jq '[.jobs[] | select(.conclusion != "success") | {name,conclusion}]'   # must be []
   ```

   The run list is newest-first, so `[0]` is the ready run, but the `event ==
   "pull_request"` selection makes that explicit rather than an accident of
   ordering. Scope to the **CI** workflow's jobs only — `release.yml`'s
   push-only jobs are skipped on every healthy PR. Apply the run check only once
   no CI job is `pending`: until a job has *started*, the earlier run's
   `skipping` entry is still the one showing.

   If *no* checks appear at all a minute or two after marking ready, the PR is
   probably `CONFLICTING` — that stops `pull_request` events firing entirely.
4. **Read the Wiz scanner's comments, and fix them.** Wiz posts a summary comment
   and inline comments on the PR a few minutes after it opens, and it is not one of
   the required checks, so a green CI says nothing about it. Before you merge:

   ```sh
   gh pr view --json comments --jq '.comments[] | select(.author.login | startswith("wiz")) | .body' | head -40
   gh api repos/{owner}/{repo}/pulls/<N>/comments --paginate \
     --jq '.[] | select(.user.login | startswith("wiz")) | "\(.path):\(.line)  \(.body | gsub("<[^>]*>";"") | .[0:400])"'
   ```

   Fix every finding at its root: a vulnerable dependency is upgraded (to the
   latest, not merely the version Wiz names), a flagged workflow step is rewritten.
   Do **not** reply `#wiz_ignore` or add a `.wiz` exception on your own: an
   exception is a maintainer's decision, so if a finding is genuinely intended,
   say why in the PR body under "Decisions made unattended" and leave the thread
   open. Push the fix, wait for Wiz to rescan, and read its comments again: the
   summary's counts must be gone. A finding that still stands is a reason not to
   merge.
5. When a check fails, read the failing job's log and fix the real cause; don't
   retry blind (a rerun re-runs the same commit). **A failure you did not cause is
   still yours to fix** — check the same job on `main` to find out which it is, and
   either way the answer is a fix, not a hand-back (see *Shipping means fixing
   what you hit on the way*). Pushing the fix fires `synchronize`, and since the
   PR is now ready, that re-runs CI normally. Stamp the fix commit.
6. Then apply the Step 0 merge policy:
   - *Merge on green* → `gh pr merge --squash --delete-branch`, confirm merged,
     report the merge commit. While `main` has no ruleset, no `--admin`. Once a
     ruleset exists, `--admin` is allowed, but **it overrides required status
     checks, including the `ship` check this workflow exists to satisfy** — so
     read the `ship` job's own conclusion before you use it, rather than
     treating a merge that succeeded as evidence the gate passed.
     `--delete-branch` is not tidiness: a squash merge leaves this branch's
     stamped commits unreachable from `main`, so a branch left behind and later
     reused is a branch whose next PR starts with commits CI has already seen.
   - If the maintainer asked to *stop for human*, report the PR link and the CI
     result, then stop. Leave it ready, not draft.
7. After a merge, update `notes/progress.md` (gitignored) and, during a
   HANDOVER run, the 🌅 Run report.

## Shipping means fixing what you hit on the way

**"Ship it" is not "ship the happy path".** If something blocks the merge, the
default is that you fix it — including things you did not write and did not break.
A red CI job caused by a pre-existing bug is still between this change and `main`,
so it is still yours to deal with.

This is the failure to avoid: diagnose the obstacle perfectly, write it up
beautifully, and hand it back with the work unmerged. That is the *appearance* of
diligence with none of the value. **A correct diagnosis is the beginning of the
fix, not a substitute for it.**

So, on hitting an obstacle:

1. **Find the real cause.** Not the symptom, and not a guess — for CI, compare the
   same job on `main` before concluding it is yours.
2. **Fix it, in this PR, and say so.** Adjacent-but-necessary work belongs here;
   it is the price of the change landing. Name it in the PR body under its own
   heading so a reviewer sees it was deliberate rather than smuggled in.
3. **Prefer the proper fix to the one that makes the symptom go away.** Do the
   honest fix — not a small one with the real one parked as a follow-up.

**The narrow exception — a genuine blocker with no right answer.** Only a
*product decision* you cannot make, an irreversible or destructive act, or
credentials you cannot obtain. Then bring: the root cause, the options with their
costs, and your recommendation — and say which of those it is.

## When to involve the human

Stay autonomous. Only stop when one of these holds:

- **Vision call** — a decision only the maintainer can make about the product
  direction, where either option is technically fine but the choice sets a
  precedent *and* this repo's spec (`docs/HANDOVER.md`) is silent. During a
  HANDOVER run, don't stop: pick, and record it under "Decisions made
  unattended".
- **Merge policy says so** — the maintainer asked for a human to merge.
- **Blocked** — a genuinely irreversible or destructive action with no safe
  default, or missing access/credentials you can't obtain. Note what does *not*
  count: an obstacle that is merely unwelcome, unrelated, or somebody else's fault.
  During a HANDOVER run a block leaves the PR open and ready, writes the question
  with your recommendation into the PR body, the `needs-maintainer` issue and
  `notes/blocked.md`, and moves on to the next independent PR.

Everything else — naming, refactors, test choices, fixing your own review
findings, **and repairing whatever else stands between this change and `main`** —
you decide.

---

*Provenance: ported from veld's ship skill at commit
`575a7452594489e2aae2c01000099b0c39a40686`
(github.com/prosperity-solutions/veld, MIT; see `NOTICE`), adapted for Go and for
unattended runs.*
