package agents

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/prosperity-solutions/outfit/skills"
)

func TestInstallSkillCreatesUpdatesAndIsIdempotent(t *testing.T) {
	base := t.TempDir()
	res, err := InstallSkill(base, false)
	if err != nil || len(res) != 2 || res[0].Action != SkillCreated {
		t.Fatalf("first install: %v %+v", err, res)
	}
	b, _ := os.ReadFile(filepath.Join(base, ".claude/skills/outfit/SKILL.md"))
	if string(b) != skills.Pointer {
		t.Fatal("the installed file is not the embedded pointer")
	}
	if res, _ = InstallSkill(base, false); res[0].Action != SkillUnchanged || res[1].Action != SkillUnchanged {
		t.Fatalf("second install: %+v", res)
	}
	// One of ours, older: updated, the other left alone.
	old := "---\nname: outfit\nmetadata:\n  managed-by: outfit\n---\nold\n"
	if err := os.WriteFile(filepath.Join(base, ".agents/skills/outfit/SKILL.md"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, _ = InstallSkill(base, false); res[1].Action != SkillUpdated {
		t.Fatalf("an older pointer of ours must be updated: %+v", res)
	}
}

func TestInstallSkillNeverTouchesAForeignFileAndWritesNothingFirst(t *testing.T) {
	base := t.TempDir()
	foreign := filepath.Join(base, ".agents/skills/outfit/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("---\nname: outfit\n---\nmine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSkill(base, false); !errors.Is(err, ErrSkillNotOurs) {
		t.Fatalf("want ErrSkillNotOurs, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, ".claude")); !os.IsNotExist(err) {
		t.Fatal("the first target was written before the second was checked")
	}
	if b, _ := os.ReadFile(foreign); string(b) != "---\nname: outfit\n---\nmine\n" {
		t.Fatal("a foreign SKILL.md was changed")
	}
	if _, err := RemoveSkill(base, false); !errors.Is(err, ErrSkillNotOurs) {
		t.Fatalf("remove must refuse a foreign file too, got %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("remove deleted a foreign SKILL.md")
	}
}

func TestInstallSkillDoesNotFollowALinkOutOfTheBase(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, ".claude")); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSkill(base, false); err == nil {
		t.Fatal("expected an error for a .claude that links out of the base")
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatalf("something was written through the link: %v", ents)
	}
}

func TestInstallSkillDryRunWritesNothing(t *testing.T) {
	base := t.TempDir()
	res, err := InstallSkill(base, true)
	if err != nil || res[0].Action != SkillCreated {
		t.Fatalf("%v %+v", err, res)
	}
	if ents, _ := os.ReadDir(base); len(ents) != 0 {
		t.Fatalf("dry run wrote %v", ents)
	}
}
