// Package state reads and writes `<per-worktree git dir>/outfit/state.json`:
// what outfit installed and, later, what it linked. It carries a schema version
// so a newer binary migrates an older file forward (HANDOVER.md §5.4), and hot
// paths trust the git version recorded here instead of calling `git --version`.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Schema is the current state.json version. Bump it with a migration in Read.
const Schema = 1

// SparseMarker is the first line of the sparse-checkout file outfit manages.
const SparseMarker = "# outfit-managed (do not edit; run `outfit sync`)"

// State is the content of state.json.
type State struct {
	Schema int `json:"schema"`
	// GitVersion is `git --version`'s number at init, so hot paths need not ask.
	GitVersion string `json:"git_version"`
	// Binary is the absolute path of the outfit that ran init.
	Binary string `json:"binary"`
	// Version is that outfit's version.
	Version string `json:"version"`
	// WorktreeConfigWasSet records whether extensions.worktreeConfig was already
	// true before the FIRST init, so uninstall only undoes what outfit did.
	WorktreeConfigWasSet bool `json:"worktree_config_was_set"`
	// Swaps are the paths currently swapped by the active profile (filled by
	// sync). The pre-commit guard protects exactly these.
	Swaps []Swap `json:"swaps"`
}

// Swap is one swapped path.
type Swap struct {
	Path string `json:"path"`
}

// Path returns the state file of a per-worktree git dir.
func Path(gitDir string) string { return filepath.Join(gitDir, "outfit", "state.json") }

// ErrNone is returned by Read when no state file exists.
var ErrNone = errors.New("no outfit state")

// Read loads and migrates the state file.
func Read(gitDir string) (*State, error) {
	b, err := os.ReadFile(Path(gitDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNone
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", Path(gitDir), err)
	}
	if s.Schema > Schema {
		return nil, fmt.Errorf("%s was written by a newer outfit (schema %d, this one understands %d): run `outfit update`", Path(gitDir), s.Schema, Schema)
	}
	// Migrations run here, oldest first. Schema 1 is the first version.
	s.Schema = Schema
	if s.Swaps == nil {
		s.Swaps = []Swap{}
	}
	return &s, nil
}

// Write stores s atomically (temp file in the same directory, then rename) and
// reports whether the file changed: an unchanged state touches nothing.
func Write(gitDir string, s *State) (changed bool, err error) {
	s.Schema = Schema
	if s.Swaps == nil {
		s.Swaps = []Swap{}
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return false, err
	}
	b = append(b, '\n')
	path := Path(gitDir)
	if old, err := os.ReadFile(path); err == nil && string(old) == string(b) {
		return false, nil
	}
	return true, WriteFileAtomic(path, b, 0o644)
}

// WriteFileAtomic writes data to path via a temp file in the same directory.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
