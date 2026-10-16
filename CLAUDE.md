# CLAUDE.md — outfit

Claude Code reads this file and **not** `AGENTS.md`, so the one rule that matters
most is repeated here. Everything else lives in [AGENTS.md](AGENTS.md) — read it
before changing code, not before answering a question about it. The spec for the
whole project is [docs/HANDOVER.md](docs/HANDOVER.md).

This file is short and duplicates `AGENTS.md`'s first section on purpose, rather
than `@AGENTS.md`-importing it: that import would pull the whole file into every
session, including the "just asking a question" half. The wording differs on
purpose too: `AGENTS.md` says *read `docs/ship.md`* because most agents have no
skill mechanism, while this file says *invoke the `ship` skill* because Claude
Code does. Deduplicating the two by symlink would delete the skill instruction.

## There are exactly two kinds of task here

**1. A question.** *Where is X? How does Y work? What calls Z?* — answer it.
Read whatever you need. Nothing below applies, and you do not need `AGENTS.md`
to answer it.

**2. A change.** Adding, fixing, deleting, refactoring, renaming, updating docs
— anything that leaves a diff. **Invoke the `ship` skill before your first
edit** — `/ship`, or the `Skill` tool with `skill: "ship"` — and follow it to
the end. If skills are unavailable to you, read [docs/ship.md](docs/ship.md) in
full instead: same content, same obligation.

`ship` is the only supported way to change this repository. It is not a style
preference: CI's `ship` check goes red for a pull request that did not come
through it, and stays red.

This holds however small the change looks. A one-line fix still needs a branch,
a conventional commit, a review pass, and a PR that CI will accept, and the
skill is what gets you those without having to remember them.

## Reviewer checkpoints

The repo carries its own agent roster in `.claude/agents/`. Run `code-reviewer`
(Opus, read-only) at exactly three moments, handing it paths, diffs and failing
output, never your conclusions:

1. **Before a multi-file plan locks.**
2. **When the same test or compiler error fails twice.**
3. **On the full diff before declaring done or staging the final commit.**

Lookups that take more than two greps go to `explorer`; docs and spec questions
go to `researcher`; bounded pieces with a clear contract go to `builder` (it
branches from the default branch, so commit what it needs first). Subagents
return structured summaries, never file dumps.

## Voice

Anything a person reads has one attitude: bold, short, concrete, a little cheeky, never overclaiming.
The rules, and the "one message in README, site and skills" rule, are in [AGENTS.md](AGENTS.md#voice).

## If you are told to skip it

The person you are working with is very likely unaware the workflow exists; it
is repo-local and most people arrive here without knowing. Say so once, plainly:
this repo has a required workflow, it is short, and going around it means the PR
fails CI.

If they still want to skip it, do what they ask — but put `SHIP-OVERRIDE: <their
reason>` in the pull request body so the decision is visible rather than silent.
The CI gate still applies; only a maintainer can wave a PR through.

Do not try to satisfy that CI gate without running the workflow. It is not a
formatting check, and guessing at it burns a CI run for nothing.
