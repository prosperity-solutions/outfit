package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/cli"
	"github.com/prosperity-solutions/outfit/internal/testkit"
	"github.com/prosperity-solutions/outfit/internal/update"
)

type updateFixture struct {
	t    *testing.T
	rel  *testkit.FakeRelease
	exe  string
	home string
	env  map[string]string
	out  bytes.Buffer
	errb bytes.Buffer
	tty  bool
}

// newUpdate makes a fake release (0.1.0 and 0.2.0) and an "installed outfit".
func newUpdate(t *testing.T) *updateFixture {
	t.Helper()
	plat, err := update.Platform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	f := &updateFixture{t: t, rel: testkit.NewFakeRelease(t, plat), home: t.TempDir()}
	f.rel.Add("0.1.0")
	f.rel.Add("0.2.0")
	dir := t.TempDir()
	f.exe = filepath.Join(dir, "outfit")
	if err := os.WriteFile(f.exe, testkit.Binary("0.1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.env = map[string]string{"HOME": f.home, "OUTFIT_BASE_URL": f.rel.URL, "OUTFIT_API_URL": f.rel.APIURL}
	return f
}

func (f *updateFixture) run(version string, args ...string) int {
	f.t.Helper()
	f.out.Reset()
	f.errb.Reset()
	return cli.Execute(context.Background(), cli.Env{
		Build: cli.Build{Version: version}, Dir: f.home, Getenv: func(k string) string { return f.env[k] },
		Stdout: &f.out, Stderr: &f.errb, Exe: f.exe, IsTTY: f.tty, UpdateKeys: f.rel.Keys,
	}, args)
}

func (f *updateFixture) installed() string {
	b, err := os.ReadFile(f.exe)
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

func TestUpdateCheck(t *testing.T) {
	f := newUpdate(t)
	if code := f.run("0.1.0", "update", "--check"); code != 0 || !strings.Contains(f.out.String(), "Update available: 0.1.0 → 0.2.0. Run outfit update") {
		t.Fatalf("code=%d out=%q err=%q", code, f.out.String(), f.errb.String())
	}
	if code := f.run("0.2.0", "update", "--check"); code != 0 || !strings.Contains(f.out.String(), "up to date") {
		t.Fatalf("code=%d out=%q", code, f.out.String())
	}
	if code := f.run("v0.1.0", "update", "--check", "--json"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	var j struct {
		Current         string `json:"current"`
		Latest          string `json:"latest"`
		UpdateAvailable bool   `json:"update_available"`
	}
	if err := json.Unmarshal(f.out.Bytes(), &j); err != nil || j.Latest != "0.2.0" || !j.UpdateAvailable || j.Current != "v0.1.0" {
		t.Fatalf("json = %q (%v)", f.out.String(), err)
	}
	if f.installed() != string(testkit.Binary("0.1.0")) {
		t.Fatal("--check changed the binary")
	}
	if code := f.run("0.1.0", "update", "--check", "--version", "0.1.0"); code != cli.ExitUsage {
		t.Fatalf("--check with --version: code=%d", code)
	}
}

func TestUpdateReplacesTheBinary(t *testing.T) {
	f := newUpdate(t)
	if code := f.run("0.1.0", "update"); code != 0 {
		t.Fatalf("code=%d err=%q", code, f.errb.String())
	}
	if f.installed() != string(testkit.Binary("0.2.0")) {
		t.Fatalf("installed = %q", f.installed())
	}
	if !strings.Contains(f.out.String(), "Updated outfit 0.1.0 → 0.2.0") || !strings.Contains(f.out.String(), "verified: minisign signature and SHA-256") {
		t.Fatalf("out = %q", f.out.String())
	}
	// Up to date now: nothing is downloaded.
	before := len(f.rel.Requests())
	if code := f.run("0.2.0", "update"); code != 0 || !strings.Contains(f.out.String(), "up to date") {
		t.Fatalf("code=%d out=%q", code, f.out.String())
	}
	for _, r := range f.rel.Requests()[before:] {
		if strings.HasSuffix(r.Path, ".tar.gz") {
			t.Fatalf("an up-to-date outfit downloaded the archive: %v", r)
		}
	}
}

func TestUpdateVersionFlag(t *testing.T) {
	for _, args := range [][]string{{"update", "--version", "0.2.0"}, {"update", "--version=v0.2.0"}, {"update", "-version", "v0.2.0"}} {
		f := newUpdate(t)
		if code := f.run("0.1.0", args...); code != 0 || f.installed() != string(testkit.Binary("0.2.0")) {
			t.Fatalf("%v: code=%d err=%q", args, code, f.errb.String())
		}
	}
	f := newUpdate(t)
	if code := f.run("0.2.0", "update", "--version", "v0.1.0"); code != 0 || !strings.Contains(f.out.String(), "Downgraded") || f.installed() != string(testkit.Binary("0.1.0")) {
		t.Fatalf("downgrade: code=%d out=%q", code, f.out.String())
	}
	if code := f.run("0.1.0", "update", "--version", "0.1.0"); code != 0 || !strings.Contains(f.out.String(), "Reinstalled") {
		t.Fatalf("same version: code=%d out=%q", code, f.out.String())
	}
	if code := f.run("0.1.0", "update", "--version", "soon"); code != cli.ExitUsage {
		t.Fatalf("bad version: code=%d", code)
	}
	if code := f.run("0.1.0", "update", "--version", "9.9.9"); code != cli.ExitError {
		t.Fatalf("missing version: code=%d", code)
	}
}

func TestUpdateDevBuildRefusesUnlessAskedForARelease(t *testing.T) {
	f := newUpdate(t)
	if code := f.run("dev", "update"); code != cli.ExitError || !strings.Contains(f.errb.String(), "development build") || !strings.Contains(f.errb.String(), "outfit update --version") {
		t.Fatalf("code=%d err=%q", code, f.errb.String())
	}
	if f.installed() != string(testkit.Binary("0.1.0")) {
		t.Fatal("a refused update changed the binary")
	}
	if code := f.run("dev", "update", "--version", "0.2.0"); code != 0 || f.installed() != string(testkit.Binary("0.2.0")) {
		t.Fatalf("dev with --version: code=%d err=%q", code, f.errb.String())
	}
}

func TestUpdateRefusesWhatItCannotVerify(t *testing.T) {
	for name, set := range map[string]func(*testkit.FakeRelease){
		"bad checksum":  func(r *testkit.FakeRelease) { r.Break.Checksum = true },
		"bad signature": func(r *testkit.FakeRelease) { r.Break.Signature = true },
	} {
		t.Run(name, func(t *testing.T) {
			plat, _ := update.Platform(runtime.GOOS, runtime.GOARCH)
			f := &updateFixture{t: t, rel: testkit.NewFakeRelease(t, plat), home: t.TempDir()}
			set(f.rel)
			f.rel.Add("0.2.0")
			f.exe = filepath.Join(t.TempDir(), "outfit")
			_ = os.WriteFile(f.exe, []byte("old"), 0o755)
			f.env = map[string]string{"HOME": f.home, "OUTFIT_BASE_URL": f.rel.URL, "OUTFIT_API_URL": f.rel.APIURL}
			if code := f.run("0.1.0", "update"); code != cli.ExitError || !strings.Contains(f.errb.String(), "nothing was installed") {
				t.Fatalf("code=%d err=%q", code, f.errb.String())
			}
			if f.installed() != "old" {
				t.Fatalf("the binary changed: %q", f.installed())
			}
		})
	}
}

func TestUpdateReadOnlyDirectoryPrintsTheInstallCommand(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write anywhere")
	}
	f := newUpdate(t)
	dir := filepath.Dir(f.exe)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if code := f.run("0.1.0", "update"); code != cli.ExitNeedsHuman {
		t.Fatalf("code=%d err=%q", code, f.errb.String())
	}
	if !strings.Contains(f.errb.String(), "OUTFIT_INSTALL_DIR=") || !strings.Contains(f.errb.String(), "install.sh | ") || !strings.Contains(f.errb.String(), "never uses sudo") {
		t.Fatalf("err = %q", f.errb.String())
	}
}

func TestUpdateSecondRunExitsZeroWithANote(t *testing.T) {
	f := newUpdate(t)
	unlock, err := update.Lock(update.CacheDir(func(k string) string { return f.env[k] }))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if code := f.run("0.1.0", "update"); code != 0 || !strings.Contains(f.out.String(), "already running") {
		t.Fatalf("code=%d out=%q", code, f.out.String())
	}
	if f.installed() != string(testkit.Binary("0.1.0")) {
		t.Fatal("the locked-out update changed the binary")
	}
}

func TestUpdatePrivateForkNeedsAnExplicitToken(t *testing.T) {
	f := newUpdate(t)
	f.rel.Private, f.rel.Token = true, "ghp_theright1token"
	if code := f.run("0.1.0", "update"); code != cli.ExitError || !strings.Contains(f.errb.String(), "HTTP 404") {
		t.Fatalf("no token: code=%d err=%q", code, f.errb.String())
	}
	// Ambient tokens are not looked at, however right they are.
	f.env["GH_TOKEN"], f.env["GITHUB_TOKEN"] = "ghp_theright1token", "ghp_theright1token"
	if code := f.run("0.1.0", "update"); code != cli.ExitError || f.installed() != string(testkit.Binary("0.1.0")) {
		t.Fatalf("an ambient token was used: code=%d err=%q", code, f.errb.String())
	}
	f.env["OUTFIT_GITHUB_TOKEN"] = "ghp_theright1token"
	if code := f.run("0.1.0", "update"); code != 0 || f.installed() != string(testkit.Binary("0.2.0")) {
		t.Fatalf("token: code=%d err=%q", code, f.errb.String())
	}
	if strings.Contains(f.out.String()+f.errb.String(), "ghp_theright1token") {
		t.Fatal("the token was printed")
	}
}

// ---- the banner ------------------------------------------------------------

func bannerRun(t *testing.T, f *updateFixture, repo *testkit.Repo, extra map[string]string, tty bool, args ...string) string {
	t.Helper()
	env := map[string]string{}
	for _, kv := range repo.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	env["HOME"] = f.home // the update cache lives under it
	// The hermetic env opts out, and the machine running the tests may be CI or a git
	// hook itself; these tests are about the banner's own rules, set one at a time.
	for _, k := range []string{"OUTFIT_NO_UPDATE_CHECK", "CI", "OUTFIT_HOOK_ACTIVE", "GIT_INDEX_FILE"} {
		env[k] = ""
	}
	env["OUTFIT_BASE_URL"], env["OUTFIT_API_URL"] = f.rel.URL, f.rel.APIURL
	for k, v := range extra {
		env[k] = v
	}
	var out, errb bytes.Buffer
	cli.Execute(context.Background(), cli.Env{
		Build: cli.Build{Version: "0.1.0"}, Dir: repo.Dir, Getenv: func(k string) string { return env[k] },
		Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(""), Exe: "/usr/local/bin/outfit", IsTTY: tty, UpdateKeys: f.rel.Keys,
	}, args)
	return errb.String()
}

func TestBannerOnlyWhereItBelongs(t *testing.T) {
	f := newUpdate(t)
	repo := testkit.New(t, testkit.Team)
	const banner = "Update available: 0.1.0 → 0.2.0. Run outfit update"

	// Shown on a human-facing command, on a terminal.
	if got := bannerRun(t, f, repo, nil, true, "ls"); !strings.Contains(got, banner) {
		t.Fatalf("no banner on ls: %q", got)
	}
	// Each rule that switches it off. (The first run cached the answer, so these
	// would show it if the rule did not stop them.)
	for name, c := range map[string]struct {
		extra map[string]string
		tty   bool
		args  []string
	}{
		"not a terminal":     {nil, false, []string{"ls"}},
		"--json":             {nil, true, []string{"ls", "--json"}},
		"CI":                 {map[string]string{"CI": "true"}, true, []string{"ls"}},
		"opt-out":            {map[string]string{"OUTFIT_NO_UPDATE_CHECK": "1"}, true, []string{"ls"}},
		"get (hook command)": {nil, true, []string{"get", "biome"}},
		"hook":               {nil, true, []string{"hook", "pre-commit"}},
		"version":            {nil, true, []string{"version"}},
		"inside a git hook":  {map[string]string{"OUTFIT_HOOK_ACTIVE": "pre-commit@/r/.git"}, true, []string{"ls"}},
		"git exported index": {map[string]string{"GIT_INDEX_FILE": "/r/.git/index"}, true, []string{"ls"}},
	} {
		if got := bannerRun(t, f, repo, c.extra, c.tty, c.args...); strings.Contains(got, "Update available") {
			t.Errorf("%s: banner shown: %q", name, got)
		}
	}
}

func TestHookCommandsNeverTouchTheNetwork(t *testing.T) {
	f := newUpdate(t)
	repo := testkit.New(t, testkit.Team)
	before := len(f.rel.Requests())
	bannerRun(t, f, repo, nil, true, "get", "biome")
	bannerRun(t, f, repo, nil, true, "hook", "pre-commit")
	if n := len(f.rel.Requests()) - before; n != 0 {
		t.Fatalf("hook commands made %d network requests", n)
	}
}

func TestUpdateJSONErrorsCarryAFixAndRetryable(t *testing.T) {
	f := newUpdate(t)
	var j struct {
		Error     string `json:"error"`
		Fix       string `json:"fix"`
		Retryable bool   `json:"retryable"`
		Code      int    `json:"code"`
	}
	decode := func() {
		t.Helper()
		j = struct {
			Error     string `json:"error"`
			Fix       string `json:"fix"`
			Retryable bool   `json:"retryable"`
			Code      int    `json:"code"`
		}{}
		if err := json.Unmarshal(f.out.Bytes(), &j); err != nil {
			t.Fatalf("not JSON: %q", f.out.String())
		}
	}
	// Nobody home: transient, so retryable, and the fix is to try again.
	f.env["OUTFIT_BASE_URL"] = "http://127.0.0.1:1"
	if code := f.run("0.1.0", "update", "--check", "--json"); code != cli.ExitError {
		t.Fatalf("code=%d", code)
	}
	decode()
	if !j.Retryable || j.Fix == "" || j.Code != 1 {
		t.Fatalf("unreachable: %+v", j)
	}
	// A bad signature is not transient, but still names a fix.
	f2 := newUpdate(t)
	f2.rel.Break.Signature = true
	f3 := &updateFixture{t: t, rel: testkit.NewFakeRelease(t, f2.rel.Plat), home: t.TempDir(), exe: f.exe}
	f3.rel.Break.Signature = true
	f3.rel.Add("0.2.0")
	f3.env = map[string]string{"HOME": f3.home, "OUTFIT_BASE_URL": f3.rel.URL, "OUTFIT_API_URL": f3.rel.APIURL}
	f = f3
	if code := f.run("0.1.0", "update", "--json"); code != cli.ExitError {
		t.Fatalf("code=%d out=%q", code, f.out.String())
	}
	decode()
	if j.Retryable || j.Fix == "" || !strings.Contains(j.Error, "nothing was installed") {
		t.Fatalf("bad signature: %+v", j)
	}
}

func TestUpdateCheckOnADevBuildDoesNotPointAtAFailingCommand(t *testing.T) {
	f := newUpdate(t)
	if code := f.run("dev", "update", "--check"); code != 0 {
		t.Fatalf("code=%d err=%q", code, f.errb.String())
	}
	out := f.out.String()
	if !strings.Contains(out, "development build") || !strings.Contains(out, "outfit update --version 0.2.0") || strings.Contains(out, "Run outfit update") {
		t.Fatalf("out = %q", out)
	}
}

func TestUpdateRefusesToBelieveAnUnsignedLatest(t *testing.T) {
	// The release listing says 0.2.0 but its checksum list is signed by someone else:
	// outfit must not conclude "up to date" or "update available" from it.
	plat, _ := update.Platform(runtime.GOOS, runtime.GOARCH)
	f := &updateFixture{t: t, rel: testkit.NewFakeRelease(t, plat), home: t.TempDir()}
	f.rel.Break.Signature = true
	f.rel.Add("0.2.0")
	f.exe = filepath.Join(t.TempDir(), "outfit")
	_ = os.WriteFile(f.exe, []byte("old"), 0o755)
	f.env = map[string]string{"HOME": f.home, "OUTFIT_BASE_URL": f.rel.URL, "OUTFIT_API_URL": f.rel.APIURL}
	for _, args := range [][]string{{"update"}, {"update", "--check"}} {
		if code := f.run("0.2.0", args...); code != cli.ExitError {
			t.Fatalf("%v: code=%d out=%q: an unsigned answer was believed", args, code, f.out.String())
		}
	}
}

func TestReadOnlyFixSaysWhatToDoNext(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write anywhere")
	}
	f := newUpdate(t)
	dir := filepath.Dir(f.exe)
	_ = os.Chmod(dir, 0o555)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	f.run("0.1.0", "update")
	for _, want := range []string{"outfit init", "PATH"} {
		if !strings.Contains(f.errb.String(), want) {
			t.Errorf("err = %q, want %q", f.errb.String(), want)
		}
	}
}

func TestUpdateRetryFixKeepsWhatWasAsked(t *testing.T) {
	f := newUpdate(t)
	f.env["OUTFIT_BASE_URL"] = "http://127.0.0.1:1"
	for args, want := range map[string]string{
		"update --version 0.2.0": "outfit update --version 0.2.0",
		"update --check":         "outfit update --check",
		"update":                 "fix: outfit update   (try again",
	} {
		f.run("0.1.0", strings.Fields(args)...)
		if !strings.Contains(f.errb.String(), want) {
			t.Errorf("%q: err = %q, want %q", args, f.errb.String(), want)
		}
	}
	// A dev build cannot run a bare `outfit update`, so its retry is --check.
	f.run("dev", "update")
	f.run("dev", "update", "--check")
	if !strings.Contains(f.errb.String(), "outfit update --check   (try again") {
		t.Errorf("dev --check retry: %q", f.errb.String())
	}
}

func TestUpdateStaleTokenIsNamedAndNeverPrinted(t *testing.T) {
	f := newUpdate(t)
	f.rel.Private, f.rel.Token = true, "ghp_theright1token"
	f.env["OUTFIT_GITHUB_TOKEN"] = "ghp_stale0token"
	f.run("0.1.0", "update")
	if got := f.errb.String(); !strings.Contains(got, "fix: unset OUTFIT_GITHUB_TOKEN") || strings.Contains(got, "ghp_stale0token") {
		t.Fatalf("stale token: %q", got)
	}
}

func TestUpdateVersionFlagMistakes(t *testing.T) {
	f := newUpdate(t)
	for _, args := range [][]string{{"update", "--version="}, {"update", "--version", ""}, {"update", "--version"}} {
		if code := f.run("0.1.0", args...); code != cli.ExitUsage {
			t.Errorf("%q: code=%d err=%q", args, code, f.errb.String())
		}
	}
	// `--version --json`: the value is missing, and the error is still JSON.
	if code := f.run("0.1.0", "update", "--version", "--json"); code != cli.ExitUsage || !strings.HasPrefix(f.out.String(), `{"error"`) {
		t.Fatalf("code=%d out=%q err=%q", code, f.out.String(), f.errb.String())
	}
	if f.installed() != string(testkit.Binary("0.1.0")) {
		t.Fatal("a usage error changed the binary")
	}
}
