package apply

import "testing"

func TestIsExtraLinkIsTheLinkOutfitMakesForThatPathOnly(t *testing.T) {
	personal := []string{"/g/common/outfit/profiles/", "/g/wt/outfit/worktree-profiles/"}
	for _, tc := range []struct {
		rel, text string
		want      bool
	}{
		{"skills/new.md", "../.outfit/profiles/mine/skills/new.md", true},
		{"skills/deep/x.md", "../../.outfit/profiles/mine/skills/deep/x.md", true},
		{"skills/new.md", "../.outfit/profiles/mine/skills/other.md", false},       // an alias for another profile file
		{"skills/my-alias.md", "../.outfit/profiles/mine/skills/new.md", false},    // a hand-made alias
		{"skills/cfg", "../.outfit/profiles/mine/profile.config", false},           // the profile's own config
		{"skills/new.md", "../../.outfit/profiles/mine/skills/new.md", false},      // wrong depth
		{"skills/new.md", "../.outfit/profiles//skills/new.md", false},             // no profile name
		{"skills/new.md", "/g/common/outfit/profiles/yolo/skills/new.md", true},    // a personal profile
		{"skills/new.md", "/g/wt/outfit/worktree-profiles/w/skills/new.md", true},  // a worktree profile
		{"skills/new.md", "/g/common/outfit/profiles/yolo/skills/other.md", false}, // another file of it
		{"skills/new.md", "/somewhere/else.md", false},
	} {
		if got := isExtraLink(tc.rel, tc.text, personal); got != tc.want {
			t.Errorf("isExtraLink(%q, %q) = %v, want %v", tc.rel, tc.text, got, tc.want)
		}
	}
}

func TestTheTeamsPathsClashWithAnExtra(t *testing.T) {
	idx := map[string]indexEntry{
		"skills/a.md":        {tracked: true},
		"skills/deep/y.md":   {tracked: true},
		"skills/Readme.md":   {tracked: true},
		"elsewhere/other.md": {tracked: true},
	}
	tr := newTrackedIdx(idx, []string{"skills"})
	for p, wantClash := range map[string]bool{
		"skills/new.md":        false,
		"skills/a.md/x.md":     true,  // a file stands where a directory would have to
		"skills/deep":          true,  // the team tracks files below the path
		"skills/deep/z.md":     false, // a new file in a directory the team has
		"skills/readme.md":     true,  // differs from a tracked file only in case
		"skills/other/new.md":  false,
		"skills/Readme.md/q":   true,
		"elsewhere/other.md/x": false, // not below a replaced directory: nobody asks
	} {
		if got := tr.clash(p) != ""; got != wantClash {
			t.Errorf("clash(%q) = %v, want %v (%q)", p, got, wantClash, tr.clash(p))
		}
	}
}

func TestReservedNamesAreGitsOwn(t *testing.T) {
	for p, want := range map[string]bool{
		"skills/.gitignore": true, "skills/deep/.GitIgnore": true, "skills/.gitattributes": true,
		"skills/.gitmodules": true, "skills/gitignore": false, "skills/a.md": false,
	} {
		if reservedExtraName(p) != want {
			t.Errorf("reservedExtraName(%q) != %v", p, want)
		}
	}
}
