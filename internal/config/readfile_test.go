package config

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	if f, err := ReadFile(path, KindTeam); f != nil || err != nil {
		t.Fatalf("missing file: got %v, %v", f, err)
	}
	if err := os.WriteFile(path, []byte("[outfit]\n biome = off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ReadFile(path, KindTeam)
	if err != nil || f.Keys["biome"] != "off" {
		t.Fatalf("regular file: %+v, %v", f, err)
	}
}

// A committed symlink must never be followed: it could point at /dev/zero, a
// secret, or a FIFO that hangs every hook.
func TestReadFileRefusesSymlinksAndFIFOs(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("[outfit]\n biome = off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	f, err := ReadFile(link, KindTeam)
	if err != nil || len(f.Keys) != 0 || len(f.Warnings) != 1 {
		t.Fatalf("symlink must not be read: %+v, %v", f, err)
	}

	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("no fifo: %v", err)
	}
	done := make(chan *File, 1)
	go func() { f, _ := ReadFile(fifo, KindTeam); done <- f }()
	select {
	case f := <-done:
		if len(f.Keys) != 0 || len(f.Warnings) != 1 {
			t.Fatalf("fifo must not be read: %+v", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO blocked")
	}
}
