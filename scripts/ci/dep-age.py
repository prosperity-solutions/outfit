#!/usr/bin/env python3
"""A new version waits seven days (docs/security.md).

Reads every version outfit depends on or runs in CI, asks the registry when it was
published, and fails when one is younger than MIN_AGE_DAYS and not named in
.github/fresh-deps.txt (the place a known-vulnerability fix that cannot wait is written down,
with its reason).

Looked at:
  go.mod                       every `require` line (the proxy's .info time)
  .github/workflows/*.yml      `uses: owner/repo@<sha> # vX` (the release, else the commit),
                               `go run|install pkg@vX`, `version: vX` of golangci-lint-action
  scripts/ci/*requirements.txt `name==version` (PyPI upload time)
  install.sh                   the pinned minisign downloads (BOOT_*_URL: the release)

A registry that cannot be reached (a timeout, a 5xx) is a warning, not a failure, so this gate
cannot stop a release because of a network hiccup; with OUTFIT_DEP_AGE_STRICT=1 (set on pull
requests) it is a failure. A registry that answers "no such version" or "not allowed" (4xx other
than a rate limit) is always a failure, and so is any line that looks like a pin but that this script
could not read, so a pin written in an unexpected way cannot slip past it.

What "published" means: the Go proxy's time for a module (the tag commit's committer date: its
author can set it, so this stops a release made today, not a maintainer who back-dates one), the
release's publish date for an action, GitHub release or PyPI file (the registry's own clock), and
the commit date only for an action that has no release (marked "commit date" in the output).
Standard library only. `--selftest` runs without network.
"""
import datetime
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

MIN_AGE_DAYS = int(os.environ.get("OUTFIT_MIN_AGE_DAYS", "7"))
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def parse_time(s):
    s = s.replace("Z", "+00:00")
    t = datetime.datetime.fromisoformat(s)
    if t.tzinfo is None:
        t = t.replace(tzinfo=datetime.timezone.utc)
    return t


def http_json(url, token=None):
    req = urllib.request.Request(url, headers={"User-Agent": "outfit-dep-age"})
    if token:
        req.add_header("Authorization", "Bearer " + token)
        req.add_header("Accept", "application/vnd.github+json")
    with urllib.request.urlopen(req, timeout=20) as r:
        return json.load(r)


def gh_token():
    for k in ("GH_TOKEN", "GITHUB_TOKEN"):
        if os.environ.get(k):
            return os.environ[k]
    try:
        return subprocess.run(["gh", "auth", "token"], capture_output=True, text=True, timeout=10).stdout.strip() or None
    except (OSError, subprocess.SubprocessError):
        return None


# ---- finding what is pinned ---------------------------------------------------------------

def go_mod_requires(text):
    """[(kind, module, version)] for require lines (block or single), `replace ... => path version`
    targets and the toolchain line."""
    out = []
    block = None
    for line in text.splitlines():
        line = line.split("//")[0].strip()
        if not line:
            continue
        m = re.match(r"^(require|replace|exclude|retract)\s*\($", line)
        if m:
            block = m.group(1)
            continue
        if block and line == ")":
            block = None
            continue
        verb = block
        body = line
        if not block:
            m = re.match(r"^(require|replace|exclude|retract)\s+(.*)$", line)
            if m:
                verb, body = m.group(1), m.group(2)
        if verb == "require":
            m = re.match(r"^(\S+)\s+(v\S+)$", body)
            if m:
                out.append(("go", m.group(1), m.group(2)))
        elif verb == "replace":
            m = re.match(r"^.*=>\s*(\S+)\s+(v\S+)$", body)
            if m:
                out.append(("go", m.group(1), m.group(2)))
        elif not block and not verb:
            m = re.match(r"^toolchain\s+go(\S+)$", line)
            if m:
                out.append(("toolchain", "go", m.group(1)))
    return out


USES = re.compile(r"""uses:\s*["']?([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)(?:/[^@\s"']*)?@([0-9a-f]{40})["']?(?:\s*#\s*(v?[0-9][\w.+-]*))?""")
USES_ANY = re.compile(r"^\s*(?:-\s*)?uses:\s*(.+?)\s*(?:#.*)?$")
GORUN = re.compile(r"\bgo\s+(?:run|install)\b((?:\s+-\S+(?:=\S+)?)*)\s+([\w./-]+)@(\S+)")
GORUN_ANY = re.compile(r"\bgo\s+(?:run|install)\b[^\n]*@")
SEMVER = re.compile(r"^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][\w.+-]*)?$")
LINTVER = re.compile(r"""^\s*version:\s*["']?(v[0-9][\w.+-]*)["']?\s*$""", re.M)
PIP = re.compile(r"^([A-Za-z0-9_.-]+)(?:\[[^\]]*\])?\s*==\s*([0-9][\w.+-]*)", re.M)
BOOT = re.compile(r"^BOOT_\w+_URL=https://github\.com/([\w.-]+/[\w.-]+)/releases/download/([^/]+)/", re.M)


def unread_lines(kind, text):
    """Lines that look like a pin of this kind but that the strict patterns above did not read."""
    bad = []
    for n, line in enumerate(text.splitlines(), 1):
        if kind == "workflow":
            if line.lstrip().startswith("#"):
                continue
            m = USES_ANY.match(line)
            if m and not m.group(1).strip("\"'").startswith("./") and not USES.search(line):
                bad.append((n, line.strip()))
            if GORUN_ANY.search(line):
                g = GORUN.search(line)
                if not g or not SEMVER.match(g.group(3)):
                    bad.append((n, line.strip()))
        elif kind == "pip":
            t = line.strip()
            if t and not t.startswith(("#", "--hash", "-")) and not t.startswith("\\") and "==" in t and not PIP.match(t):
                bad.append((n, t))
    return bad


def collect(root, problems=None):
    """[(kind, name, version, extra)] for everything pinned in the repository. Lines that look like a
    pin but could not be read are appended to `problems` as (file, line number, text)."""
    problems = problems if problems is not None else []
    items = []
    with open(os.path.join(root, "go.mod")) as f:
        for kind, mod, ver in go_mod_requires(f.read()):
            items.append((kind, mod, ver, None))
    wf = os.path.join(root, ".github", "workflows")
    for fn in sorted(os.listdir(wf)):
        if not fn.endswith((".yml", ".yaml")):
            continue
        text = open(os.path.join(wf, fn)).read()
        for m in USES.finditer(text):
            items.append(("action", m.group(1), m.group(2), m.group(3)))
        for m in GORUN.finditer(text):
            if SEMVER.match(m.group(3)):
                items.append(("gotool", m.group(2), m.group(3), None))
        if "golangci/golangci-lint-action" in text:
            for m in LINTVER.finditer(text):
                items.append(("golangci-lint", "golangci/golangci-lint", m.group(1), None))
        for n, line in unread_lines("workflow", text):
            problems.append((".github/workflows/" + fn, n, line))
    ci = os.path.join(root, "scripts", "ci")
    for fn in sorted(os.listdir(ci)):
        if fn.endswith("requirements.txt"):
            text = open(os.path.join(ci, fn)).read()
            for m in PIP.finditer(text):
                items.append(("pip", m.group(1), m.group(2), None))
            for n, line in unread_lines("pip", text):
                problems.append(("scripts/ci/" + fn, n, line))
    inst = os.path.join(root, "install.sh")
    if os.path.exists(inst):
        for m in BOOT.finditer(open(inst).read()):
            items.append(("release", m.group(1), m.group(2), None))
    seen, out = set(), []
    for it in items:
        k = it[:3]
        if k not in seen:
            seen.add(k)
            out.append(it)
    return out


# ---- asking the registries ----------------------------------------------------------------

def escape_module(path):
    return re.sub(r"[A-Z]", lambda m: "!" + m.group(0).lower(), path)


class Rejected(Exception):
    """The registry answered that the version does not exist or may not be read (a 4xx)."""


def published(kind, name, version, extra, token, fetch=http_json):
    """The time a version was published. Raises LookupError when the registry could not be reached
    (a warning) and Rejected when it answered no (a failure)."""
    try:
        if kind == "toolchain":
            return parse_time(fetch("https://proxy.golang.org/golang.org/toolchain/@v/v0.0.1-go%s.linux-amd64.info" % version)["Time"])
        if kind == "go":
            return parse_time(fetch("https://proxy.golang.org/%s/@v/%s.info" % (escape_module(name), version))["Time"])
        if kind == "gotool":
            parts = name.split("/")
            for n in range(len(parts), 0, -1):
                mod = "/".join(parts[:n])
                try:
                    return parse_time(fetch("https://proxy.golang.org/%s/@v/%s.info" % (escape_module(mod), version))["Time"])
                except urllib.error.HTTPError as e:
                    if e.code not in (404, 410):
                        raise
            raise LookupError("no module for " + name)
        if kind == "pip":
            files = fetch("https://pypi.org/pypi/%s/%s/json" % (name, version))["urls"]
            return min(parse_time(f["upload_time_iso_8601"]) for f in files)
        if kind in ("golangci-lint", "release"):
            return parse_time(fetch("https://api.github.com/repos/%s/releases/tags/%s" % (name, version), token)["published_at"])
        if kind == "action":
            if extra:
                try:
                    return parse_time(fetch("https://api.github.com/repos/%s/releases/tags/%s" % (name, extra), token)["published_at"])
                except urllib.error.HTTPError as e:
                    if e.code != 404:
                        raise
            return parse_time(fetch("https://api.github.com/repos/%s/commits/%s" % (name, version), token)["commit"]["committer"]["date"])
    except urllib.error.HTTPError as e:
        # A rate limit says nothing about the version; every other 4xx is an answer.
        if e.code in (429,) or (e.code == 403 and "rate limit" in (getattr(e, "reason", "") or "").lower()):
            raise LookupError("rate limited (%s)" % e.code)
        if 400 <= e.code < 500:
            raise Rejected("%s answered %d for %s %s@%s" % ("registry", e.code, kind, name, version))
        raise LookupError(str(e))
    except (urllib.error.URLError, OSError, KeyError, ValueError) as e:
        raise LookupError(str(e))
    raise LookupError("unknown kind " + kind)


def read_allow(root):
    path = os.path.join(root, ".github", "fresh-deps.txt")
    allow = {}
    if os.path.exists(path):
        for line in open(path):
            body, _, why = line.partition("#")
            body = body.strip()
            if body:
                allow[body] = why.strip()
    return allow


def judge(items, now, allow, lookup):
    """(too_new, unknown, ok_count): too_new = [(item, age_days)] not allowed."""
    too_new, unknown, ok = [], [], 0
    rejected = []
    for it in items:
        kind, name, version, extra = it
        key = "%s@%s" % (name, version)
        try:
            t = lookup(*it)
        except LookupError as e:
            unknown.append((it, str(e)))
            continue
        except Rejected as e:
            rejected.append((it, str(e)))
            continue
        age = (now - t).total_seconds() / 86400
        if age >= MIN_AGE_DAYS:
            ok += 1
        elif key in allow:
            ok += 1
        else:
            too_new.append((it, age))
    return too_new, unknown, ok, rejected


def main():
    if "--selftest" in sys.argv:
        return selftest()
    root = ROOT
    problems = []
    items = collect(root, problems)
    token = gh_token()
    now = datetime.datetime.now(datetime.timezone.utc)
    allow = read_allow(root)
    strict = os.environ.get("OUTFIT_DEP_AGE_STRICT") == "1"
    too_new, unknown, ok, rejected = judge(items, now, allow, lambda k, n, v, x: published(k, n, v, x, token))
    print("dependency age: %d pinned versions checked, minimum %d days%s" % (len(items), MIN_AGE_DAYS, " (strict)" if strict else ""))
    failed = False
    for it, why in unknown:
        level = "error" if strict else "warning"
        failed = failed or strict
        print("::%s::could not tell when %s %s@%s was published (%s)" % (level, it[0], it[1], it[2], why))
    for it, why in rejected:
        failed = True
        print("::error::%s" % why)
    for f, n, line in problems:
        failed = True
        print("::error file=%s,line=%d::looks like a pinned version but could not be read (pin it as `x@vX.Y.Z` / `uses: o/r@<sha> # vX`): %s" % (f, n, line))
    for it, age in too_new:
        failed = True
        print("::error::%s %s@%s was published %.1f days ago (minimum %d). Take the newest version that is old enough, "
              "or, for a fix to a known vulnerability, name it in .github/fresh-deps.txt with the reason."
              % (it[0], it[1], it[2], age, MIN_AGE_DAYS))
    if failed:
        return 1
    print("ok: every pinned version is at least %d days old (%d unknown)" % (MIN_AGE_DAYS, len(unknown)))
    return 0


# ---- selftest -----------------------------------------------------------------------------

def selftest():
    fails = []

    def check(name, cond):
        print(("ok   " if cond else "FAIL ") + name)
        if not cond:
            fails.append(name)

    gm = """module x
go 1.27
toolchain go1.27.2
require (
\ta.dev/m v0.3.0
\tgithub.com/Foo/Bar v1.2.3 // indirect
)
require golang.org/x/sys v0.49.0
replace golang.org/x/net => golang.org/x/net v0.50.0
replace (
\tgithub.com/a/b => github.com/c/d v1.0.0
\tgithub.com/e/f => ../local
)
exclude golang.org/x/text v0.1.0
"""
    got = go_mod_requires(gm)
    check("go.mod: requires, replace targets and the toolchain", got == [("toolchain", "go", "1.27.2"), ("go", "a.dev/m", "v0.3.0"), ("go", "github.com/Foo/Bar", "v1.2.3"), ("go", "golang.org/x/sys", "v0.49.0"), ("go", "golang.org/x/net", "v0.50.0"), ("go", "github.com/c/d", "v1.0.0")])
    check("module path escaping", escape_module("github.com/Foo/Bar") == "github.com/!foo/!bar")
    m = USES.search("      - uses: actions/cache/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0")
    check("uses with path and version comment", m and m.groups() == ("actions/cache", "55cc8345863c7cc4c66a329aec7e433d2d1c52a9", "v6.1.0"))
    check("quoted uses is read too", USES.search("      - uses: 'actions/labeler@%s'" % ("a" * 40)) is not None)
    check("go run tool", GORUN.search("go run github.com/goreleaser/goreleaser/v2@v2.18.2 check").groups()[1:] == ("github.com/goreleaser/goreleaser/v2", "v2.18.2"))
    check("go install with a flag before the package", GORUN.search("go install -v x.dev/t@v1.2.3").groups()[1:] == ("x.dev/t", "v1.2.3"))
    check("pip pin, with extras and spaces", PIP.search("zizmor[x] == 1.30.1 \\").groups() == ("zizmor", "1.30.1"))

    # Pins written in a way the strict patterns do not read must be reported, never skipped.
    bad_wf = "\n".join([
        "      - uses: docker://alpine:3.20",
        "      - uses: actions/checkout@v4",
        "      - uses: owner/repo@" + "b" * 40,  # fine
        "        run: go run x.dev/t@latest",
        "        run: GOFLAGS=-x go run x.dev/t@master",
        "        run: go install x.dev/t@v1.2.3",  # fine
    ])
    unread = [t for _, t in unread_lines("workflow", bad_wf)]
    check("docker://, a tag ref and a branch ref are reported", len(unread) == 4 and all("v1.2.3" not in t and "b" * 40 not in t for t in unread))
    check("a local action is not a problem", unread_lines("workflow", "      - uses: ./.github/actions/x") == [])
    check("a comment that mentions go run x@v is not a pin", unread_lines("workflow", "# tools run with `go run x@v`") == [])
    check("a pip line with a tag-like pin is read, a hash line is not a pin", unread_lines("pip", "zizmor==1.30.1 \\\n    --hash=sha256:abc\n") == [])

    now = datetime.datetime(2026, 10, 15, tzinfo=datetime.timezone.utc)
    ago = lambda d: now - datetime.timedelta(days=d)
    times = {"old@v1": ago(30), "edge@v1": ago(7), "new@v1": ago(3), "newok@v1": ago(1)}
    items = [("go", "old", "v1", None), ("go", "edge", "v1", None), ("go", "new", "v1", None), ("go", "newok", "v1", None), ("pip", "gone", "v1", None), ("pip", "nope", "v1", None)]

    def lookup(kind, name, version, extra):
        k = "%s@%s" % (name, version)
        if name == "nope":
            raise Rejected("404")
        if k not in times:
            raise LookupError("no network")
        return times[k]

    too_new, unknown, ok, rejected = judge(items, now, {"newok@v1": "fixes GO-2026-1"}, lookup)
    check("a version exactly seven days old passes", ("go", "edge", "v1", None) not in [i for i, _ in too_new])
    check("a young version fails", [i[1] for i, _ in too_new] == ["new"])
    check("an allowlisted young version passes", ("go", "newok", "v1", None) not in [i for i, _ in too_new])
    check("an unreachable registry is unknown", [i[1] for i, _ in unknown] == ["gone"])
    check("a registry that says no is rejected", [i[1] for i, _ in rejected] == ["nope"])
    check("counts", ok == 3)

    # The real repository: the pins that must be found (a pattern change that drops a whole kind
    # fails here), and nothing that looks like a pin may go unread.
    problems = []
    items = collect(ROOT, problems)
    kinds = {}
    for k, n, v, _ in items:
        kinds.setdefault(k, set()).add(n)
    check("go.mod pins and the toolchain are found", {"toolchain", "go"} <= set(kinds) and "aead.dev/minisign" in kinds.get("go", ()))
    check("action pins are found", {"actions/checkout", "actions/setup-go", "actions/download-artifact", "actions/attest-build-provenance"} <= kinds.get("action", set()))
    check("go run tools are found", {"github.com/goreleaser/goreleaser/v2", "golang.org/x/vuln/cmd/govulncheck"} <= kinds.get("gotool", set()))
    check("pip pins are found", {"zizmor", "pyyaml"} <= kinds.get("pip", set()))
    check("golangci-lint and the minisign downloads are found", "golangci/golangci-lint" in kinds.get("golangci-lint", ()) and "jedisct1/minisign" in kinds.get("release", ()))
    check("nothing in the repository looks like an unread pin", problems == [])
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
