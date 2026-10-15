package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/prosperity-solutions/outfit/internal/agents"
	"github.com/prosperity-solutions/outfit/internal/apply"
	"github.com/prosperity-solutions/outfit/skills"
)

// cmdSkills is `outfit skills`: the agent-facing documentation the binary carries, and
// the pointer skill that sends agents to it.
//
//	outfit skills [--json]            the index: topic, size, one line
//	outfit skills <topic> [--json]    one document
//	outfit skills install [--project] put the pointer skill where agents read skills
//	outfit skills remove  [--project] take it away again
//
// The first two need no repository, no config and no git: an agent asking how outfit
// works may be standing anywhere. Both write only to stdout (the caller pipes it into
// its context), and neither is on the update banner's list.
func cmdSkills(ctx context.Context, e Env, args []string) int {
	fs := newFlags("skills")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	project := fs.Bool("project", false, "install/remove in this worktree (.claude/skills, .agents/skills) instead of your home directory")
	dry := fs.Bool("dry-run", false, "install/remove: only say what would change")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }
	if len(pos) > 1 {
		return fail(Usagef("usage: outfit skills [topic | install | remove] [--project] [--json]").WithFix("outfit skills -h"))
	}
	if len(pos) == 1 && (pos[0] == "install" || pos[0] == "remove") {
		return skillsInstall(ctx, e, pos[0] == "remove", *project, *dry, *asJSON)
	}
	if *project || *dry {
		return fail(Usagef("--project and --dry-run belong to `outfit skills install` and `outfit skills remove`").WithFix("outfit skills install --project"))
	}
	if len(pos) == 0 {
		if *asJSON {
			type row struct {
				Name    string `json:"name"`
				Summary string `json:"summary"`
				Bytes   int    `json:"bytes"`
			}
			var rows []row
			for _, t := range skills.Topics() {
				rows = append(rows, row{t.Name, t.Summary, len(t.Body)})
			}
			return writeJSON(e, rows, true)
		}
		return writeOut(e, skillsIndex())
	}
	t, ok := skills.Find(pos[0])
	if !ok {
		msg := fmt.Sprintf("unknown skill topic %q (available: %s)", pos[0], strings.Join(skills.Names(), ", "))
		if near := closestTopic(pos[0]); near != "" {
			msg += "; did you mean " + near + "?"
		}
		return fail(Usagef("%s", msg).WithFix("outfit skills"))
	}
	if *asJSON {
		return writeJSON(e, map[string]any{"name": t.Name, "summary": t.Summary, "body": t.Body}, true)
	}
	return writeOut(e, withNewline(t.Body))
}

// writeOut writes one finished document to stdout. A reader that has already left (`| head`)
// got what it asked for: that is not a failure.
func writeOut(e Env, text string) int {
	if _, err := io.WriteString(e.Stdout, text); err != nil && !isBrokenPipe(err) {
		return report(e.Stdout, e.Stderr, false, Errorf("could not write to stdout: %v", err))
	}
	return ExitOK
}

// writeJSON prints v as one JSON document, and says so when it cannot be encoded.
func writeJSON(e Env, v any, indent bool) int {
	var b []byte
	var err error
	if indent {
		b, err = json.MarshalIndent(v, "", "  ")
	} else {
		b, err = json.Marshal(v)
	}
	if err != nil {
		return report(e.Stdout, e.Stderr, true, Errorf("cannot encode the output as JSON: %v", err))
	}
	return writeOut(e, string(b)+"\n")
}

func isBrokenPipe(err error) bool { return errors.Is(err, syscall.EPIPE) }

func withNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

func skillsIndex() string {
	var b strings.Builder
	b.WriteString("outfit agent documentation\n\n")
	ts := skills.Topics()
	w := 0
	for _, t := range ts {
		if len(t.Name) > w {
			w = len(t.Name)
		}
	}
	for _, t := range ts {
		fmt.Fprintf(&b, "  %-*s  %3dK  %s\n", w, t.Name, (len(t.Body)+1023)/1024, t.Summary)
	}
	b.WriteString("\nRead one with `outfit skills <topic>`. Every document describes this binary, not whatever outfit the docs were written against.\n")
	b.WriteString("Live state is not in here on purpose: `outfit status`, `outfit ls`, `outfit show <profile>`.\n")
	b.WriteString("Put a pointer skill where your agent reads skills: `outfit skills install` (or `--project` for this worktree).\n")
	return b.String()
}

// closestTopic suggests a topic for a near miss: a prefix or a substring, nothing cleverer.
func closestTopic(name string) string {
	n := strings.ToLower(name)
	if n == "" {
		return ""
	}
	for _, t := range skills.Names() {
		if strings.HasPrefix(t, n) || strings.HasPrefix(n, t) || strings.Contains(t, n) {
			return t
		}
	}
	return ""
}

type skillsOut struct {
	Scope  string               `json:"scope"`
	Base   string               `json:"base"`
	DryRun bool                 `json:"dry_run,omitempty"`
	Files  []agents.SkillResult `json:"files"`
}

func skillsInstall(ctx context.Context, e Env, remove, project, dry, asJSON bool) int {
	fail := func(err error) int { return report(e.Stdout, e.Stderr, asJSON, err) }
	out := skillsOut{Scope: "global", DryRun: dry}
	var l *loaded
	if project {
		out.Scope = "project"
		var err error
		if l, err = load(ctx, e); err != nil {
			return fail(err)
		}
		out.Base = l.Repo.WorkTree
		// A skill directory git tracks is the team's own: not ours to write into. Asked
		// without literal pathspecs and case-insensitively (a Mac tracks `.Claude/...` as
		// the same folder), and a submodule (mode 160000) at `.claude` or `.agents` counts:
		// the parent's index lists nothing inside it.
		res, err := l.Git.Read(ctx, "--icase-pathspecs", "ls-files", "-z", "-s", "--", ".claude", ".agents")
		if err != nil {
			return fail(Errorf("cannot ask git what is tracked: %v", err))
		}
		for _, rec := range strings.Split(string(res.Stdout), "\x00") {
			meta, p, ok := strings.Cut(rec, "\t")
			if !ok {
				continue
			}
			low := strings.ToLower(p)
			if strings.HasPrefix(meta, "160000") || strings.HasPrefix(low, ".claude/skills/outfit/") || strings.HasPrefix(low, ".agents/skills/outfit/") {
				return fail((&Error{Code: ExitNeedsHuman, Msg: p + " is tracked by git (the team's own skill or a submodule); outfit does not write into it"}).
					WithFix("outfit skills install   (for your home directory instead)"))
			}
		}
	} else {
		home := e.Getenv("HOME")
		if home == "" || !filepath.IsAbs(home) {
			return fail(Errorf("HOME is not set to an absolute path, so there is no home directory to put the skill in").WithFix("outfit skills install --project"))
		}
		out.Base = home
	}
	var err error
	if remove {
		out.Files, err = agents.RemoveSkill(out.Base, dry, project)
	} else {
		out.Files, err = agents.InstallSkill(out.Base, dry, project)
	}
	if err != nil {
		code := ExitError
		msg := err.Error()
		fix := ""
		if errors.Is(err, agents.ErrSkillLink) {
			code = ExitNeedsHuman
			fix = "make the link relative and inside the folder, or put the file there by hand (outfit skills " + map[bool]string{true: "remove", false: "install"}[remove] + " -h)"
			if project {
				fix = "replace the link with a real directory, or install into your home directory: outfit skills install"
			}
		} else if errors.Is(err, agents.ErrSkillNotOurs) {
			code = ExitNeedsHuman
			msg += ": a SKILL.md outfit did not write is left alone"
			fix = "move or delete that file, then rerun"
		}
		// A write that failed half way leaves a file the exclude entry has to cover, or
		// `git add -A` would commit it.
		if project && l != nil && !dry && !remove && len(out.Files) > 0 {
			_ = skillsExclude(ctx, l, false)
		}
		return fail((&Error{Code: code, Msg: msg}).WithFix(fix))
	}
	if project && l != nil && !dry {
		if err := skillsExclude(ctx, l, remove); err != nil {
			return fail(Errorf("%v", err))
		}
	}
	if asJSON {
		return writeJSON(e, out, false)
	}
	where := "~"
	if project {
		where = "."
	}
	for _, f := range out.Files {
		act := f.Action
		if dry {
			act = "would be " + act
		}
		fmt.Fprintf(e.Stdout, "%s %s/%s\n", act, where, f.Path)
	}
	switch {
	case dry:
		fmt.Fprintln(e.Stdout, "(dry run: nothing was changed)")
	case !remove:
		fmt.Fprintln(e.Stdout, "Agents pick the skill up when their next session starts. What it points at: outfit skills")
	}
	return ExitOK
}

// skillsExclude keeps the project skill out of `git add -A` (it is personal, like the
// SessionStart entry), and takes the entries away again on remove.
func skillsExclude(ctx context.Context, l *loaded, remove bool) error {
	excl := filepath.Join(l.Repo.CommonDir, "info", "exclude")
	for _, d := range agents.SkillDirs {
		line := "/" + d + "/"
		comment := "# outfit skills install --project: the personal agent skill stays out of commits (" + d + ")"
		if remove {
			// The exclude file is shared by every worktree of the clone: the entry stays while
			// another worktree still has its own copy, or `git add -A` there would commit it.
			if skillElsewhere(l, d) {
				continue
			}
			if _, err := apply.RemoveExcludeEntry(excl, comment, line); err != nil {
				return err
			}
			continue
		}
		if _, err := l.Git.Read(ctx, "check-ignore", "-q", "--", d+"/"+agents.SkillFile); err == nil {
			continue
		}
		if err := apply.AddExcludeEntry(excl, comment, line); err != nil {
			return err
		}
	}
	return nil
}

// skillElsewhere reports whether a worktree other than this one still has outfit's skill in d.
func skillElsewhere(l *loaded, d string) bool {
	for _, w := range uninstallWorktrees(l) {
		if w.Path == "" || filepath.Clean(w.Path) == filepath.Clean(l.Repo.WorkTree) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(w.Path, d, agents.SkillFile)); err == nil {
			return true
		}
	}
	return false
}
