// Package cli holds flag parsing, human/JSON output and exit codes. Commands
// are a plain switch over os.Args: no cobra.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"aead.dev/minisign"

	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/update"
)

// Build carries the release build's identity, set by ldflags in package main.
type Build struct {
	Version string
	Commit  string
	Date    string
}

// Env is everything a command needs from the outside world.
type Env struct {
	Build  Build
	Dir    string // working directory
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	// Stdin is passed through to chained hooks (pre-push reads its refs there).
	Stdin io.Reader
	// Exe is the absolute path of the running binary, recorded in hook shims.
	Exe string
	// IsTTY reports whether stderr is a terminal (the update banner only shows
	// there).
	IsTTY bool
	// GHToken returns the output of `gh auth token`; nil means do not look.
	GHToken func() string
	// HTTP and UpdateKeys are for tests of `update`; nil means the defaults (the
	// system client, the keys compiled in from internal/update/keys.txt).
	HTTP       *http.Client
	UpdateKeys []minisign.PublicKey
}

// Main runs the CLI against the real process environment and returns the exit
// code.
func Main(b Build) int {
	return Run(context.Background(), b, os.Args[1:], os.Stdout, os.Stderr)
}

// Run is Execute with the process's working directory and environment.
func Run(ctx context.Context, b Build, args []string, stdout, stderr io.Writer) int {
	dir, err := os.Getwd()
	if err != nil {
		return report(stdout, stderr, false, Errorf("cannot determine the working directory: %v", err))
	}
	return Execute(ctx, Env{Build: b, Dir: dir, Getenv: os.Getenv, Stdout: stdout, Stderr: stderr, Stdin: os.Stdin,
		Exe: stableExe(), IsTTY: isTTY(os.Stderr), GHToken: update.GHAuthToken}, args)
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// stableExe is the path recorded in hook shims. Package managers install a
// versioned file (/opt/homebrew/Cellar/outfit/1.2.3/bin/outfit) and link a stable
// name to it (/opt/homebrew/bin/outfit); the versioned path disappears on the
// next upgrade and the shim would fall back to a PATH lookup. So when `outfit` on
// PATH is this very binary, record that name, otherwise the resolved path.
func stableExe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		real = exe
	}
	if p, err := exec.LookPath("outfit"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			a, aerr := os.Stat(abs)
			b, berr := os.Stat(real)
			if aerr == nil && berr == nil && os.SameFile(a, b) {
				return abs
			}
		}
	}
	return real
}

const usage = `usage: outfit <command> [flags]

commands:
  init [--dry-run]       install the hook dispatcher (git hooks keep working)
  uninstall              remove everything init installed, in every worktree
  get <key>              print a toggle's value (exit 1 when unset)
  set <key>=<val>…       personal toggle override; key= unsets   [--here | --global]
  ls                     list profiles across the three tiers
  status                 the active profile and where every value comes from
  update                 install the latest release (or --version vX.Y.Z); --check only looks
  version                version, commit, build date, git version found

every command accepts --json (plumbing: "outfit hook <name>", run by git). Run 'outfit <command> -h' for its flags.`

// Execute runs one command and returns its exit code. Human-facing commands may
// then print the update banner (HANDOVER.md §5), never the ones git hooks run.
func Execute(ctx context.Context, e Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(e.Stderr, usage)
		return ExitUsage
	}
	if !update.BannerCommands[args[0]] || !bannerEnabled(e, args) {
		return dispatch(ctx, e, args)
	}
	b := newBanner(e)
	b.Start(ctx)
	code := dispatch(ctx, e, args)
	if line := b.Finish(); line != "" {
		fmt.Fprintln(e.Stderr, line)
	}
	return code
}

func dispatch(ctx context.Context, e Env, args []string) int {
	switch args[0] {
	case "update":
		return cmdUpdate(ctx, e, args[1:])
	case "version":
		return cmdVersion(ctx, e, args[1:])
	case "get":
		return cmdGet(ctx, e, args[1:])
	case "set":
		return cmdSet(ctx, e, args[1:])
	case "ls":
		return cmdLs(ctx, e, args[1:])
	case "init":
		return cmdInit(ctx, e, args[1:])
	case "uninstall":
		return cmdUninstall(ctx, e, args[1:])
	case "hook":
		return cmdHook(ctx, e, args[1:])
	case "status":
		return cmdStatus(ctx, e, args[1:])
	case "-h", "--help", "help":
		fmt.Fprintln(e.Stdout, usage)
		return ExitOK
	default:
		return report(e.Stdout, e.Stderr, wantsJSON(args),
			Usagef("unknown command %q", args[0]).WithFix("outfit help"))
	}
}

type versionInfo struct {
	Version string  `json:"version"`
	Commit  string  `json:"commit"`
	Date    string  `json:"date"`
	OS      string  `json:"os"`
	Arch    string  `json:"arch"`
	Git     gitInfo `json:"git"`
}

type gitInfo struct {
	Version string `json:"version,omitempty"`
	OK      bool   `json:"ok"`
	Warning string `json:"warning,omitempty"`
}

// cmdVersion never fails because of git: install.sh prints it as its last step,
// so a missing or old git is reported as a warning, not an error.
func cmdVersion(ctx context.Context, e Env, args []string) int {
	fs := newFlags("version")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	if code, bad := noArgs(e, "version", pos, args); bad {
		return code
	}

	b := e.Build
	info := versionInfo{Version: b.Version, Commit: b.Commit, Date: b.Date, OS: runtime.GOOS, Arch: runtime.GOARCH}
	v, _, err := gitx.Client{}.Version(ctx)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		info.Git.Warning = fmt.Sprintf("git not found on PATH: install git %s or newer", gitx.Floor)
	case err != nil:
		info.Git.Warning = fmt.Sprintf("could not read the git version: %v", err)
	case gitx.CheckFloor(v) != nil:
		info.Git = gitInfo{Version: v.String(), Warning: gitx.CheckFloor(v).Error()}
	default:
		info.Git = gitInfo{Version: v.String(), OK: true}
	}

	if *asJSON {
		out, _ := json.Marshal(info)
		fmt.Fprintln(e.Stdout, string(out))
		return ExitOK
	}
	gv := info.Git.Version
	if gv == "" {
		gv = "not found"
	}
	fmt.Fprintf(e.Stdout, "outfit %s (commit %s, built %s, %s/%s, git %s)\n",
		info.Version, info.Commit, info.Date, info.OS, info.Arch, gv)
	if info.Git.Warning != "" {
		fmt.Fprintf(e.Stderr, "warning: %s\n", info.Git.Warning)
	}
	return ExitOK
}
