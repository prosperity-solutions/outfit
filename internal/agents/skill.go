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

// ErrSkillLink is returned when a link (or something that is not a directory) stands on the
// way to a SKILL.md and outfit will not write through it.
var ErrSkillLink = errors.New("outfit does not write through links there")

// ErrSkillNotOurs is returned for a SKILL.md that outfit did not write: it is left alone.
var ErrSkillNotOurs = errors.New("not written by outfit")

// ours reports whether text is a SKILL.md outfit wrote: its frontmatter carries the marker
// as `managed-by: outfit` under `metadata:`, where outfit's own pointer has it.
func ours(text string) bool {
	if !strings.HasPrefix(text, "---\n") {
		return false
	}
	front, _, ok := strings.Cut(text[4:], "\n---")
	if !ok {
		return false
	}
	inMeta := false
	for _, l := range strings.Split(front, "\n") {
		switch {
		case strings.TrimSpace(l) == "":
		case !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t"):
			inMeta = strings.TrimSpace(l) == "metadata:"
		case inMeta && strings.TrimSpace(l) == skills.Marker:
			return true
		}
	}
	return false
}

// realDirs checks that every directory on the way to dir, where it exists, is a real
// directory and not a link. A repository can commit `.claude` as a link, and git does not
// look through links, so "git does not track it" proves nothing about where a write goes.
func realDirs(root *os.Root, dir string) error {
	parts := strings.Split(dir, "/")
	for i := range parts {
		p := path.Join(parts[:i+1]...)
		fi, err := root.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return linkErr(p, err)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s is a link or not a directory: %w", p, ErrSkillLink)
		}
	}
	return nil
}

// InstallSkill writes the pointer skill under base (a home directory or a worktree) into
// every SkillDirs entry. It never follows a link out of base (os.Root), never replaces a
// SKILL.md it did not write (ErrSkillNotOurs, naming the file, and nothing is written
// before every target has been checked) and is idempotent. dry only reports. strict also
// refuses a link anywhere on the way (use it inside a repository, whose links are not the
// user's own).
func InstallSkill(base string, dry, strict bool) ([]SkillResult, error) {
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
		if strict {
			if err := realDirs(root, d); err != nil {
				return nil, err
			}
		}
		fi, lerr := root.Lstat(p)
		switch {
		case errors.Is(lerr, fs.ErrNotExist):
			out = append(out, SkillResult{p, SkillCreated})
		case lerr != nil:
			return nil, linkErr(p, lerr)
		case !fi.Mode().IsRegular():
			return nil, fmt.Errorf("%s: %w (it is not a regular file)", p, ErrSkillNotOurs)
		default:
			b, rerr := root.ReadFile(p)
			if rerr != nil {
				return nil, linkErr(p, rerr)
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
			// Beside the target and renamed into it, so a reader never sees half a file. The
			// temp name is fixed, so whatever is there (a leftover, a planted link or FIFO) is
			// removed as an entry, never followed, and the file is created exclusively.
			tmp := p + ".outfit-tmp"
			if err := root.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return err
			}
			_, werr := f.WriteString(want)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr == nil {
				werr = root.Rename(tmp, p)
			}
			if werr != nil {
				_ = root.Remove(tmp)
			}
			return werr
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

// linkErr turns os.Root's "path escapes from parent" (a link, absolute or leading out of
// the base) into the refusal it is, naming the path.
func linkErr(p string, err error) error {
	if strings.Contains(err.Error(), "escapes from parent") {
		return fmt.Errorf("%s: a link on the way is absolute or leads outside the folder: %w", p, ErrSkillLink)
	}
	return fmt.Errorf("%s: %w", p, err)
}

// RemoveSkill takes away the SKILL.md files outfit wrote under base, and the directories it
// made when they are empty real directories. Every target is checked before anything is
// removed: a SKILL.md outfit did not write stays, and then nothing is removed
// (ErrSkillNotOurs).
func RemoveSkill(base string, dry, strict bool) ([]SkillResult, error) {
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	var out []SkillResult
	var todo []string
	for _, d := range SkillDirs {
		p := path.Join(d, SkillFile)
		if strict {
			if err := realDirs(root, d); err != nil {
				return nil, err
			}
		}
		fi, lerr := root.Lstat(p)
		if errors.Is(lerr, fs.ErrNotExist) {
			out = append(out, SkillResult{p, SkillAbsent})
			continue
		}
		if lerr != nil {
			return nil, linkErr(p, lerr)
		}
		// Mode first: reading a FIFO would wait for a writer that never comes.
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: %w", p, ErrSkillNotOurs)
		}
		b, rerr := root.ReadFile(p)
		if rerr != nil {
			return nil, linkErr(p, rerr)
		}
		if !ours(string(b)) {
			return nil, fmt.Errorf("%s: %w", p, ErrSkillNotOurs)
		}
		out = append(out, SkillResult{p, SkillRemoved})
		todo = append(todo, d)
	}
	if dry {
		return out, nil
	}
	for _, d := range todo {
		if err := root.Remove(path.Join(d, SkillFile)); err != nil {
			return out, err
		}
		// The directories this created (outfit, then skills; never .claude or .agents
		// themselves), innermost first, only while empty real directories: Remove would
		// unlink a link whether or not its target is empty (`~/.claude/skills` is often a
		// link into a dotfiles repository).
		for dir := d; strings.Contains(dir, "/"); dir = path.Dir(dir) {
			if di, derr := root.Lstat(dir); derr != nil || !di.IsDir() || root.Remove(dir) != nil {
				break
			}
		}
	}
	return out, nil
}
