package config

import (
	"reflect"
	"strings"
	"testing"
)

func file(kind Kind, text string) *File { return ParseFile(strings.NewReader(text), "f", kind) }

func entries(es ...Entry) GitConfig { return GitConfig{Entries: es} }

func TestPrecedenceFirstHitWins(t *testing.T) {
	// Every layer sets x; peel them off one at a time.
	team := file(KindTeam, "[outfit]\n x = team\n")
	prof := file(KindProfile, "[outfit]\n x = profile\n")
	all := []Entry{
		{ScopeGlobal, "x", "global"},
		{ScopeLocal, "x", "local"},
		{ScopeWorktree, "x", "worktree"},
	}
	cases := []struct {
		name    string
		git     []Entry
		profile *File
		team    *File
		want    string
		source  string
	}{
		{"worktree wins", all, prof, team, "worktree", SourceWorktree},
		{"then local", all[:2], prof, team, "local", SourceLocal},
		{"then global", all[:1], prof, team, "global", SourceGlobal},
		{"then profile", nil, prof, team, "profile", SourceProfile},
		{"then team", nil, nil, team, "team", SourceTeam},
	}
	for _, c := range cases {
		res := Resolve(Input{Git: entries(c.git...), Team: c.team, Profile: c.profile})
		got, ok := res.Get("x")
		if !ok || got.Value != c.want || got.Source != c.source || got.Locked {
			t.Errorf("%s: got %+v", c.name, got)
		}
	}
	res := Resolve(Input{Git: entries(all...), Team: team, Profile: prof})
	got, _ := res.Get("x")
	if len(got.Shadowed) != 4 || got.Shadowed[0].Source != SourceLocal || got.Shadowed[3].Source != SourceTeam {
		t.Fatalf("shadowed = %+v", got.Shadowed)
	}
}

func TestLaterEntryInTheSameScopeWins(t *testing.T) {
	res := Resolve(Input{Git: entries(Entry{ScopeLocal, "x", "1"}, Entry{ScopeLocal, "x", "2"})})
	if got, _ := res.Get("x"); got.Value != "2" {
		t.Fatalf("got %+v", got)
	}
}

func TestUnsetKeyIsAbsent(t *testing.T) {
	res := Resolve(Input{Team: file(KindTeam, "[outfit]\n a = 1\n")})
	if _, ok := res.Get("nope"); ok {
		t.Fatal("unset key must be absent")
	}
}

// HANDOVER.md scenario 13: a locked key resolves from .outfit/config alone.
func TestLockedKeyCannotBeOverriddenFromAnyScopeOrProfile(t *testing.T) {
	team := file(KindTeam, "[outfit]\n locked = secrets-scan, hook.pre-push\n secrets-scan = on\n hook.pre-push = on\n")
	prof := file(KindProfile, "[outfit]\n secrets-scan = off\n")
	g := entries(
		Entry{ScopeGlobal, "secrets-scan", "off"},
		Entry{ScopeLocal, "secrets-scan", "off"},
		Entry{ScopeWorktree, "secrets-scan", "off"},
		Entry{ScopeWorktree, "hook.pre-push", "off"},
	)
	res := Resolve(Input{Git: g, Team: team, Profile: prof})
	for _, k := range []string{"secrets-scan", "hook.pre-push"} {
		got, ok := res.Get(k)
		if !ok || got.Value != "on" || got.Source != SourceTeam || !got.Locked {
			t.Errorf("%s: got %+v", k, got)
		}
	}
	got, _ := res.Get("secrets-scan")
	if len(got.Shadowed) != 4 {
		t.Errorf("ignored overrides must be listed: %+v", got.Shadowed)
	}
	warns := 0
	for _, w := range res.Warnings {
		if strings.Contains(w.Message, "is locked by .outfit/config") {
			warns++
		}
	}
	if warns != 5 {
		t.Errorf("want one warning per ignored override (5), got %d: %v", warns, res.Warnings)
	}
	if !reflect.DeepEqual(res.Locked, []string{"hook.pre-push", "secrets-scan"}) || !res.IsLocked("secrets-scan") {
		t.Errorf("locked = %v", res.Locked)
	}
}

func TestLockedWithoutTeamValueResolvesToNothing(t *testing.T) {
	team := file(KindTeam, "[outfit]\n locked = x\n")
	res := Resolve(Input{Git: entries(Entry{ScopeLocal, "x", "off"}), Team: team})
	if _, ok := res.Get("x"); ok {
		t.Fatal("a locked key the team never set must not resolve from a personal override")
	}
}

func TestPersonalLockedIsIgnoredWithWarning(t *testing.T) {
	res := Resolve(Input{Git: entries(Entry{ScopeLocal, "locked", "biome"}, Entry{ScopeLocal, "biome", "off"})})
	if res.IsLocked("biome") {
		t.Fatal("only .outfit/config can lock")
	}
	if _, ok := res.Get("locked"); ok {
		t.Fatal("locked is not a toggle")
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}

func TestChooseProfile(t *testing.T) {
	cases := []struct {
		name string
		es   []Entry
		want ProfileChoice
		warn bool
	}{
		{"nothing is base", nil, ProfileChoice{Name: Base, Source: "default"}, false},
		{"local", []Entry{{ScopeLocal, "profile", "lean"}}, ProfileChoice{Name: "lean", Source: SourceLocal}, false},
		{"worktree beats local and remembers the clone default",
			[]Entry{{ScopeLocal, "profile", "lean"}, {ScopeWorktree, "profile", "yolo"}},
			ProfileChoice{Name: "yolo", Source: SourceWorktree, CloneDefault: "lean"}, false},
		{"same in both is not a shadow",
			[]Entry{{ScopeLocal, "profile", "lean"}, {ScopeWorktree, "profile", "lean"}},
			ProfileChoice{Name: "lean", Source: SourceWorktree}, false},
		{"global is never used", []Entry{{ScopeGlobal, "profile", "yolo"}}, ProfileChoice{Name: Base, Source: "default"}, true},
		{"global loses to local", []Entry{{ScopeGlobal, "profile", "yolo"}, {ScopeLocal, "profile", "lean"}}, ProfileChoice{Name: "lean", Source: SourceLocal}, true},
		{"empty value is unset", []Entry{{ScopeLocal, "profile", ""}}, ProfileChoice{Name: Base, Source: "default"}, false},
	}
	for _, c := range cases {
		got, warns := ChooseProfile(entries(c.es...))
		if got != c.want || (len(warns) > 0) != c.warn {
			t.Errorf("%s: got %+v warns=%v, want %+v warn=%v", c.name, got, warns, c.want, c.warn)
		}
	}
}

func TestParseGitList(t *testing.T) {
	raw := "global\x00outfit.biome\noff\x00" +
		"global\x00user.name\nsomeone\x00" +
		"local\x00extensions.worktreeconfig\ntrue\x00" +
		"local\x00outfit.hook.pre-commit\noff\x00" +
		"local\x00outfit.flag\x00" + // bare key: true
		"worktree\x00outfit.multi\nline1\nline2\x00" +
		"local\x00outfit.Bad_Key\nx\x00" +
		"local\x00outfit.profile\nyolo\x00"
	gc := ParseGitList([]byte(raw))
	want := []Entry{
		{ScopeGlobal, "biome", "off"},
		{ScopeLocal, "hook.pre-commit", "off"},
		{ScopeLocal, "flag", "true"},
		{ScopeWorktree, "multi", "line1\nline2"},
		{ScopeLocal, "profile", "yolo"},
	}
	if !reflect.DeepEqual(gc.Entries, want) {
		t.Fatalf("entries = %+v\nwant     %+v", gc.Entries, want)
	}
	if !gc.WorktreeConfig {
		t.Fatal("extensions.worktreeConfig must be detected")
	}
	if len(gc.Warnings) != 1 {
		t.Fatalf("warnings = %v", gc.Warnings)
	}
	if got := ParseGitList(nil); len(got.Entries) != 0 {
		t.Fatal("empty input")
	}
	// Truncated input must not panic.
	_ = ParseGitList([]byte("local\x00outfit.x"))
	_ = ParseGitList([]byte("local"))
}

func TestGitListFlagsSawLocalAndLocalOnlyWorktreeConfig(t *testing.T) {
	gc := ParseGitList([]byte("global\x00extensions.worktreeconfig\ntrue\x00"))
	if gc.WorktreeConfig || gc.SawLocal {
		t.Fatalf("worktreeConfig only counts from the repo's own config: %+v", gc)
	}
	gc = ParseGitList([]byte("local\x00core.bare\nfalse\x00local\x00extensions.worktreeconfig\nTrue\x00"))
	if !gc.WorktreeConfig || !gc.SawLocal {
		t.Fatalf("%+v", gc)
	}
	// git keeps the subsection's case: `outfit.Hook.x` is a different key.
	gc = ParseGitList([]byte("local\x00outfit.Hook.pre-commit\noff\x00"))
	if len(gc.Entries) != 0 || len(gc.Warnings) != 1 {
		t.Fatalf("%+v", gc)
	}
}

func TestWarningsAreDeterministic(t *testing.T) {
	team := file(KindTeam, "[outfit]\n locked = a, b, c, d, e, f\n a = 1\n b = 1\n c = 1\n d = 1\n e = 1\n f = 1\n")
	var es []Entry
	for _, k := range []string{"f", "a", "e", "b", "d", "c"} {
		es = append(es, Entry{ScopeLocal, k, "x"})
	}
	first := Resolve(Input{Git: entries(es...), Team: team}).Warnings
	for i := 0; i < 20; i++ {
		got := Resolve(Input{Git: entries(es...), Team: team}).Warnings
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("warnings order changed:\n%v\n%v", first, got)
		}
	}
}

func TestSystemScopeAndCommandProfileWarn(t *testing.T) {
	res := Resolve(Input{Git: entries(Entry{ScopeSystem, "biome", "off"}, Entry{ScopeCommand, "profile", "yolo"})})
	if _, ok := res.Get("biome"); ok {
		t.Fatal("system scope must not resolve")
	}
	if res.Profile.Name != Base || len(res.Warnings) != 2 {
		t.Fatalf("%+v %v", res.Profile, res.Warnings)
	}
}
