package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want Version
	}{
		{"git version 2.43.0", Version{2, 43, 0}},
		{"git version 2.39.5 (Apple Git-154)", Version{2, 39, 5}},
		{"git version 2.36.0.rc1", Version{2, 36, 0}},
		{"2.50", Version{2, 50, 0}},
		{"git version 2.54.0\n", Version{2, 54, 0}},
	}
	for _, c := range cases {
		got, err := ParseVersion(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	if _, err := ParseVersion("no digits here"); err == nil {
		t.Error("expected an error for unparsable input")
	}
}

func TestCheckFloor(t *testing.T) {
	cases := []struct {
		v    Version
		fail bool
	}{
		{Version{2, 34, 1}, true}, // Ubuntu 22.04
		{Version{2, 35, 9}, true},
		{Version{2, 36, 0}, false},
		{Version{2, 39, 5}, false}, // Apple git
		{Version{3, 0, 0}, false},
	}
	for _, c := range cases {
		err := CheckFloor(c.v)
		if (err != nil) != c.fail {
			t.Errorf("CheckFloor(%v) = %v, want fail=%v", c.v, err, c.fail)
		}
	}
	var fe *FloorError
	err := CheckFloor(Version{2, 34, 1})
	if !errors.As(err, &fe) {
		t.Fatalf("want *FloorError, got %T", err)
	}
	if !strings.Contains(err.Error(), "ppa:git-core/ppa") || !strings.Contains(err.Error(), "2.34.1") {
		t.Errorf("error text must name the fix: %q", err.Error())
	}
}

func TestVersionLess(t *testing.T) {
	if !(Version{2, 9, 0}).Less(Version{2, 10, 0}) {
		t.Error("2.9 must be older than 2.10 (numeric, not lexical)")
	}
	if (Version{2, 36, 0}).Less(Version{2, 36, 0}) {
		t.Error("equal versions are not Less")
	}
}

func TestCallCounting(t *testing.T) {
	ResetCalls()
	c := Client{}
	if _, err := c.Read(context.Background(), "--version"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(context.Background(), "--version"); err != nil {
		t.Fatal(err)
	}
	if got := Calls(); got != 2 {
		t.Fatalf("Calls() = %d, want 2", got)
	}
}

func TestTraceFile(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv(TraceEnv, trace)
	if _, err := (Client{}).Read(context.Background(), "--version"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(trace)
	if err != nil || strings.Count(string(b), "\n") != 1 || !strings.Contains(string(b), "git --version") {
		t.Fatalf("trace = %q, err = %v", b, err)
	}
}

func TestReadSetsOptionalLocksOff(t *testing.T) {
	// `git var` is cheap; GIT_OPTIONAL_LOCKS shows up in the environment of a
	// child, which we observe through an alias that prints it.
	res, err := Client{Env: []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=alias.lock", "GIT_CONFIG_VALUE_0=!printf %s \"$GIT_OPTIONAL_LOCKS\""}}.Read(context.Background(), "lock")
	if err != nil {
		t.Skipf("alias execution unavailable: %v", err)
	}
	if string(res.Stdout) != "0" {
		t.Fatalf("GIT_OPTIONAL_LOCKS = %q on a read, want 0", res.Stdout)
	}
	res, err = Client{Env: []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=alias.lock", "GIT_CONFIG_VALUE_0=!printf %s \"$GIT_OPTIONAL_LOCKS\""}}.Write(context.Background(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) == "0" && os.Getenv("GIT_OPTIONAL_LOCKS") != "0" {
		t.Fatal("a write must not set GIT_OPTIONAL_LOCKS=0")
	}
}

func TestErrorCarriesExitCodeAndStderr(t *testing.T) {
	res, err := Client{Dir: t.TempDir()}.Read(context.Background(), "rev-parse", "--git-dir")
	var ge *Error
	if !errors.As(err, &ge) || ge.ExitCode == 0 || res.ExitCode != ge.ExitCode {
		t.Fatalf("want *Error with non-zero exit, got %v", err)
	}
}
