// Package cli holds flag parsing, human/JSON output and exit codes. Commands
// are a plain switch over os.Args: no cobra.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/prosperity-solutions/outfit/internal/gitx"
)

// Build carries the release build's identity, set by ldflags in package main.
type Build struct {
	Version string
	Commit  string
	Date    string
}

// Main runs the CLI against the real process environment and returns the exit
// code.
func Main(b Build) int {
	return Run(context.Background(), b, os.Args[1:], os.Stdout, os.Stderr)
}

// Run is Main with injectable streams, for tests.
func Run(ctx context.Context, b Build, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: outfit <command> [flags]\ncommands: version\nrun `outfit version` to see what is installed")
		return ExitUsage
	}
	switch args[0] {
	case "version":
		return cmdVersion(ctx, b, args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, "usage: outfit <command> [flags]\ncommands: version")
		return ExitOK
	default:
		return report(stdout, stderr, false, Usagef("unknown command %q", args[0]).
			WithFix("outfit help"))
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
func cmdVersion(ctx context.Context, b Build, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	info := versionInfo{Version: b.Version, Commit: b.Commit, Date: b.Date, OS: runtime.GOOS, Arch: runtime.GOARCH}
	v, raw, err := gitx.Client{}.Version(ctx)
	switch {
	case err != nil:
		info.Git.Warning = "git not found on PATH: install git 2.36 or newer"
	case gitx.CheckFloor(v) != nil:
		info.Git = gitInfo{Version: v.String(), Warning: gitx.CheckFloor(v).Error()}
	default:
		info.Git = gitInfo{Version: v.String(), OK: true}
	}
	_ = raw

	if *asJSON {
		out, _ := json.Marshal(info)
		fmt.Fprintln(stdout, string(out))
		return ExitOK
	}
	gv := info.Git.Version
	if gv == "" {
		gv = "not found"
	}
	fmt.Fprintf(stdout, "outfit %s (commit %s, built %s, %s/%s, git %s)\n",
		info.Version, info.Commit, info.Date, info.OS, info.Arch, gv)
	if info.Git.Warning != "" {
		fmt.Fprintf(stderr, "warning: %s\n", info.Git.Warning)
	}
	return ExitOK
}
