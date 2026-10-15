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

A registry that cannot be reached is a warning, not a failure: this gate must not stop a
release because of a network hiccup. Standard library only. `--selftest` runs without network.
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
    """(module, version) for each require line, block or single."""
    out = []
    in_block = False
    for line in text.splitlines():
        line = line.split("//")[0].strip()
        if line.startswith("require ("):
            in_block = True
            continue
        if in_block and line == ")":
            in_block = False
            continue
        m = re.match(r"^(?:require\s+)?(\S+)\s+(v\S+)$", line) if (in_block or line.startswith("require ")) else None
        if m:
            out.append((m.group(1), m.group(2)))
    return out


USES = re.compile(r"uses:\s*([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)(?:/[^@\s]*)?@([0-9a-f]{40})(?:\s*#\s*(v?[0-9][\w.+-]*))?")
GORUN = re.compile(r"go\s+(?:run|install)\s+([\w./-]+)@(v[\w.+-]+)")
LINTVER = re.compile(r"^\s*version:\s*(v[0-9][\w.+-]*)\s*$", re.M)
PIP = re.compile(r"^([A-Za-z0-9_.-]+)==([0-9][\w.+-]*)", re.M)
BOOT = re.compile(r"^BOOT_\w+_URL=https://github\.com/([\w.-]+/[\w.-]+)/releases/download/([^/]+)/", re.M)


def collect(root):
    """[(kind, name, version, extra)] for everything pinned in the repository."""
    items = []
    with open(os.path.join(root, "go.mod")) as f:
        for mod, ver in go_mod_requires(f.read()):
            items.append(("go", mod, ver, None))
    wf = os.path.join(root, ".github", "workflows")
    for fn in sorted(os.listdir(wf)):
        if not fn.endswith((".yml", ".yaml")):
            continue
        text = open(os.path.join(wf, fn)).read()
        for m in USES.finditer(text):
            items.append(("action", m.group(1), m.group(2), m.group(3)))
        for m in GORUN.finditer(text):
            items.append(("gotool", m.group(1), m.group(2), None))
        if "golangci/golangci-lint-action" in text:
            for m in LINTVER.finditer(text):
                items.append(("golangci-lint", "golangci/golangci-lint", m.group(1), None))
    ci = os.path.join(root, "scripts", "ci")
    for fn in sorted(os.listdir(ci)):
        if fn.endswith("requirements.txt"):
            for m in PIP.finditer(open(os.path.join(ci, fn)).read()):
                items.append(("pip", m.group(1), m.group(2), None))
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


def published(kind, name, version, extra, token, fetch=http_json):
    """The time a version was published; raises LookupError when it cannot be told."""
    try:
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
    for it in items:
        kind, name, version, extra = it
        key = "%s@%s" % (name, version)
        try:
            t = lookup(*it)
        except LookupError as e:
            unknown.append((it, str(e)))
            continue
        age = (now - t).total_seconds() / 86400
        if age >= MIN_AGE_DAYS:
            ok += 1
        elif key in allow:
            ok += 1
        else:
            too_new.append((it, age))
    return too_new, unknown, ok


def main():
    if "--selftest" in sys.argv:
        return selftest()
    root = ROOT
    items = collect(root)
    token = gh_token()
    now = datetime.datetime.now(datetime.timezone.utc)
    allow = read_allow(root)
    too_new, unknown, ok = judge(items, now, allow, lambda k, n, v, x: published(k, n, v, x, token))
    print("dependency age: %d pinned versions checked, minimum %d days" % (len(items), MIN_AGE_DAYS))
    for it, why in unknown:
        print("::warning::could not tell when %s %s@%s was published (%s)" % (it[0], it[1], it[2], why))
    for it, age in too_new:
        print("::error::%s %s@%s was published %.1f days ago (minimum %d). Take the newest version that is old enough, "
              "or, for a fix to a known vulnerability, name it in .github/fresh-deps.txt with the reason."
              % (it[0], it[1], it[2], age, MIN_AGE_DAYS))
    if too_new:
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
require (
\ta.dev/m v0.3.0
\tgithub.com/Foo/Bar v1.2.3 // indirect
)
require golang.org/x/sys v0.49.0
"""
    check("go.mod: block and single requires", go_mod_requires(gm) == [("a.dev/m", "v0.3.0"), ("github.com/Foo/Bar", "v1.2.3"), ("golang.org/x/sys", "v0.49.0")])
    check("module path escaping", escape_module("github.com/Foo/Bar") == "github.com/!foo/!bar")
    m = USES.search("      - uses: actions/cache/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0")
    check("uses with path and version comment", m and m.groups() == ("actions/cache", "55cc8345863c7cc4c66a329aec7e433d2d1c52a9", "v6.1.0"))
    check("go run tool", GORUN.search("go run github.com/goreleaser/goreleaser/v2@v2.18.2 check").groups() == ("github.com/goreleaser/goreleaser/v2", "v2.18.2"))
    check("pip pin", PIP.search("zizmor==1.30.1 \\").groups() == ("zizmor", "1.30.1"))

    now = datetime.datetime(2026, 10, 15, tzinfo=datetime.timezone.utc)
    ago = lambda d: now - datetime.timedelta(days=d)
    times = {"old@v1": ago(30), "edge@v1": ago(7), "new@v1": ago(3), "newok@v1": ago(1)}
    items = [("go", "old", "v1", None), ("go", "edge", "v1", None), ("go", "new", "v1", None), ("go", "newok", "v1", None), ("pip", "gone", "v1", None)]

    def lookup(kind, name, version, extra):
        k = "%s@%s" % (name, version)
        if k not in times:
            raise LookupError("no network")
        return times[k]

    too_new, unknown, ok = judge(items, now, {"newok@v1": "fixes GO-2026-1"}, lookup)
    check("a version exactly seven days old passes", ("go", "edge", "v1", None) not in [i for i, _ in too_new])
    check("a young version fails", [i[1] for i, _ in too_new] == ["new"])
    check("an allowlisted young version passes", ("go", "newok", "v1", None) not in [i for i, _ in too_new])
    check("an unreachable registry is unknown, not a failure", [i[1] for i, _ in unknown] == ["gone"])
    check("counts", ok == 3)
    # the real repository must at least parse
    items = collect(ROOT)
    check("the repository's pins are found", any(i[0] == "go" for i in items) and any(i[0] == "action" for i in items) and any(i[0] == "pip" for i in items))
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
