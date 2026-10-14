package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(context.Background(), Build{Version: "v1.2.3", Commit: "abc", Date: "today"}, args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersionHuman(t *testing.T) {
	code, out, _ := run(t, "version")
	if code != ExitOK || !strings.HasPrefix(out, "outfit v1.2.3 (commit abc, built today") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := run(t, "version", "--json")
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	var v versionInfo
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("not json: %v: %q", err, out)
	}
	if v.Version != "v1.2.3" || v.Commit != "abc" || v.OS == "" {
		t.Fatalf("unexpected %+v", v)
	}
}

func TestUnknownCommandNamesTheFix(t *testing.T) {
	code, _, errs := run(t, "nope")
	if code != ExitUsage || !strings.Contains(errs, "unknown command") || !strings.Contains(errs, "fix: outfit help") {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
}

func TestNoArgsIsUsage(t *testing.T) {
	if code, _, _ := run(t); code != ExitUsage {
		t.Fatalf("code=%d", code)
	}
}

func TestReportJSONShape(t *testing.T) {
	var out, errb bytes.Buffer
	code := report(&out, &errb, true, Errorf("boom").WithFix("outfit sync"))
	if code != ExitError {
		t.Fatalf("code=%d", code)
	}
	var e jsonError
	if err := json.Unmarshal(out.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e.Error != "boom" || e.Fix != "outfit sync" || e.Retryable || e.Code != ExitError {
		t.Fatalf("%+v", e)
	}
}

func fakeGit(t *testing.T, out string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\necho '"+out+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// `version` is what install.sh prints last: old or missing git is a warning,
// never a failure.
func TestVersionWarnsOnOldGit(t *testing.T) {
	fakeGit(t, "git version 2.34.1")
	code, out, errs := run(t, "version")
	if code != ExitOK || !strings.Contains(out, "git 2.34.1") || !strings.Contains(errs, "ppa:git-core/ppa") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	_, jout, _ := run(t, "version", "--json")
	if !strings.Contains(jout, `"ok":false`) || !strings.Contains(jout, `"warning"`) {
		t.Fatalf("json = %q", jout)
	}
}

func TestVersionWarnsOnMissingGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, out, errs := run(t, "version")
	if code != ExitOK || !strings.Contains(out, "git not found") || !strings.Contains(errs, "install git 2.36.0 or newer") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestUnknownCommandHonoursJSON(t *testing.T) {
	code, out, _ := run(t, "nope", "--json")
	var e jsonError
	if code != ExitUsage || json.Unmarshal([]byte(out), &e) != nil || e.Fix != "outfit help" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}
