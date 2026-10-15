package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyAddsTheLineAndTheDocOnceAndLeavesTheRestAlone(t *testing.T) {
	d := t.TempDir()
	write(t, d, "AGENTS.md", "# Agents\n\nBe kind.")
	r, err := Apply(d, false, false)
	if err != nil || r.AgentsMD != Added || r.Doc != Created {
		t.Fatalf("%v %+v", err, r)
	}
	got := read(t, d, "AGENTS.md")
	if got != "# Agents\n\nBe kind.\n\n"+AgentsLine+"\n" {
		t.Fatalf("AGENTS.md: %q", got)
	}
	if doc := read(t, d, DocPath); !strings.Contains(doc, "install.sh | sh") || !strings.Contains(doc, "git config -f .outfit/config --get outfit.biome") {
		t.Fatalf("docs/outfit.md lacks the install command or the fallback: %q", doc)
	}
	// Twice changes nothing; a document the team edited stays as it is.
	write(t, d, DocPath, "ours\n")
	r, err = Apply(d, false, false)
	if err != nil || r.AgentsMD != Present || r.Doc != Exists || read(t, d, DocPath) != "ours\n" || read(t, d, "AGENTS.md") != got {
		t.Fatalf("second run: %v %+v", err, r)
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	d := t.TempDir()
	write(t, d, "AGENTS.md", "x\n")
	r, err := Apply(d, true, false)
	if err != nil || r.AgentsMD != Added || r.Doc != Created || !r.DryRun {
		t.Fatalf("%v %+v", err, r)
	}
	if read(t, d, "AGENTS.md") != "x\n" {
		t.Fatal("dry run changed AGENTS.md")
	}
	if _, err := os.Stat(filepath.Join(d, "docs")); !os.IsNotExist(err) {
		t.Fatal("dry run made docs/")
	}
}

func TestApplyNoAgentsFileAndASwappedOne(t *testing.T) {
	d := t.TempDir()
	r, err := Apply(d, false, false)
	if err != nil || r.AgentsMD != Missing || r.Doc != Created {
		t.Fatalf("%v %+v", err, r)
	}
	d = t.TempDir()
	write(t, d, ".outfit/profiles/p/AGENTS.md", "mine\n")
	if err := os.Symlink(".outfit/profiles/p/AGENTS.md", filepath.Join(d, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	r, err = Apply(d, false, true)
	if err != nil || r.AgentsMD != Swapped || r.Doc != "" {
		t.Fatalf("%v %+v", err, r)
	}
	if _, serr := os.Stat(filepath.Join(d, "docs")); !os.IsNotExist(serr) {
		t.Fatal("a swapped AGENTS.md must write nothing at all")
	}
	if read(t, d, ".outfit/profiles/p/AGENTS.md") != "mine\n" {
		t.Fatal("the profile's file was edited through the link")
	}
}

func TestApplyNeverWritesThroughALinkedDocsFolder(t *testing.T) {
	d, out := t.TempDir(), t.TempDir()
	if err := os.Symlink(out, filepath.Join(d, "docs")); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(d, false, false); err == nil {
		t.Fatal("expected an error")
	}
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Fatalf("wrote through the link: %v", ents)
	}
}

func TestApplyFollowsATeamLinkInsideTheWorktreeButNeverAProfilesLink(t *testing.T) {
	d := t.TempDir()
	write(t, d, "CLAUDE.md", "# Claude\r\nrules\r\n")
	if err := os.Symlink("CLAUDE.md", filepath.Join(d, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	r, err := Apply(d, false, false)
	if err != nil || r.AgentsMD != Added {
		t.Fatalf("%v %+v", err, r)
	}
	if got := read(t, d, "CLAUDE.md"); got != "# Claude\r\nrules\r\n\r\n"+AgentsLine+"\r\n" {
		t.Fatalf("the link's target must get the line, with its own line endings: %q", got)
	}
	if fi, _ := os.Lstat(filepath.Join(d, "AGENTS.md")); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link itself was replaced")
	}
	// A link that leads into .outfit/ is a profile's file, a dangling one and one outside lead nowhere.
	for name, target := range map[string]string{"profile": ".outfit/profiles/p/AGENTS.md", "dangling": "nope.md", "outside": "../elsewhere.md"} {
		d := t.TempDir()
		write(t, d, ".outfit/profiles/p/AGENTS.md", "mine\n")
		if err := os.Symlink(target, filepath.Join(d, "AGENTS.md")); err != nil {
			t.Fatal(err)
		}
		r, err := Apply(d, false, false)
		if err != nil || r.AgentsMD != Link || r.Doc != Created {
			t.Fatalf("%s: %v %+v", name, err, r)
		}
		if read(t, d, ".outfit/profiles/p/AGENTS.md") != "mine\n" {
			t.Fatalf("%s: a profile's file was edited", name)
		}
	}
}

func TestApplyChecksEverythingBeforeItWritesAnything(t *testing.T) {
	// docs/outfit.md is a directory: AGENTS.md must not have been changed when that is found.
	d := t.TempDir()
	write(t, d, "AGENTS.md", "x\n")
	if err := os.MkdirAll(filepath.Join(d, DocPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(d, false, false); err == nil {
		t.Fatal("expected an error")
	}
	if read(t, d, "AGENTS.md") != "x\n" {
		t.Fatal("AGENTS.md was changed although docs/outfit.md could not be written")
	}
	// A docs folder that is a link is refused by a dry run too (it agrees with the real run).
	d2, out := t.TempDir(), t.TempDir()
	write(t, d2, "AGENTS.md", "x\n")
	if err := os.Symlink(out, filepath.Join(d2, "docs")); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(d2, true, false); err == nil {
		t.Fatal("the dry run must refuse a linked docs folder like the real run")
	}
}

func TestHasLineRecognisesAnEditedVersion(t *testing.T) {
	for _, l := range []string{AgentsLine, "  " + AgentsLine + "  ", "- Optional: this repo supports outfit profiles; see docs/outfit.md."} {
		if !hasLine("a\n" + l + "\nb") {
			t.Errorf("%q not recognised", l)
		}
	}
	if hasLine("outfit is nice") {
		t.Error("unrelated text counted")
	}
}
