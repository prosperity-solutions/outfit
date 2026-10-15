package config

import (
	"reflect"
	"strings"
	"testing"
)

// FuzzParseFile (HANDOVER.md §7.5): whatever bytes a teammate commits, the parser never
// panics, is deterministic, and accepts ONLY toggle keys: nothing that could name a git
// setting (core.*, include.*, alias.*…) ever reaches Keys, and `profile` / `locked`
// never appear where they are not allowed.
func FuzzParseFile(f *testing.F) {
	for _, seed := range []string{
		"[outfit]\n\tbiome = off\n",
		"[outfit]\n\tlocked = secrets-scan, hook.pre-push\n\tsecrets-scan = on\n",
		"[outfit \"hook\"]\n\tpre-commit = off\n",
		"[core]\n\tfsmonitor = /tmp/evil\n[include]\n\tpath = /etc/passwd\n[includeIf \"gitdir:/\"]\n\tpath = x\n",
		"[outfit]\n\thook.pre-commit = off\n\tprofile = yolo\n",
		"\xef\xbb\xbf[outfit]\nlocked = a\n",
		"[outfit]\nbiome = \"unterminated\n",
		"[outfit]\nbiome = a \\\n  b\n",
		"[ outfit ]\n x = 1\n[outfit.]\n y = 2\n",
		"[outfit \"a\\\"b\"]\nz = 1\n",
	} {
		f.Add(seed, false)
		f.Add(seed, true)
	}
	f.Fuzz(func(t *testing.T, text string, profile bool) {
		kind := KindTeam
		if profile {
			kind = KindProfile
		}
		a := ParseFile(strings.NewReader(text), "fuzz", kind)
		b := ParseFile(strings.NewReader(text), "fuzz", kind)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("not deterministic for %q", text)
		}
		for k := range a.Keys {
			n, err := NormalizeKey(k)
			if err != nil || n != k {
				t.Fatalf("key %q was accepted but is not a toggle key (%v)", k, err)
			}
			if k == KeyProfile || k == KeyLocked {
				t.Fatalf("reserved key %q ended up in Keys for %q", k, text)
			}
			if strings.HasPrefix(k, "core") || strings.Contains(k, "include") || strings.Contains(k, "alias") || strings.Contains(k, "fsmonitor") {
				t.Fatalf("a git setting leaked into Keys: %q (from %q)", k, text)
			}
		}
		if kind == KindProfile && len(a.Locked) != 0 {
			t.Fatalf("a profile file locked keys: %v", a.Locked)
		}
		for _, l := range a.Locked {
			if n, err := NormalizeKey(l); err != nil || n != l {
				t.Fatalf("locked entry %q is not a valid key", l)
			}
		}
	})
}

// FuzzNormalizeKey: the key whitelist. Whatever comes in, an accepted key is one
// canonical lowercase toggle name and normalising it again changes nothing.
func FuzzNormalizeKey(f *testing.F) {
	for _, s := range []string{"biome", "hook.pre-commit", "Hook.Pre-Commit", " tests ", "core.fsmonitor", "a.b", "", "x\x00y", "-a", "hook.", "ünï"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, k string) {
		n, err := NormalizeKey(k)
		if err != nil {
			return
		}
		if n2, err := NormalizeKey(n); err != nil || n2 != n {
			t.Fatalf("%q -> %q is not a fixed point (%q, %v)", k, n, n2, err)
		}
		if strings.ToLower(n) != n || strings.ContainsAny(n, " \t\r\n\x00=/\\\"'") {
			t.Fatalf("%q normalised to %q, which is not a plain toggle name", k, n)
		}
		if strings.Count(n, ".") > 1 || (strings.Contains(n, ".") && !strings.HasPrefix(n, HookPrefix)) {
			t.Fatalf("%q normalised to %q: a dotted key that is not hook.<name>", k, n)
		}
	})
}
