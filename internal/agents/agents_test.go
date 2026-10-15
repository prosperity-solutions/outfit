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
	// Uninstall must not fail over a file outfit never wrote.
	if changed, err := Remove(dir); err != nil || changed {
		t.Fatalf("Remove on a file without outfit's marker: changed=%v err=%v", changed, err)
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

func TestSymlinkedSettingsOrClaudeDirAreNeverFollowed(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "settings.json")
	const content = `{"env":{"TOKEN":"sk-secret"},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"x ` + Marker + `"}]}]}}`
	if err := os.WriteFile(secret, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// settings.local.json is a link to somebody else's settings.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, filepath.FromSlash(SettingsFile))
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, ""); err == nil {
		t.Fatal("Install followed a symlinked settings file")
	}
	if changed, err := Remove(dir); err != nil || changed {
		t.Fatalf("Remove on a symlinked settings file: changed=%v err=%v (nothing of outfit's to remove, and no failure)", changed, err)
	}
	if Installed(dir) {
		t.Fatal("Installed followed a symlinked settings file")
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}
	if b, _ := os.ReadFile(secret); string(b) != content {
		t.Fatal("the target was written")
	}

	// .claude itself is a link to another project's directory.
	dir2 := t.TempDir()
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "settings.local.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(dir2, ".claude")); err != nil {
		t.Fatal(err)
	}
	if changed, err := Install(dir2, ""); err != nil || changed {
		t.Fatalf("Install through a linked .claude: changed=%v err=%v", changed, err)
	}
	if changed, err := Remove(dir2); err != nil || changed {
		t.Fatalf("Remove through a linked .claude: changed=%v err=%v", changed, err)
	}
	if b, err := os.ReadFile(filepath.Join(other, "settings.local.json")); err != nil || string(b) != content {
		t.Fatal("the other project's settings were touched")
	}
}

func TestAHookOfThePersonNextToOutfitsIsNeverLost(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	mixed := `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"echo mine"},{"type":"command","command":"old ` + Marker + `"}]}]}}`
	path := filepath.Join(dir, filepath.FromSlash(SettingsFile))
	if err := os.WriteFile(path, []byte(mixed), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := Install(dir, "/new/outfit"); err != nil || !changed {
		t.Fatalf("install: changed=%v err=%v", changed, err)
	}
	got := settings(t, dir)
	if !strings.Contains(got, "echo mine") || !strings.Contains(got, "/new/outfit") || strings.Contains(got, "old "+Marker) || !strings.Contains(got, `"matcher": "startup"`) {
		t.Fatalf("update lost something or kept the old entry:\n%s", got)
	}
	if n := strings.Count(got, Marker); n != 1 {
		t.Fatalf("%d entries", n)
	}
	if changed, err := Remove(dir); err != nil || !changed {
		t.Fatalf("remove: changed=%v err=%v", changed, err)
	}
	got = settings(t, dir)
	if got == "" || !strings.Contains(got, "echo mine") || strings.Contains(got, Marker) || !strings.Contains(got, `"matcher": "startup"`) {
		t.Fatalf("remove took the person's hook with it:\n%s", got)
	}
}
