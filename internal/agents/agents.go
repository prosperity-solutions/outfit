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

// ours reports whether a SessionStart item is the one outfit wrote.
func ours(item json.RawMessage) bool {
	var it struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(item, &it) != nil {
		return false
	}
	for _, h := range it.Hooks {
		if strings.HasSuffix(strings.TrimSpace(h.Command), Marker) {
			return true
		}
	}
	return false
}

// Install adds (or brings up to date) the SessionStart entry in the worktree's
// settings.local.json, leaving everything else in the file as it is. It reports
// whether the file changed. A worktree without a .claude directory gets nothing.
func Install(worktree, exe string) (changed bool, err error) {
	if !HasClaude(worktree) {
		return false, nil
	}
	path := filepath.Join(worktree, filepath.FromSlash(SettingsFile))
	top, err := read(path)
	if err != nil {
		return false, err
	}
	list, err := entries(top)
	if err != nil {
		return false, fmt.Errorf("%s: %v", path, err)
	}
	item, _ := encode(object{"hooks": mustJSON([]object{{"type": mustJSON("command"), "command": mustJSON(Command(exe))}})}, false)
	var next []json.RawMessage
	found := false
	for _, it := range list {
		if ours(it) {
			if !found {
				next = append(next, item)
			}
			found = true
			continue
		}
		next = append(next, it)
	}
	if !found {
		next = append(next, item)
	}
	if found && len(next) == len(list) && sameJSON(list, next) {
		return false, nil
	}
	if err := setEntries(top, next); err != nil {
		return false, fmt.Errorf("%s: %v", path, err)
	}
	return true, write(path, top)
}

// Remove takes outfit's SessionStart entry away. A settings file that held nothing
// else is removed with it (outfit made it). It reports whether anything changed.
func Remove(worktree string) (changed bool, err error) {
	path := filepath.Join(worktree, filepath.FromSlash(SettingsFile))
	top, err := read(path)
	if err != nil {
		return false, err
	}
	list, err := entries(top)
	if err != nil {
		return false, fmt.Errorf("%s: %v", path, err)
	}
	var next []json.RawMessage
	for _, it := range list {
		if !ours(it) {
			next = append(next, it)
		}
	}
	if len(next) == len(list) {
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
	top, err := read(filepath.Join(worktree, filepath.FromSlash(SettingsFile)))
	if err != nil {
		return false
	}
	list, _ := entries(top)
	for _, it := range list {
		if ours(it) {
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
