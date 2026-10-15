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
| Properties (random sequences) | `internal/e2e/property_test.go` | `go test ./internal/e2e -run TestProperties` |
| Fault injection | `internal/e2e/fault_test.go` | `go test ./internal/e2e -run TestFaultInjection` |
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
| 15 | Spaces, NFD Unicode, case-insensitive APFS, a subdirectory whose only file was swapped | `swaps-paths.txtar` (spaces, NFC and, off macOS, NFD names, a profile file of the wrong case, the single-file subdirectory; the macOS CI legs run the same script on case-insensitive APFS) |
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
`OUTFIT_PROPERTY_CHECKS` (default 8) sets how many sequences run; `-rapid.seed=<n>` replays a
failure (rapid prints the seed and the steps; it also writes the failing sequence to
`testdata/rapid/`, which is git-ignored). A failing sequence is worth turning into a
testscript line next to the scenario it belongs to, as `swaps-refusals.txtar` did for the
first one this test found.

## Fault injection

A binary built with `-tags faultinject` has numbered kill points (`internal/fault`): in the
sync code (before each step, between a link's creation and its rename, after the sparse
patterns are written, before the state is written, in the teardown), in every atomic file
write (`state.WriteFileAtomic`, between the temporary file and its rename), in a profile copy
and in `promote` (before the rename, before the local copy is removed), and in `init` and
`uninstall` (between their phases). With `OUTFIT_FAULT_AT=N` the process dies by SIGKILL at
the Nth point; `OUTFIT_FAULT_LOG=<file>` lists them. `TestFaultInjection` kills thirteen
operations (use, switch, back to base, absorb, quarantine, `author on/off`, use of a local
profile, `promote`, `init`, `uninstall`, uninstall in a worktree farm, a killed `use`
followed by `uninstall`) at **every** point each has, in parallel, then reruns `sync` (or
`uninstall`) twice and checks that both runs simply worked (exit 0, except the one scenario
that ends in "a person must look"), nothing was lost, no temporary link or untracked file is
left behind, the profile on disk is the profile in config, and an uninstall (or the undo of a
killed `init`) restores `git config --list` exactly. In a normal build `fault.Point` is an
empty function.

It found four bugs in its first runs, all fixed with a test that pins them: settings nothing
remembered (the intent is now written before sparse checkout is turned on), links nothing
remembered (uninstall finds them by their skip-worktree bit and target), a temporary link
beside the target (it now lives in the git dir; `swaps-stray.txtar` covers the fallback for a
git dir on another filesystem), and temporary files of a killed write in outfit's hooks
directory.

What a kill cannot reach: the places outside the code above that change things (the git
config writes of `use`, `set` and `author`, the agents' settings file), and torn writes
(SIGKILL stops the process between system calls, it does not tear one).

## Fuzzing

The seed corpora run as ordinary tests. `scripts/dev/fuzz.sh [seconds]` runs each target for
a while; the `fuzz` CI job runs them for 20 s each. A crash writes its input to
`testdata/fuzz/<Target>/`: commit it, it becomes a regression test (CI uploads it as an
artifact). `FuzzPlan` found, in its first minute, a conflicted path the plan would have
quarantined and removed.

## CI

`all-green` needs every job except two, which are deliberately outside it (a deviation from
§8.1's "every job"): `fuzz` (it looks for NEW inputs, so it can fail on old code in an
unrelated pull request) and `perf` (soft, Linux only, `continue-on-error`). A failing one is a
red job and a red workflow run, and the merge policy reads every job, but the branch ruleset
PR 11 adds requires only `ship`, `commits` and `all-green`.
