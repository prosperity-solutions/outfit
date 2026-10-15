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
	r, err := Apply(d, false)
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
	r, err = Apply(d, false)
	if err != nil || r.AgentsMD != Present || r.Doc != Exists || read(t, d, DocPath) != "ours\n" || read(t, d, "AGENTS.md") != got {
		t.Fatalf("second run: %v %+v", err, r)
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	d := t.TempDir()
	write(t, d, "AGENTS.md", "x\n")
	r, err := Apply(d, true)
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
	r, err := Apply(d, false)
	if err != nil || r.AgentsMD != Missing || r.Doc != Created {
		t.Fatalf("%v %+v", err, r)
	}
	d = t.TempDir()
	write(t, d, ".outfit/profiles/p/AGENTS.md", "mine\n")
	if err := os.Symlink(".outfit/profiles/p/AGENTS.md", filepath.Join(d, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	r, err = Apply(d, false)
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
	if _, err := Apply(d, false); err == nil {
		t.Fatal("expected an error")
	}
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Fatalf("wrote through the link: %v", ents)
	}
}
