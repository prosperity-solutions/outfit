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

// State is the content of state.json, kept per worktree.
type State struct {
	Schema int `json:"schema"`
	// GitVersion is `git --version`'s number at init, so hot paths need not ask.
	GitVersion string `json:"git_version"`
	// Swaps are the paths currently swapped by the active profile (filled by
	// sync). The pre-commit guard protects exactly these.
	Swaps []Swap `json:"swaps"`
	// Profile is the profile sync last made real in this worktree ("" = base).
	Profile string `json:"profile,omitempty"`
	// Author is author mode (`outfit author on`): every committed profile is on
	// disk for editing side by side, instead of only the active one.
	Author bool `json:"author,omitempty"`
	// CommittedProfiles remembers that the repository has committed profiles, so
	// the no-op sync path can keep inactive ones hidden without asking git.
	CommittedProfiles bool `json:"committed_profiles,omitempty"`
	// SparseSet and ExpectFilesSet record that OUTFIT turned sparse checkout and
	// sparse.expectFilesOutsideOfPatterns on in this worktree, so uninstall undoes
	// exactly that and nothing a person configured.
	SparseSet      bool `json:"sparse_set,omitempty"`
	ExpectFilesSet bool `json:"expect_files_set,omitempty"`
}

// Swap is one swapped path.
type Swap struct {
	Path string `json:"path"`
	// Target is the symlink text sync made, so a link someone else replaced is
	// told apart from ours.
	Target string `json:"target,omitempty"`
	// VariantHash is the SHA-256 of the variant file when sync last looked at it:
	// "unchanged since linking" is what lets an editor's atomic save be absorbed.
	VariantHash string `json:"variant_hash,omitempty"`
	// VariantBlob is git's blob id for the variant when the profile is committed. It
	// moves only when GIT changes the profile (a pull, a checkout, or a commit of an edit
	// made through the link); a pull that moves it together with the base is the profile
	// maintainer adapting the profile, so the base is re-baselined, not reported stale.
	VariantBlob string `json:"variant_blob,omitempty"`
	// BaseBlob is the blob of the committed (base) file at link time: when HEAD's
	// blob for the path differs, the base moved under the swap (a stale base). It is
	// recorded now and read by stale-base detection (a later change).
	BaseBlob string `json:"base_blob,omitempty"`
}

// Setting is one outfit.* git config value.
type Setting struct {
	// Where is "local" for the repository's own config, else the name of the
	// worktree git dir whose config.worktree holds it.
	Where string `json:"where"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Install is `<common-dir>/outfit/install.json`: facts about the whole
// repository (not one worktree) that uninstall needs, written BEFORE init changes
// anything so a half-finished init can still be undone exactly.
type Install struct {
	Schema  int    `json:"schema"`
	Binary  string `json:"binary"`
	Version string `json:"version"`
	// WorktreeConfigWasSet records whether extensions.worktreeConfig was already
	// true before the FIRST init, so uninstall only undoes what outfit did.
	WorktreeConfigWasSet bool `json:"worktree_config_was_set"`
	// Preexisting are the outfit.* settings that existed before the first init:
	// uninstall keeps them and removes the rest.
	Preexisting []Setting `json:"preexisting"`
}

// InstallPath returns the install record of a repository.
func InstallPath(commonDir string) string { return filepath.Join(commonDir, "outfit", "install.json") }

// ReadInstall loads the install record; ErrNone when init never ran.
func ReadInstall(commonDir string) (*Install, error) {
	b, err := os.ReadFile(InstallPath(commonDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNone
	}
	if err != nil {
		return nil, err
	}
	var in Install
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", InstallPath(commonDir), err)
	}
	if in.Schema > Schema {
		return nil, fmt.Errorf("%s was written by a newer outfit (schema %d): run `outfit update`", InstallPath(commonDir), in.Schema)
	}
	in.Schema = Schema
	if in.Preexisting == nil {
		in.Preexisting = []Setting{}
	}
	return &in, nil
}

// WriteInstall stores the install record atomically; it reports whether the file changed.
func WriteInstall(commonDir string, in *Install) (bool, error) {
	in.Schema = Schema
	if in.Preexisting == nil {
		in.Preexisting = []Setting{}
	}
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return false, err
	}
	b = append(b, '\n')
	path := InstallPath(commonDir)
	if old, err := os.ReadFile(path); err == nil && string(old) == string(b) {
		return false, nil
	}
	return true, WriteFileAtomic(path, b, 0o644)
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
