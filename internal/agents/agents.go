// Package agents wires outfit into a coding agent's own settings: today one entry,
// a Claude Code SessionStart hook that runs `outfit sync`, so a session that starts
// after the files drifted (git reset --hard fires no hook) starts in sync.
package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/state"
)

// Marker ends the command outfit writes. It is how outfit recognises its own entry
// again (to leave it alone on a second init, to take it away on uninstall) without
// touching anything a person wrote.
const Marker = "# outfit-managed"

// SettingsFile is the personal, uncommitted Claude Code settings file of a
// worktree: outfit is a personal tool (it works for people who do not use it), so
// the entry never goes into the committed .claude/settings.json.
const SettingsFile = ".claude/settings.local.json"

// IgnoreComment and IgnoreLine are the .git/info/exclude entry that keeps the file
// out of `git add -A` (Claude Code does the same when it makes the file itself; a file
// outfit made would otherwise be one careless commit from being published).
const (
	IgnoreComment = "# outfit init --agents: the personal Claude Code settings stay out of commits (outfit uninstall removes this)"
	IgnoreLine    = "/" + SettingsFile
)

// Command is the SessionStart command: silent, never fails the session, finds the
// binary by the path recorded at init and then on PATH.
func Command(exe string) string {
	run := "outfit sync"
	if exe != "" {
		run = shellQuote(exe) + " sync || outfit sync"
	}
	return "{ " + run + "; } >/dev/null 2>&1 || true " + Marker
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// HasClaude reports whether the worktree has a .claude directory.
func HasClaude(worktree string) bool {
	fi, err := os.Lstat(filepath.Join(worktree, ".claude"))
	return err == nil && fi.IsDir()
}

type object = map[string]json.RawMessage

// settingsPath is the settings file of a worktree, after checking that nothing on
// the way is a link: a repository (a teammate's branch, a clone) can commit
// `.claude` or `settings.local.json` as a symlink, and reading through it would copy
// somebody else's settings into this worktree, writing or removing would reach
// outside it. Missing is fine (Install makes it); anything but a plain file is not.
func settingsPath(worktree string) (string, error) {
	if !HasClaude(worktree) {
		return "", errNoClaude
	}
	path := filepath.Join(worktree, filepath.FromSlash(SettingsFile))
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return path, nil
	case err != nil:
		return "", err
	case !fi.Mode().IsRegular():
		return "", fmt.Errorf("%w: %s (a link?): outfit leaves it alone", errNotPlain, path)
	}
	return path, nil
}

var (
	errNoClaude = errors.New("no .claude directory")
	errNotPlain = errors.New("not a plain file")
)

func read(path string) (object, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return object{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return object{}, nil
	}
	var top object
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object outfit can edit safely (%v)", path, err)
	}
	return top, nil
}

// encode is json.Marshal without the HTML escaping (a command with > and & in it
// stays readable in the settings file a person may open).
func encode(v any, indent bool) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func write(path string, top object) error {
	b, err := encode(top, true)
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(path, append(b, '\n'), 0o600)
}

// entries returns the SessionStart array of a settings object.
func entries(top object) ([]json.RawMessage, error) {
	raw, ok := top["hooks"]
	if !ok {
		return nil, nil
	}
	var hooks object
	if err := json.Unmarshal(raw, &hooks); err != nil {
		return nil, errors.New(`"hooks" is not an object`)
	}
	raw, ok = hooks["SessionStart"]
	if !ok {
		return nil, nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, errors.New(`"hooks.SessionStart" is not a list`)
	}
	return list, nil
}

func setEntries(top object, list []json.RawMessage) error {
	var hooks object
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return errors.New(`"hooks" is not an object`)
		}
	}
	if hooks == nil {
		hooks = object{}
	}
	if len(list) == 0 {
		delete(hooks, "SessionStart")
	} else {
		b, err := encode(list, false)
		if err != nil {
			return err
		}
		hooks["SessionStart"] = b
	}
	if len(hooks) == 0 {
		delete(top, "hooks")
		return nil
	}
	b, err := encode(hooks, false)
	if err != nil {
		return err
	}
	top["hooks"] = b
	return nil
}

// isOurs reports whether one hook command is the one outfit wrote.
func isOurs(command string) bool {
	return strings.HasSuffix(strings.TrimSpace(command), Marker)
}

// rewriteItem works on the hooks INSIDE one SessionStart item, never on the item as a
// whole: a person (or Claude Code's own /hooks editor) may have put a command of their
// own next to outfit's. Our hook is replaced by `replace` (the first one found; any
// further copy is dropped) or, with nil, removed. Everything else in the item stays.
// had says the item held one of ours; keep is false when nothing is left in it.
func rewriteItem(item json.RawMessage, replace *json.RawMessage) (out json.RawMessage, had, keep bool) {
	var obj object
	if json.Unmarshal(item, &obj) != nil {
		return item, false, true
	}
	var inner []json.RawMessage
	if raw, ok := obj["hooks"]; !ok || json.Unmarshal(raw, &inner) != nil {
		return item, false, true
	}
	var next []json.RawMessage
	placed := false
	for _, h := range inner {
		var hk struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(h, &hk) != nil || !isOurs(hk.Command) {
			next = append(next, h)
			continue
		}
		had = true
		if replace != nil && !placed {
			next = append(next, *replace)
			placed = true
		}
	}
	if !had {
		return item, false, true
	}
	if len(next) == 0 {
		return nil, true, false
	}
	b, err := encode(next, false)
	if err != nil {
		return item, true, true
	}
	obj["hooks"] = b
	b, err = encode(obj, false)
	if err != nil {
		return item, true, true
	}
	return b, true, true
}

// Install adds (or brings up to date) the SessionStart entry in the worktree's
// settings.local.json, leaving everything else in the file as it is. It reports
// whether the file changed. A worktree without a .claude directory gets nothing.
func Install(worktree, exe string) (changed bool, err error) {
	return install(worktree, exe, true)
}

// WouldInstall reports whether Install would change the file, writing nothing.
func WouldInstall(worktree, exe string) (bool, error) {
	return install(worktree, exe, false)
}

func install(worktree, exe string, write_ bool) (changed bool, err error) {
	path, err := settingsPath(worktree)
	if errors.Is(err, errNoClaude) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	top, err := read(path)
	if err != nil {
		return false, err
	}
	list, err := entries(top)
	if err != nil {
		return false, fmt.Errorf("%s: %v", path, err)
	}
	hook := mustJSON(object{"type": mustJSON("command"), "command": mustJSON(Command(exe))})
	var next []json.RawMessage
	found := false
	for _, it := range list {
		var replace *json.RawMessage
		if !found {
			replace = &hook
		}
		out, had, keep := rewriteItem(it, replace)
		if had {
			found = true
		}
		if keep {
			next = append(next, out)
		}
	}
	if !found {
		item, _ := encode(object{"hooks": mustJSON([]json.RawMessage{hook})}, false)
		next = append(next, item)
	}
	if found && sameJSON(list, next) {
		return false, nil
	}
	if err := setEntries(top, next); err != nil {
		return false, fmt.Errorf("%s: %v", path, err)
	}
	if !write_ {
		return true, nil
	}
	return true, write(path, top)
}

// Remove takes outfit's SessionStart entry away. A settings file that held nothing
// else is removed with it (outfit made it). It reports whether anything changed.
func Remove(worktree string) (changed bool, err error) {
	path, err := settingsPath(worktree)
	// Nothing there, or not a file outfit would ever have written (a link a dotfiles
	// manager made, say): there is nothing of outfit's to remove, and uninstall must
	// not fail over somebody else's file.
	if errors.Is(err, errNoClaude) || errors.Is(err, errNotPlain) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A file without our marker has nothing of ours to remove, and uninstall must not
	// fail over a settings file outfit never wrote (comments, say, make it invalid JSON).
	if raw, rerr := os.ReadFile(path); rerr != nil || !bytes.Contains(raw, []byte(Marker)) {
		return false, nil
	}
	top, err := read(path)
	if err != nil {
		return false, err
	}
	list, err := entries(top)
	if err != nil {
		return false, fmt.Errorf("%s: %v", path, err)
	}
	var next []json.RawMessage
	had := false
	for _, it := range list {
		out, h, keep := rewriteItem(it, nil)
		had = had || h
		if keep {
			next = append(next, out)
		}
	}
	if !had {
		return false, nil
	}
	if err := setEntries(top, next); err != nil {
		return false, fmt.Errorf("%s: %v", path, err)
	}
	if len(top) == 0 {
		return true, os.Remove(path)
	}
	return true, write(path, top)
}

// Installed reports whether the worktree's settings carry outfit's entry.
func Installed(worktree string) bool {
	path, err := settingsPath(worktree)
	if err != nil {
		return false
	}
	top, err := read(path)
	if err != nil {
		return false
	}
	list, _ := entries(top)
	for _, it := range list {
		if _, had, _ := rewriteItem(it, nil); had {
			return true
		}
	}
	return false
}

func mustJSON(v any) json.RawMessage {
	b, err := encode(v, false)
	if err != nil {
		panic(err)
	}
	return b
}

func sameJSON(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		var x, y any
		if json.Unmarshal(a[i], &x) != nil || json.Unmarshal(b[i], &y) != nil {
			return false
		}
		xb, _ := json.Marshal(x)
		yb, _ := json.Marshal(y)
		if !bytes.Equal(xb, yb) {
			return false
		}
	}
	return true
}
