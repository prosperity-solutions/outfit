package plan

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestSparseTextShape(t *testing.T) {
	got := SparseText(SparseOptions{Hidden: []string{"CLAUDE.md", "AGENTS.md", "apps/web/AGENTS.md"}, HideProfiles: true, Show: []string{"yolo"}})
	want := Marker + "\n/*\n!/AGENTS.md\n!/CLAUDE.md\n!/apps/web/AGENTS.md\n!/.outfit/profiles/*\n/.outfit/profiles/yolo/\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	// Base with committed profiles: everything hidden, nothing included back.
	if got := SparseText(SparseOptions{HideProfiles: true}); got != Marker+"\n/*\n!/.outfit/profiles/*\n" {
		t.Fatalf("base: %q", got)
	}
	// Author mode: profiles stay on disk.
	if got := SparseText(SparseOptions{Hidden: []string{"AGENTS.md"}}); strings.Contains(got, ".outfit") {
		t.Fatalf("author mode hid profiles: %q", got)
	}
}

func sortedCopy(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func TestSparseRoundTrip(t *testing.T) {
	for _, o := range []SparseOptions{
		{},
		{Hidden: []string{"AGENTS.md"}},
		{Hidden: []string{"a b/c.md", "x*y", "[z]", "!bang", "#hash", `back\slash`, "trailing "}, HideProfiles: true, Show: []string{"my.profile-1"}},
		{HideProfiles: true},
		{HideProfiles: true, Show: []string{"b", "a.c", "yolo"}},
	} {
		text := SparseText(o)
		got, ok := ParseSparse(text)
		if !ok {
			t.Fatalf("%+v: not parsed back: %q", o, text)
		}
		sort.Strings(o.Hidden)
		if !reflect.DeepEqual(got.Hidden, o.Hidden) && (len(got.Hidden) != 0 || len(o.Hidden) != 0) {
			t.Errorf("hidden: %q != %q", got.Hidden, o.Hidden)
		}
		if got.HideProfiles != o.HideProfiles || !reflect.DeepEqual(got.Show, sortedCopy(o.Show)) {
			t.Errorf("%+v != %+v", got, o)
		}
		// Rendering twice gives the same text (an unchanged file is never rewritten).
		if SparseText(got) != text {
			t.Errorf("not a fixed point:\n%q\n%q", text, SparseText(got))
		}
	}
	for _, bad := range []string{"", "/*\n", "# some other file\n/*\n", Marker + "\n!/x\n", Marker + "\n/*\n/odd\n"} {
		if _, ok := ParseSparse(bad); ok {
			t.Errorf("%q parsed as managed", bad)
		}
	}
}

func TestEscapedPathsStayLiteral(t *testing.T) {
	for in, want := range map[string]string{
		"plain.md":  "plain.md",
		"a*b":       `a\*b`,
		"a?b":       `a\?b`,
		"[x]":       `\[x\]`,
		"!x":        `\!x`,
		"#x":        `\#x`,
		"a!b":       "a!b",
		`a\b`:       `a\\b`,
		"trail ":    `trail\ `,
		"d/e f.md":  "d/e f.md",
		"café/é.md": "café/é.md",
	} {
		if got := escapePattern(in); got != want {
			t.Errorf("escapePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidSwapPath(t *testing.T) {
	for _, ok := range []string{"AGENTS.md", "apps/web/AGENTS.md", "a b.md", "café.md", ".claude/CLAUDE.md", "dir/.hidden"} {
		if err := ValidSwapPath(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "/abs", "../up", "a/../b", "a//b", "a/", "./a", ".git/config", "x/.GIT/y", ".outfit/config", ".outfit", "a\nb", "a\x00b", "a\rb", "\x7f"} {
		if err := ValidSwapPath(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// FuzzSparse: whatever paths come in, the text is a fixed point of parse+render
// and never contains a line it cannot read back.
func FuzzSparse(f *testing.F) {
	f.Add("AGENTS.md", "yolo")
	f.Add("a b/*.md", "x")
	f.Add("!#\\[x]", "p.q")
	f.Fuzz(func(t *testing.T, path, active string) {
		if ValidSwapPath(path) != nil || strings.ContainsAny(active, "/\n\r\x00") || active == "" {
			return
		}
		o := SparseOptions{Hidden: []string{path}, HideProfiles: true, Show: []string{active}}
		text := SparseText(o)
		got, ok := ParseSparse(text)
		if !ok || SparseText(got) != text {
			t.Fatalf("path %q active %q: text %q does not round-trip (%+v, %v)", path, active, text, got, ok)
		}
	})
}
