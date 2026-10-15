package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func settings(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(SettingsFile)))
	if err != nil {
		return ""
	}
	return string(b)
}

func TestInstallNeedsAClaudeDirectory(t *testing.T) {
	dir := t.TempDir()
	if changed, err := Install(dir, "/bin/outfit"); err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if settings(t, dir) != "" {
		t.Fatal("a file was made where there is no .claude directory")
	}
}

func TestInstallIsIdempotentAndRemoveRestores(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if changed, err := Install(dir, "/opt/my bin/outfit"); err != nil || !changed {
		t.Fatalf("first install: changed=%v err=%v", changed, err)
	}
	first := settings(t, dir)
	if !strings.Contains(first, "SessionStart") || !strings.Contains(first, Marker) || !strings.Contains(first, `/opt/my bin/outfit`) {
		t.Fatalf("entry missing:\n%s", first)
	}
	if changed, err := Install(dir, "/opt/my bin/outfit"); err != nil || changed {
		t.Fatalf("second install: changed=%v err=%v", changed, err)
	}
	if settings(t, dir) != first {
		t.Fatal("a second install rewrote the file")
	}
	if !Installed(dir) {
		t.Fatal("Installed is false")
	}
	// A moved binary updates the one entry, it does not add a second.
	if changed, err := Install(dir, "/usr/local/bin/outfit"); err != nil || !changed {
		t.Fatalf("update: changed=%v err=%v", changed, err)
	}
	if n := strings.Count(settings(t, dir), Marker); n != 1 {
		t.Fatalf("%d entries after an update", n)
	}
	if changed, err := Remove(dir); err != nil || !changed {
		t.Fatalf("remove: changed=%v err=%v", changed, err)
	}
	if settings(t, dir) != "" {
		t.Fatalf("outfit made the file, so removing its entry removes the file:\n%s", settings(t, dir))
	}
	if changed, err := Remove(dir); err != nil || changed {
		t.Fatalf("second remove: changed=%v err=%v", changed, err)
	}
}

func TestOtherSettingsAndHooksSurvive(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := `{"permissions":{"allow":["Bash(go test:*)"]},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo mine"}]}],"Stop":[{"hooks":[{"type":"command","command":"echo bye"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(SettingsFile)), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, ""); err != nil {
		t.Fatal(err)
	}
	got := settings(t, dir)
	for _, want := range []string{`Bash(go test:*)`, `echo mine`, `echo bye`, Marker} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q lost:\n%s", want, got)
		}
	}
	if _, err := Remove(dir); err != nil {
		t.Fatal(err)
	}
	got = settings(t, dir)
	if strings.Contains(got, Marker) || !strings.Contains(got, `echo mine`) || !strings.Contains(got, `echo bye`) || !strings.Contains(got, `permissions`) {
		t.Fatalf("remove touched what is not outfit's:\n%s", got)
	}
}

func TestAFileOutfitCannotReadIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filepath.FromSlash(SettingsFile))
	const bad = "{ // a comment\n}\n"
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, ""); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := Remove(dir); err == nil {
		t.Fatal("expected an error")
	}
	if settings(t, dir) != bad {
		t.Fatal("the file was changed")
	}
	if Installed(dir) {
		t.Fatal("Installed on an unreadable file")
	}
}

func TestCommandIsSilentAndSelfIdentifying(t *testing.T) {
	c := Command("/x/outfit")
	if !strings.HasSuffix(c, Marker) || !strings.Contains(c, ">/dev/null 2>&1") || !strings.Contains(c, "|| true") {
		t.Fatal(c)
	}
	if !strings.Contains(Command("/it's/outfit"), `'/it'\''s/outfit'`) {
		t.Fatal("exe path is not shell-quoted")
	}
}
