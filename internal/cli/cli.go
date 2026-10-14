// Package cli holds flag parsing, human/JSON output and exit codes. Commands
// are a plain switch over os.Args: no cobra.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/prosperity-solutions/outfit/internal/gitx"
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
	return Execute(ctx, Env{Build: b, Dir: dir, Getenv: os.Getenv, Stdout: stdout, Stderr: stderr}, args)
}

const usage = `usage: outfit <command> [flags]

commands:
  get <key>              print a toggle's value (exit 1 when unset)
  set <key>=<val>…       personal toggle override; key= unsets   [--here | --global]
  ls                     list profiles across the three tiers
  status                 the active profile and where every value comes from
  version                version, commit, build date, git version found

every command accepts --json. Run 'outfit <command> -h' for its flags.`

// Execute runs one command and returns its exit code.
func Execute(ctx context.Context, e Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(e.Stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "version":
		return cmdVersion(ctx, e, args[1:])
	case "get":
		return cmdGet(ctx, e, args[1:])
	case "set":
		return cmdSet(ctx, e, args[1:])
	case "ls":
		return cmdLs(ctx, e, args[1:])
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
