package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
)

// parse parses flags that may appear before OR after positional arguments
// (`outfit get biome --json` works) and returns the positionals. All outfit
// flags are booleans, so a flag never consumes the next argument.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, pos []string
	for i, a := range args {
		switch {
		case a == "--":
			pos = append(pos, args[i+1:]...)
			return pos, fs.Parse(flags)
		case strings.HasPrefix(a, "-") && a != "-":
			flags = append(flags, a)
		default:
			pos = append(pos, a)
		}
	}
	return pos, fs.Parse(flags)
}

// newFlags makes a FlagSet that prints nothing itself: errors go through report
// (so --json callers get JSON) and -h through parseArgs.
func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseArgs parses args; when it returns stop=true the command must return code.
func parseArgs(e Env, fs *flag.FlagSet, args []string) (pos []string, code int, stop bool) {
	pos, err := parse(fs, args)
	switch {
	case err == nil:
		return pos, 0, false
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprintf(e.Stdout, "usage: outfit %s [flags]\n", fs.Name())
		fs.SetOutput(e.Stdout)
		fs.PrintDefaults()
		return nil, ExitOK, true
	default:
		return nil, report(e.Stdout, e.Stderr, slices.Contains(args, "--json"),
			Usagef("%v", err).WithFix("outfit "+fs.Name()+" -h")), true
	}
}
