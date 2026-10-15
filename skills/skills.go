// Package skills carries outfit's agent-facing documentation inside the binary.
//
// Two things live here. The pointer skill (outfit/SKILL.md) is what `npx skills add`
// and `outfit skills install` put where an agent looks for skills: frontmatter, a
// paragraph and a pointer, a couple of kilobytes. The topics (topics/*.md) are the
// documentation itself, printed by `outfit skills <topic>`. The pointer never goes
// stale because it holds no documentation; the topics describe the binary that prints
// them, which is the only version whose behaviour matters to the reader.
package skills

import (
	"embed"
	"sort"
	"strings"
)

// Pointer is the SKILL.md that outfit installs for agents.
//
//go:embed outfit/SKILL.md
var Pointer string

//go:embed topics/*.md
var files embed.FS

// Marker is the frontmatter line that says outfit wrote a SKILL.md, so that `skills
// remove` and a second `skills install` touch only files of its own.
const Marker = "managed-by: outfit"

// Topic is one document: the name an agent asks for, the one-line summary the index
// shows (so the index costs no parsing) and the text.
type Topic struct {
	Name    string
	Summary string
	Body    string
}

// order is the reading order, not the alphabet: `basics` first because it is the one an
// agent reads before typing anything.
var order = []struct{ name, summary string }{
	{"basics", "Start here. What outfit is, the command surface, and the rules that stop an agent guessing wrong."},
	{"profiles", "Tiers, layout, how a swap works, and what `outfit sync` does with each swapped file."},
	{"toggles", "`get`/`set`, where a value comes from, locked keys, and using a toggle inside a team hook."},
	{"authoring", "See and edit inactive profiles (`outfit author on|off`), make, copy and promote profiles."},
	{"worktrees", "Linked worktrees: pinned profiles, worktree profiles, what `git worktree add` copies."},
	{"troubleshooting", "Stale base, quarantine, merge conflicts, refusals, out of sync: symptom, meaning, fix."},
	{"agents", "What an agent should and should not do in a repository that uses outfit."},
	{"updating", "Installing, updating and uninstalling outfit, and the signature checks."},
}

// Topics returns every topic in reading order. A topic whose file is missing is a build
// error caught by this package's test, never a runtime surprise.
func Topics() []Topic {
	out := make([]Topic, 0, len(order))
	for _, o := range order {
		b, err := files.ReadFile("topics/" + o.name + ".md")
		if err != nil {
			continue
		}
		out = append(out, Topic{Name: o.name, Summary: o.summary, Body: string(b)})
	}
	return out
}

// Find returns the topic called name.
func Find(name string) (Topic, bool) {
	for _, t := range Topics() {
		if t.Name == name {
			return t, true
		}
	}
	return Topic{}, false
}

// Names lists the topic names in reading order.
func Names() []string {
	var n []string
	for _, o := range order {
		n = append(n, o.name)
	}
	return n
}

// Files lists the embedded topic file names, sorted; the package test compares them with
// the declared order so a topic can be neither unlisted nor missing.
func Files() []string {
	ents, _ := files.ReadDir("topics")
	var n []string
	for _, e := range ents {
		n = append(n, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(n)
	return n
}
