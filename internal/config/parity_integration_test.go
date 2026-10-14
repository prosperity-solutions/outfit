package config_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/testkit"
)

// The §3.0 fallback (`git config -f .outfit/config --get outfit.x`) and outfit
// itself read the SAME committed file. Wherever git can read a file, outfit's
// view of the outfit.* keys must be identical, or `locked` and the team defaults
// would mean different things to users and non-users.
func TestOutfitAgreesWithGitConfigF(t *testing.T) {
	testkit.SkipIfShort(t)
	testkit.Isolate(t)
	corpus := []string{
		"[outfit]\n a = 1\n b = two words\n",
		"\xef\xbb\xbf[outfit]\n a = 1\n",
		"[outfit]\r\n a = 1\r\n b = 2\r\n",
		"[outfit]\n# c \\\n a = 1\n",
		"[outfit]\n a = x\\\\\n b = y\n",
		"[outfit]\n a = x \\\n y\n",
		"[outfit]\n a = x # c \\\n b = y\n",
		"[outfit]\n a = \"q # not a comment\"\n b = 2 ; real comment\n",
		"[outfit]\n bare\n a = 1\n",
		"[OUTFIT]\n A = 1\n",
		"[outfit \"hook\"]\n pre-commit = off\n[outfit]\n a = 1\n",
		"[outfit]\n a = 1\n a = 2\n",
		"[outfit]\n empty =\n quoted = \"\"\n",
		"[outfit]\n a = \"tab\\there\"\n",
		"[core]\n fsmonitor = x\n[outfit]\n a = 1\n",
		"[outfit] a = inline\n[outfit]\n b = 2\n",
		"[outfit]\n locked = a, b\n a = 1\n",
		"[outfit.sub]\n x = 1\n",
		"[outfit \"hook\"]\n a.b = 1\n",   // git rejects: parity only where git succeeds
		"[outfit]\n hook.x = 1\n",         // git rejects the whole file
		"[outfit\"hook\"]\n x = 1\n",      // git rejects
		"[outfit \"\"]\n x = 1\n",         // git reads outfit..x
		"[outfit.hook]\n pre-push = on\n", // deprecated dotted subsection
		"[outfit]\n c = z\\\n w\n",
		"[outfit]\n c = z \\\n   w\n",
	}
	dir := t.TempDir()
	for i, in := range corpus {
		path := filepath.Join(dir, "c"+string(rune('a'+i))+".cfg")
		if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "config", "-f", path, "--list", "-z")
		out, err := cmd.Output()
		if err != nil {
			continue // git rejects the file: no parity is possible, outfit warns instead
		}
		git := map[string]string{}
		for _, rec := range strings.Split(string(out), "\x00") {
			k, v, _ := strings.Cut(rec, "\n")
			if !strings.HasPrefix(k, "outfit.") {
				continue
			}
			if !strings.Contains(rec, "\n") {
				v = "true"
			}
			name := strings.TrimPrefix(k, "outfit.")
			// Only keys outfit accepts take part: git also reads outfit.sub.x,
			// outfit..x and uppercase variants, which outfit deliberately ignores.
			if nk, nerr := config.NormalizeKey(name); nerr != nil || nk != name || name == "locked" || name == "profile" {
				continue
			}
			git[name] = v // last wins, like git config --get
		}

		f, ferr := config.ReadFile(path, config.KindTeam)
		if ferr != nil {
			t.Fatal(ferr)
		}
		var gk, ok []string
		for k := range git {
			gk = append(gk, k)
		}
		for k := range f.Keys {
			ok = append(ok, k)
		}
		sort.Strings(gk)
		sort.Strings(ok)
		if strings.Join(gk, ",") != strings.Join(ok, ",") {
			t.Errorf("input %q:\n  git keys    %v\n  outfit keys %v\n  warnings    %v", in, gk, ok, f.Warnings)
			continue
		}
		for k, v := range git {
			if f.Keys[k] != v {
				t.Errorf("input %q: key %s: git=%q outfit=%q", in, k, v, f.Keys[k])
			}
		}
	}
}
