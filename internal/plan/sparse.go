package plan

import (
	"fmt"
	"path"
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
	if p == ".outfit" || strings.HasPrefix(p, ".outfit/") {
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
	// HideProfiles hides the committed profiles that are not Active. False in
	// author mode, and when the repository has none.
	HideProfiles bool
	// Active is the active COMMITTED profile whose directory stays on disk ("" for
	// base, local and worktree profiles).
	Active string
}

// SparseText renders the managed sparse-checkout file (HANDOVER.md §3.4.3). The
// patterns are non-cone: "/*" includes everything, then each "!/path" hides one
// swapped file and "!/.outfit/profiles/*" hides every committed profile, then the
// active one is included back. Paths are written in sorted order so the text is a
// pure function of its inputs (an unchanged file is never rewritten).
func SparseText(o SparseOptions) string {
	var b strings.Builder
	b.WriteString(Marker + "\n/*\n")
	hidden := append([]string(nil), o.Hidden...)
	sortStrings(hidden)
	for _, p := range hidden {
		b.WriteString("!/" + escapePattern(p) + "\n")
	}
	if o.HideProfiles {
		b.WriteString("!/" + CommittedRoot + "/*\n")
		if o.Active != "" {
			b.WriteString("/" + CommittedRoot + "/" + escapePattern(o.Active) + "/\n")
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
			o.Active = unescapePattern(strings.TrimSuffix(strings.TrimPrefix(l, "/"+CommittedRoot+"/"), "/"))
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

func sortStrings(s []string) {
	// insertion sort: the lists are tiny and this keeps the package import-light
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
