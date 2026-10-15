package agents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/prosperity-solutions/outfit/skills"
)

// SkillDirs are the directories, relative to a home directory or a worktree, where
// coding agents read skills: Claude Code's, and the shared `.agents` one the other
// agents (Codex, Cursor, …) follow. The pointer skill goes into both.
var SkillDirs = []string{".claude/skills/outfit", ".agents/skills/outfit"}

// SkillFile is the file name every agent expects.
const SkillFile = "SKILL.md"

// Skill actions, as reported per file.
const (
	SkillCreated   = "created"
	SkillUpdated   = "updated"
	SkillUnchanged = "unchanged"
	SkillRemoved   = "removed"
	SkillAbsent    = "absent"
)

// SkillResult is what happened to one SKILL.md.
type SkillResult struct {
	Path   string `json:"path"` // relative to the base
	Action string `json:"action"`
}

// ErrSkillNotOurs is returned for a SKILL.md that outfit did not write: it is left alone.
var ErrSkillNotOurs = errors.New("not written by outfit")

// ours reports whether text is a SKILL.md outfit wrote: its frontmatter carries the marker.
func ours(text string) bool {
	if !strings.HasPrefix(text, "---\n") {
		return false
	}
	front, _, ok := strings.Cut(text[4:], "\n---")
	if !ok {
		return false
	}
	for _, l := range strings.Split(front, "\n") {
		if strings.TrimSpace(l) == skills.Marker {
			return true
		}
	}
	return false
}

// InstallSkill writes the pointer skill under base (a home directory or a worktree) into
// every SkillDirs entry. It never follows a link out of base (os.Root), never replaces a
// SKILL.md it did not write (ErrSkillNotOurs, naming the file, and nothing is written
// before every target has been checked) and is idempotent. dry only reports.
func InstallSkill(base string, dry bool) ([]SkillResult, error) {
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	want := skills.Pointer
	var out []SkillResult
	var todo []func() error
	for _, d := range SkillDirs {
		p := path.Join(d, SkillFile)
		fi, lerr := root.Lstat(p)
		switch {
		case errors.Is(lerr, fs.ErrNotExist):
			out = append(out, SkillResult{p, SkillCreated})
		case lerr != nil:
			return nil, fmt.Errorf("%s: %w", p, lerr)
		case !fi.Mode().IsRegular():
			return nil, fmt.Errorf("%s: %w (it is not a regular file)", p, ErrSkillNotOurs)
		default:
			b, rerr := root.ReadFile(p)
			if rerr != nil {
				return nil, fmt.Errorf("%s: %w", p, rerr)
			}
			if !ours(string(b)) {
				return nil, fmt.Errorf("%s: %w", p, ErrSkillNotOurs)
			}
			if string(b) == want {
				out = append(out, SkillResult{p, SkillUnchanged})
				continue
			}
			out = append(out, SkillResult{p, SkillUpdated})
		}
		d, p := d, p
		todo = append(todo, func() error {
			if err := root.MkdirAll(d, 0o755); err != nil {
				return err
			}
			// Beside the target and renamed into it, so a reader never sees half a file.
			tmp := p + ".outfit-tmp"
			if err := root.WriteFile(tmp, []byte(want), 0o644); err != nil {
				return err
			}
			if err := root.Rename(tmp, p); err != nil {
				_ = root.Remove(tmp)
				return err
			}
			return nil
		})
	}
	if dry {
		return out, nil
	}
	for _, f := range todo {
		if err := f(); err != nil {
			return out, err
		}
	}
	return out, nil
}

// RemoveSkill takes away the SKILL.md files outfit wrote under base, and the
// directories it made when they are empty. A SKILL.md outfit did not write stays
// (reported as ErrSkillNotOurs after the rest is done).
func RemoveSkill(base string, dry bool) ([]SkillResult, error) {
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	var out []SkillResult
	var firstErr error
	for _, d := range SkillDirs {
		p := path.Join(d, SkillFile)
		fi, lerr := root.Lstat(p)
		if errors.Is(lerr, fs.ErrNotExist) {
			out = append(out, SkillResult{p, SkillAbsent})
			continue
		}
		if lerr != nil {
			return out, fmt.Errorf("%s: %w", p, lerr)
		}
		b, rerr := root.ReadFile(p)
		if !fi.Mode().IsRegular() || rerr != nil || !ours(string(b)) {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", p, ErrSkillNotOurs)
			}
			continue
		}
		out = append(out, SkillResult{p, SkillRemoved})
		if dry {
			continue
		}
		if err := root.Remove(p); err != nil {
			return out, err
		}
		// The directories this created (outfit, then skills; never .claude or .agents
		// themselves), innermost first, only while empty (Remove refuses a directory
		// with anything in it).
		for dir := d; strings.Contains(dir, "/"); dir = path.Dir(dir) {
			if root.Remove(dir) != nil {
				break
			}
		}
	}
	return out, firstErr
}
