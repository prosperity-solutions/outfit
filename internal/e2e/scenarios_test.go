package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestScenarioMapIsReal keeps docs/testing.md honest: every scenario of HANDOVER.md §7.4
// (1-21) has a row, and every file or test a row names exists.
func TestScenarioMapIsReal(t *testing.T) {
	doc, err := os.ReadFile("../../docs/testing.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`^\| (\d+) \|`)
	name := regexp.MustCompile("`([^`]+)`")
	// Every Test*/Fuzz* function in the tree.
	funcs := map[string]bool{}
	fn := regexp.MustCompile(`(?m)^func ((?:Test|Fuzz)\w+)\(`)
	_ = filepath.WalkDir("../..", func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, "_test.go") {
			if b, rerr := os.ReadFile(p); rerr == nil {
				for _, m := range fn.FindAllStringSubmatch(string(b), -1) {
					funcs[m[1]] = true
				}
			}
		}
		return nil
	})
	seen := map[int]bool{}
	for _, line := range strings.Split(string(doc), "\n") {
		m := row.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		seen[n] = true
		cells := strings.Split(line, "|")
		pinned := cells[len(cells)-2]
		found := false
		for _, nm := range name.FindAllStringSubmatch(pinned, -1) {
			found = true
			switch {
			case strings.HasSuffix(nm[1], ".txtar"):
				if _, err := os.Stat(filepath.Join("../../testdata/script", nm[1])); err != nil {
					t.Errorf("scenario %d names %s, which does not exist", n, nm[1])
				}
			case strings.HasPrefix(nm[1], "Test") || strings.HasPrefix(nm[1], "Fuzz"):
				if !funcs[nm[1]] {
					t.Errorf("scenario %d names %s, which does not exist", n, nm[1])
				}
			}
		}
		if !found {
			t.Errorf("scenario %d names nothing that pins it", n)
		}
	}
	for n := 1; n <= 21; n++ {
		if !seen[n] {
			t.Errorf("scenario %d is missing from docs/testing.md", n)
		}
	}
}
