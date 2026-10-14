package cli

import (
	"flag"
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
