package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
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

// wantsJSON reports whether args ask for JSON output in any spelling the flag
// package accepts (-json, --json, --json=true), even when parsing then fails.
func wantsJSON(args []string) bool {
	for _, a := range args {
		switch a {
		case "-json", "--json", "-json=true", "--json=true":
			return true
		}
	}
	return false
}

// usageTail is each command's positional arguments, for `-h`.
var usageTail = map[string]string{"get": " <key>", "set": " <key>=<val>…", "ls": "", "status": "", "version": ""}

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
		fmt.Fprintf(e.Stdout, "usage: outfit %s%s [flags]\n", fs.Name(), usageTail[fs.Name()])
		fs.VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(e.Stdout, "  --%s\t%s\n", f.Name, f.Usage)
		})
		return nil, ExitOK, true
	default:
		return nil, report(e.Stdout, e.Stderr, wantsJSON(args),
			Usagef("%v", err).WithFix("outfit "+fs.Name()+" -h")), true
	}
}

// noArgs reports a usage error when a command that takes no positionals got some.
func noArgs(e Env, name string, pos, args []string) (int, bool) {
	if len(pos) == 0 {
		return 0, false
	}
	return report(e.Stdout, e.Stderr, wantsJSON(args),
		Usagef("outfit %s takes no arguments, got %q", name, pos[0]).WithFix("outfit "+name+" -h")), true
}
