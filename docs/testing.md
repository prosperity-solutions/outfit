# Testing

How outfit is tested, and where each promise in the spec is pinned. The harness is
`internal/testkit`: a hermetic environment (own `HOME`, no ambient git config, fixed dates
and locale), a fixture factory that builds repositories from one `git fast-import` stream
and caches them per git version, and the testscript commands (`outfit`, `fixture`,
`atomicsave`, `islink`, `gitcalls`, `hooklog`, …) used by `testdata/script/*.txtar`.

| Kind | Where | Run it |
|---|---|---|
| Unit | `internal/*/…_test.go` | `go test -race -short ./...` |
| Scenarios (real git, real outfit) | `testdata/script/*.txtar` through `internal/e2e` | `go test ./internal/e2e -run TestScripts/<name>` |
| Properties (random sequences) | `internal/e2e/property_test.go` | `go test -count=1 ./internal/e2e -run TestProperties` |
| Fault injection | `internal/e2e/fault_test.go` | `go test -count=1 ./internal/e2e -run TestFaultInjection` |
| Fuzz targets | `FuzzParseFile`, `FuzzNormalizeKey` (config), `FuzzSparse`, `FuzzPlan` (plan), `FuzzExtractBinary`, `FuzzExtractBinaryEntries` (update) | `scripts/dev/fuzz.sh [seconds]` |
| Performance, hard | `gitcalls` in `testdata/script/perf-gates.txtar` (and the budgets in `swaps-basic`, `default-variant`) | part of the scenarios, on every OS |
| Performance, soft | `scripts/ci/perf-soft.sh` (hyperfine, Linux) | `scripts/ci/perf-soft.sh` |

The git versions in CI are the floor (2.36), the latest, and Apple's. Run a scenario against
an older git by putting it first on `PATH`.

## The scenarios (docs/HANDOVER.md §7.4)

Data loss first. Every row names where it is pinned.

| # | Scenario | Pinned by |
|---|---|---|
| 1 | An atomic-rename save is absorbed byte-identically and the path relinked | `swaps-absorb.txtar` |
| 2 | File and variant both changed: both quarantined, exit 3 | `swaps-absorb.txtar` |
| 3 | Conflict markers are never absorbed; exit 3 | `swaps-conflict.txtar` |
| 4 | A clean pull that changes the base gives a stale status (exit 4), variant untouched | `swaps-stale.txtar` |
| 5 | Nothing stages a swapped path or mode 120000 (`commit -a`, `add -A`, `add --sparse`, sparse off) | `swaps-safety.txtar`, `hooks-guard.txtar` |
| 6 | `reset --hard`, `checkout -f`, `clean -fdx`, `stash -u`/`pop`, `rebase -i` never destroy variant bytes | `swaps-safety.txtar` |
| 7 | Killed at every injected fault point and rerun, sync converges with nothing lost | `TestFaultInjection`, `swaps-stray.txtar` |
| 8 | 10 parallel syncs across a worktree farm are serialised and end valid | `swaps-parallel.txtar` |
| 9 | `worktree add` gets its links, the copied `outfit.profile` is stripped, `use --here` still overrides | `swaps-worktree-add.txtar` |
| 10 | `uninstall` restores identical `git config --list`, sparse state and tree | `swaps-uninstall.txtar`, `hooks-lifecycle.txtar` |
| 11 | Husky, lefthook, plain hooks: args, stdin and exit codes pass through; a husky hooksPath reset does not win; shims run with the binary deleted | `hooks-husky.txtar`, `hooks-plain-lefthook.txtar`, `hooks-binary-missing.txtar`, `hooks-moved.txtar` |
| 12 | A malicious committed `.outfit/config` is ignored with a warning, nothing executes | `malicious-config.txtar`, `FuzzParseFile` |
| 13 | A `locked` key cannot be overridden from any scope or profile | `toggles.txtar` |
| 14 | A foreign sparse checkout is refused; a commit in a linked worktree is still guarded | `swaps-refusals.txtar`, `swaps-paths.txtar` |
| 15 | Spaces, NFD Unicode, case-insensitive APFS, a subdirectory whose only file was swapped | `swaps-paths.txtar` (spaces, NFC names, NFD names on Linux only (macOS normalises them), a profile file of the wrong case on case-insensitive APFS and elsewhere, the single-file subdirectory; the macOS CI legs run the script on APFS) |
| 16 | A variant missing at HEAD is unswapped: no dangling link, no loop | `swaps-missing-variant.txtar` |
| 17 | A failing sync inside `post-checkout` does not change git's exit code | `swaps-hooks.txtar` |
| 18 | Inactive profiles are not on disk, `rg --hidden` finds nothing under `.outfit/`, `author on/off` round-trips | `swaps-uninstall.txtar`, `swaps-basic.txtar` |
| 19 | Local and worktree profiles: `new`, edits, `promote`, `worktree remove` deletes worktree profiles, name clashes refused | `profiles-local.txtar` |
| 20 | `use` with uncommitted edits in the active committed variant is refused, nothing half-switched | `swaps-refusals.txtar` |
| 21 | The default variant (§3.0): a clone with `.outfit/` committed, on a machine without outfit | `default-variant.txtar` |

`TestScenarioMapIsReal` fails when a row above names a file or test that does not exist, or
when a scenario number goes missing.

## Properties

`TestProperties` draws random sequences of `use`, `set`, `new`, an edit through a link, an
editor's atomic save, an upstream pull (docs or the base file; a rebase when a local commit
rules out a fast-forward), a conflicting merge, `worktree add`/`remove`, `stash`/`pop`, a
forced rebase, `commit -A` and `sync`, against a fixture with an upstream. A step that does
not apply (an edit with nothing swapped) is a logged no-op, not a discarded sequence. It
checks after **every** step:

- no user byte is lost (every token written is still in a worktree, outfit's own
  directories, a stash or a commit);
- no symlink is committed or staged, and no swapped path is staged;
- a clean status stays clean after the commands that must not dirty it (not while an edit is
  pending: from an edit or an editor's save until the tree is clean again);
- no command ends in an internal error (exit 1);

and at the end that a second `sync` changes nothing: no write, no mtime of the swapped paths,
the profiles or outfit's own files, in the main worktree and in every linked one.
`OUTFIT_PROPERTY_CHECKS` (default 8) sets how many sequences run. A failure prints the
invariant that broke, a dump of the tree (status, the swapped paths and where they point,
`state.json`, the quarantine) and every step with the token it wrote. To replay it copy the
`-rapid.seed=<n>` rapid prints into
`go test -count=1 ./internal/e2e -run TestProperties -rapid.seed=<n>` (the `-rapid.failfile`
it also prints exists only on the machine that ran it; rapid replays a leftover failfile from
the git-ignored `internal/e2e/testdata/rapid/` until you delete it). Every sequence starts with `use yolo`
and fails at once if the model then finds no swapped path (so a model that stopped reading
`state.json` cannot pass by testing nothing); a run of 30 or more sequences must also have
executed edit steps (a short run may simply not draw one). A failing sequence is worth turning into a
testscript line next to the scenario it belongs to, as `swaps-refusals.txtar` did for the
first one this test found.

## Fault injection

A binary built with `-tags faultinject` has numbered kill points (`internal/fault`): in the
sync code (before each step, between a link's creation and its rename, after the sparse
patterns are written, before the state is written, in the teardown), in every atomic file
write (`state.WriteFileAtomic`, between the temporary file and its rename), in a profile copy
and in `promote` (before the rename, before the local copy is removed), and in `init` and
`uninstall` (between their phases). With `OUTFIT_FAULT_AT=N` the process dies by SIGKILL at
the Nth point; `OUTFIT_FAULT_LOG=<file>` lists them. `TestFaultInjection` kills fourteen
operations (use, switch, back to base, absorb, quarantine, `author on/off`, use of a local
profile, the same from a symlinked spelling of the path, `promote`, `init`, `uninstall`,
uninstall in a worktree farm, a killed `use` followed by `uninstall`) at **every** point each has, in parallel, then reruns `sync` (or
`uninstall`) twice and checks that both runs simply worked (exit 0, except the one scenario
that ends in "a person must look"), nothing was lost, no temporary link or untracked file is
left behind, the profile on disk is the profile in config, and an uninstall (or the undo of a
killed `init`) restores `git config --list` exactly. In a normal build `fault.Point` is an
empty function.

It found five bugs in its first runs, all fixed with a test that pins them: an `uninstall`
that could never finish after a kill past the point where sparse checkout was switched off
(a clean base file is now recognised as already restored), settings nothing remembered (the
intent is now written before sparse checkout is turned on), links nothing remembered
(uninstall finds them by their skip-worktree bit and target), a temporary link beside the
target (it now lives in the git dir; `swaps-stray.txtar` covers sweeping the leftover of the
fallback used for a git dir on another filesystem, `TestLinkBesideFallback` the fallback
itself), and temporary files of a killed write in outfit's hooks directory.

A failing kill names its point and prints how to rerun just that one, for example
`go test -count=1 ./internal/e2e -run '^TestFaultInjection$/^uninstall$/^kill-at-04-teardown-sparse-checkout-is-off$'`,
together with the numbered list of the operation's points.

What a kill cannot reach: the places outside the code above that change things (the git
config writes of `use`, `set` and `author`, the agents' settings file), and torn writes
(SIGKILL stops the process between system calls, it does not tear one).

## Adding coverage

- **A new place that changes the worktree, the git dir or the config** (a rename, a remove,
  a symlink, a config write): put a `fault.Point("<what has happened when it is reached>")`
  right before it, with a name of its own (for the state of the tree when it is reached, or what comes next). Points are counted per process in the order they
  are reached (a point in a loop counts once per round), so inserting one renumbers the later
  ones: the failing test's name carries the point's name, not just its number. An existing
  operation picks a new point up by itself.
- **A new operation** (a command that changes things): add a `faultScenario` in
  `faultScenarios()` (setup, the killed `op`, what to `recover` with) and a `step` in
  `steps()` of `property_test.go` (a step that does not apply must log and return, never
  skip). Nothing fails when an operation is missing: look for `os.Rename`, `os.Remove`,
  `os.Symlink` and `config` writes in your diff and ask which test kills them.
- **A bug found by a random test**: reduce it to a testscript line next to the scenario it
  belongs to (`swaps-refusals.txtar` and `swaps-safety.txtar` have two) and keep the
  fuzz input under `testdata/fuzz/<Target>/`.

## Fuzzing

The seed corpora run as ordinary tests. `scripts/dev/fuzz.sh [seconds]` runs each target for
a while; the `fuzz` CI job runs them for 20 s each. A crash writes its input to
`testdata/fuzz/<Target>/`: commit it, it becomes a regression test (CI uploads it as an
artifact). `FuzzPlan` found, in its first minute, a conflicted path the plan would have
quarantined and removed.

## CI

`all-green` needs every job except two, which are deliberately outside it (a deviation from
§8.1's "every job"): `fuzz` (it looks for NEW inputs, so it can fail on old code in an
unrelated pull request) and `perf` (soft, Linux only, `continue-on-error`). A failing `fuzz` is a
red job and a red workflow run; a failing `perf` only raises a `::warning::` annotation
(`continue-on-error`). The merge policy reads every job, but the branch ruleset PR 11 adds
requires only `ship`, `commits` and `all-green`.
