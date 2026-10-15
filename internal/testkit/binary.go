package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// BuildOutfit compiles the real `outfit` binary (with the given build tags, e.g.
// "faultinject") into a directory of t's and returns its path. The go build cache keeps
// repeated calls quick. Tests that kill or race the real process need a real binary:
// the in-process testscript command shares the test binary's address space.
func BuildOutfit(t testing.TB, tags string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	out := filepath.Join(t.TempDir(), "outfit")
	args := []string{"build", "-o", out}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, "./cmd/outfit")
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build outfit: %v\n%s", err, b)
	}
	return out
}

// Outcome is one run of the real binary.
type Outcome struct {
	Stdout, Stderr string
	Code           int
	Killed         bool // the process died from a signal (fault injection)
}

// RunOutfit runs the binary built by BuildOutfit in the fixture's clone, in its
// hermetic environment, with extra environment entries (e.g. OUTFIT_FAULT_AT=3).
func (r *Repo) RunOutfit(bin string, extraEnv []string, args ...string) Outcome {
	r.T.Helper()
	return r.RunOutfitIn(r.Dir, bin, extraEnv, args...)
}

// RunOutfitIn is RunOutfit in another directory (a linked worktree).
func (r *Repo) RunOutfitIn(dir, bin string, extraEnv []string, args ...string) Outcome {
	r.T.Helper()
	// bin is what BuildOutfit returned: an absolute path to a file named outfit. Anything else
	// is a bug in the test, not something to run.
	if bin = filepath.Clean(bin); !filepath.IsAbs(bin) || filepath.Base(bin) != "outfit" {
		r.T.Fatalf("RunOutfit: %q is not a built outfit binary", bin)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string(nil), r.Env...), extraEnv...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	o := Outcome{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			o.Code = ee.ExitCode()
			o.Killed = o.Code == -1
		} else {
			r.T.Fatalf("running outfit: %v", err)
		}
	}
	return o
}
