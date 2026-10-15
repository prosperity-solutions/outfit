package config

import (
	"strings"
	"testing"
)

func TestReplaceLinesOfAProfile(t *testing.T) {
	src := "[outfit]\n    biome = off\n[outfit \"profile\"]\n    replace = .claude/skills\n    replace = docs/agents\n    Replace = third\n    other = x\n    replace\n"
	f := ParseFile(strings.NewReader(src), "profile.config", KindProfile)
	if got := strings.Join(f.Replace, ","); got != ".claude/skills,docs/agents,third" {
		t.Fatalf("Replace = %q", got)
	}
	if f.Keys["biome"] != "off" || len(f.Keys) != 1 {
		t.Fatalf("`replace` must not become a toggle: %v", f.Keys)
	}
	var unknown, bare bool
	for _, w := range f.Warnings {
		unknown = unknown || strings.Contains(w.Message, "unknown key other")
		bare = bare || strings.Contains(w.Message, "needs a directory")
	}
	if !unknown || !bare {
		t.Fatalf("warnings: %+v", f.Warnings)
	}
}

func TestReplaceIsOnlyForProfiles(t *testing.T) {
	f := ParseFile(strings.NewReader("[outfit \"profile\"]\n    replace = skills\n"), ".outfit/config", KindTeam)
	if len(f.Replace) != 0 || len(f.Warnings) != 1 || !strings.Contains(f.Warnings[0].Message, "only valid in a profile's profile.config") {
		t.Fatalf("%+v %+v", f.Replace, f.Warnings)
	}
}
