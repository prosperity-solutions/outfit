package cli

import (
	"regexp"
	"strings"
	"testing"
)

// The argument syntax of each command is written in `usageTail` (what `-h` prints) and in the
// usage text (what `outfit help` prints): they must say the same thing.
func TestUsageTextAgreesWithEachCommandsHelp(t *testing.T) {
	line := regexp.MustCompile(`(?m)^  ([a-z]+)((?: [^\n]*?)?)(?:  +|\n|$)`)
	for _, m := range line.FindAllStringSubmatch(usage, -1) {
		cmd, args := m[1], strings.TrimSpace(m[2])
		tail, ok := usageTail[cmd]
		if !ok || args == "" {
			continue
		}
		// The usage line shows the same positionals, possibly with flags in brackets after them.
		want := strings.TrimSpace(tail)
		if want != "" && !strings.HasPrefix(args, want) && !strings.HasPrefix(strings.Join(strings.Fields(args), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("outfit %s: `outfit help` says %q but `-h` says %q", cmd, args, want)
		}
	}
}
