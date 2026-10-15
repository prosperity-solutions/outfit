package plan

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Marker is the first line of the sparse-checkout file outfit manages; a sparse
// file without it is somebody else's and is never touched (HANDOVER.md §3.4.6).
const Marker = "# outfit-managed (do not edit; run `outfit sync`)"

// CommittedRoot is where committed profiles live, relative to the worktree.
const CommittedRoot = ".outfit/profiles"

// ValidSwapPath reports whether p may be swapped: a clean, relative, slash
// separated path inside the repository that is not git's or outfit's own, with no
// character that cannot be written into a sparse pattern file (newlines) or that
// a shell, a terminal or a filesystem treats as control.
func ValidSwapPath(p string) error {
	switch {
	case p == "" || p == ".":
		return fmt.Errorf("empty path")
	case strings.HasPrefix(p, "/") || strings.HasPrefix(p, "./") || strings.Contains(p, "//") || strings.HasSuffix(p, "/"):
		return fmt.Errorf("%q is not a clean relative path", p)
	case path.Clean(p) != p:
		return fmt.Errorf("%q is not a clean relative path", p)
	}
	for _, part := range strings.Split(p, "/") {
		switch {
		case part == ".." || part == ".":
			return fmt.Errorf("%q has a %q component", p, part)
		case strings.EqualFold(part, ".git"):
			return fmt.Errorf("%q is inside .git", p)
		}
	}
	// (Any case: git matches sparse patterns case-insensitively where core.ignorecase is on, so
	// `.OUTFIT` would hide the team's .outfit/config as well.)
	if first, _, _ := strings.Cut(p, "/"); strings.EqualFold(first, ".outfit") {
		return fmt.Errorf("%q is inside .outfit, which holds the profiles themselves", p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%q contains a control character", p)
		}
	}
	return nil
}

// escapePattern makes p a literal gitignore-style pattern: wildcard characters,
// a leading "!" or "#", and trailing spaces would otherwise change its meaning.
func escapePattern(p string) string {
	var b strings.Builder
	for i, r := range p {
		switch r {
		case '\\', '*', '?', '[', ']':
			b.WriteByte('\\')
		case '!', '#':
			if i == 0 {
				b.WriteByte('\\')
			}
		}
		b.WriteRune(r)
	}
	s := b.String()
	if strings.HasSuffix(s, " ") {
		// Only unescaped trailing spaces are stripped: escape the last one.
		s = s[:len(s)-1] + `\ `
	}
	return s
}

// SparseOptions says what the managed sparse-checkout file must hide.
type SparseOptions struct {
	// Hidden are the swapped paths (the base files are hidden; links stand in).
	Hidden []string
	// HideDirs are directories the active profile replaces as a whole (`!/dir/`): every tracked
	// file under them is hidden, the team's new ones included, with no sync needed. A swapped
	// path under one needs no line of its own.
	HideDirs []string
	// HideProfiles hides the committed profiles that are not Active. False in
	// author mode, and when the repository has none.
	HideProfiles bool
	// Show are the committed profiles whose directories stay on disk although the others
	// are hidden: the active one (none for base, local and worktree profiles) and any the
	// person asked for (`outfit author on <name>`). Sorted, no duplicates, no empty names.
	Show []string
}

// SparseText renders the managed sparse-checkout file (HANDOVER.md §3.4.3). The
// patterns are non-cone: "/*" includes everything, then each "!/path" hides one
// swapped file and "!/.outfit/profiles/*" hides every committed profile, then the
// active one is included back. Paths are written in sorted order so the text is a
// pure function of its inputs (an unchanged file is never rewritten). Every shown profile
// is included back after the "hide all" line.
func SparseText(o SparseOptions) string {
	var b strings.Builder
	b.WriteString(Marker + "\n/*\n")
	dirs := append([]string(nil), o.HideDirs...)
	sort.Strings(dirs)
	for i, d := range dirs {
		if d != "" && (i == 0 || d != dirs[i-1]) {
			b.WriteString("!/" + escapePattern(d) + "/\n")
		}
	}
	hidden := append([]string(nil), o.Hidden...)
	sort.Strings(hidden)
	for _, p := range hidden {
		if UnderAny(p, dirs) {
			continue
		}
		b.WriteString("!/" + escapePattern(p) + "\n")
	}
	if o.HideProfiles {
		b.WriteString("!/" + CommittedRoot + "/*\n")
		show := append([]string(nil), o.Show...)
		sort.Strings(show)
		for i, name := range show {
			if name != "" && (i == 0 || name != show[i-1]) {
				b.WriteString("/" + CommittedRoot + "/" + escapePattern(name) + "/\n")
			}
		}
	}
	return b.String()
}

// ParseSparse reads a managed sparse-checkout file back: the hidden paths, whether
// profiles are hidden, and the active committed profile. ok is false when the text
// is not an outfit-managed file. It is SparseText's inverse (a fuzz target).
func ParseSparse(text string) (o SparseOptions, ok bool) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) < 2 || lines[0] != Marker || lines[1] != "/*" {
		return o, false
	}
	for _, l := range lines[2:] {
		switch {
		case l == "!/"+CommittedRoot+"/*":
			o.HideProfiles = true
		case strings.HasPrefix(l, "/"+CommittedRoot+"/") && strings.HasSuffix(l, "/"):
			o.Show = append(o.Show, unescapePattern(strings.TrimSuffix(strings.TrimPrefix(l, "/"+CommittedRoot+"/"), "/")))
		case strings.HasPrefix(l, "!/") && strings.HasSuffix(l, "/") && !strings.HasSuffix(l, `\/`):
			o.HideDirs = append(o.HideDirs, unescapePattern(strings.TrimSuffix(strings.TrimPrefix(l, "!/"), "/")))
		case strings.HasPrefix(l, "!/"):
			o.Hidden = append(o.Hidden, unescapePattern(strings.TrimPrefix(l, "!/")))
		default:
			return SparseOptions{}, false
		}
	}
	return o, true
}

func unescapePattern(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// UnderAny reports whether path is inside one of the directories (a directory is not under itself).
func UnderAny(path string, dirs []string) bool {
	for _, d := range dirs {
		if d != "" && strings.HasPrefix(path, d+"/") {
			return true
		}
	}
	return false
}

// IgnoreMarker is the first line of the `.gitignore` outfit keeps in a replaced directory: the
// profile's own files there (paths the repository does not track) are links, and git must neither
// list nor stage them. The file is untracked and ignores itself; only a file that starts with this
// line is ever rewritten or removed.
const IgnoreMarker = "# outfit-managed: the profile's own files in this directory (do not edit; run `outfit sync`)"

// IgnoreText renders that file for dir: one anchored line per file, relative to dir, in sorted
// order, so the text is a pure function of its inputs. No files gives "": there is no file.
func IgnoreText(dir string, files []string) string {
	if len(files) == 0 {
		return ""
	}
	rel := make([]string, 0, len(files))
	for _, f := range files {
		if r, ok := strings.CutPrefix(f, dir+"/"); ok && r != "" {
			rel = append(rel, r)
		}
	}
	sort.Strings(rel)
	var b strings.Builder
	b.WriteString(IgnoreMarker + "\n/.gitignore\n")
	for i, r := range rel {
		if i == 0 || r != rel[i-1] {
			b.WriteString("/" + escapePattern(r) + "\n")
		}
	}
	return b.String()
}

// OurIgnore reports that text is a file IgnoreText wrote.
func OurIgnore(text string) bool { return strings.HasPrefix(text, IgnoreMarker+"\n") }
