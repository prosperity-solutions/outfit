package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
