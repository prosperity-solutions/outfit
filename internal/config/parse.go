package config

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxFileSize bounds a committed config file. Nothing legitimate comes close.
const MaxFileSize = 1 << 20

// Kind says which committed file is being parsed.
type Kind int

const (
	// KindTeam is `.outfit/config`: toggles and `locked`.
	KindTeam Kind = iota
	// KindProfile is `.outfit/profiles/<name>/profile.config`: toggles only.
	KindProfile
)

// File is the accepted content of one committed config file.
type File struct {
	// Source is the path used in warnings.
	Source string
	// Keys maps normalized key to its last value.
	Keys map[string]string
	// Locked is the union of every `locked` line (team files only).
	Locked []string
	// Replace lists the directories a profile replaces as a whole (`[outfit "profile"]
	// replace = <dir>`, one line each; profile.config only). Only the syntax is checked here:
	// what a path may be is decided where it is used (internal/plan.ValidSwapPath).
	Replace []string
	// Warnings lists everything that was ignored, with line numbers.
	Warnings []Warning
}

// ParseFile parses git-config-syntax text, accepting ONLY `outfit.*` keys.
// Anything else (core.fsmonitor, include.path, includeIf…, other sections,
// malformed lines, reserved keys in the wrong file) is ignored with a warning;
// parsing never fails and never executes or follows anything.
func ParseFile(r io.Reader, source string, kind Kind) *File {
	f := &File{Source: source, Keys: map[string]string{}}
	warn := func(line int, format string, a ...any) {
		f.Warnings = append(f.Warnings, Warning{Source: source, Line: line, Message: fmt.Sprintf(format, a...)})
	}

	data, err := io.ReadAll(io.LimitReader(r, MaxFileSize+1))
	if err != nil {
		warn(0, "cannot read: %v", err)
		return f
	}
	if len(data) > MaxFileSize {
		warn(0, "file is larger than %d bytes: ignored", MaxFileSize)
		return f
	}

	// Git accepts a UTF-8 byte order mark; a file with one must not lose its
	// `locked` line to a "bad key" on line 1.
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))

	var (
		section    string // lowercased section name, "" before the first header
		subsection string
		accepted   bool // does the current section hold outfit keys?
	)
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), MaxFileSize)
	lineNo := 0
	var pending string // line continuation
	pendingStart := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		if pending != "" {
			raw = pending + raw
			pending = ""
		} else {
			pendingStart = lineNo
		}
		if continues(raw) {
			pending = raw[:len(raw)-1]
			continue
		}
		line := strings.TrimSpace(raw)
		start := pendingStart
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}

		if line[0] == '[' {
			name, sub, rest, err := parseHeader(line)
			if err != nil {
				warn(start, "%v: line ignored%s", err, gitRejects)
				section, subsection, accepted = "", "", false
				continue
			}
			section, subsection = name, sub
			accepted = section == "outfit" && (subsection == "" || subsection == "hook" || subsection == "profile")
			if !accepted {
				warn(start, "section [%s] is not allowed in committed outfit config (only [outfit], [outfit \"hook\"] and, in a profile, [outfit \"profile\"]); its keys are ignored", sectionLabel(name, sub))
			}
			rest = strings.TrimSpace(rest)
			if rest == "" || rest[0] == '#' || rest[0] == ';' {
				continue
			}
			line = rest // `[outfit] key = value` on one line
		}

		key, val, hasVal, err := parseKeyValue(line)
		if err != nil {
			warn(start, "%v: line ignored%s", err, gitRejects)
			continue
		}
		if section == "" {
			warn(start, "key %q appears before any section header: put it under [outfit]: ignored%s", esc(key), gitRejects)
			continue
		}
		if !accepted {
			warn(start, "ignored %s.%s: only outfit.* keys are accepted", sectionLabel(section, subsection), key)
			continue
		}
		if subsection == "profile" {
			// [outfit "profile"] replace = <dir>: not a toggle (it never reaches `outfit get`, `git -c` or
			// the resolution order); a profile's own declaration, read from its own file.
			switch {
			case !strings.EqualFold(key, "replace"):
				warn(start, "unknown key %s in [outfit \"profile\"] (only `replace`): ignored", esc(key))
			case kind != KindProfile:
				warn(start, "`replace` is only valid in a profile's profile.config: ignored")
			case !hasVal || strings.TrimSpace(val) == "":
				warn(start, "`replace` needs a directory (replace = .claude/skills): ignored")
			case strings.ContainsFunc(val, isControl) || !utf8.ValidString(val):
				warn(start, "value of replace contains control characters or invalid UTF-8: ignored")
			default:
				f.Replace = append(f.Replace, strings.TrimSpace(val))
			}
			continue
		}
		if subsection == "hook" {
			key = HookPrefix + key
		}
		nk, err := NormalizeKey(key)
		if err != nil {
			warn(start, "%v: ignored%s", err, gitRejects)
			continue
		}
		key = nk
		if !hasVal {
			val = "true" // git semantics: a bare key is true
		}
		if strings.ContainsFunc(val, isControl) || !utf8.ValidString(val) {
			warn(start, "value of %s contains control characters or invalid UTF-8: ignored", key)
			continue
		}
		if subsection == "" && strings.HasPrefix(key, HookPrefix) {
			warn(start, "%s inside [outfit] is not valid git syntax: git, and so every teammate's `git config -f` fallback, rejects the WHOLE file. Write it as [outfit \"hook\"] %s = %s", key, strings.TrimPrefix(key, HookPrefix), val)
		}

		switch key {
		case KeyProfile:
			warn(start, "`profile` is not allowed in committed config (the active profile is personal; choose one with `outfit use <name>`): ignored")
		case KeyLocked:
			if kind != KindTeam {
				warn(start, "`locked` is only valid in .outfit/config: ignored")
				continue
			}
			for _, k := range strings.FieldsFunc(val, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
				lk, err := NormalizeKey(k)
				if err != nil {
					warn(start, "locked: %v: ignored", err)
					continue
				}
				f.Locked = append(f.Locked, lk)
			}
		default:
			if hint := spellingHint(val, hasVal); hint != "" {
				warn(start, "%s = %s: %s", key, esc(val), hint)
			}
			f.Keys[key] = val
		}
	}
	if err := sc.Err(); err != nil {
		warn(lineNo, "cannot read line: %v", err)
	}
	// `locked = pre-push` locks the key "pre-push", not the hook toggle
	// "hook.pre-push": the natural spelling for a hook protects nothing.
	for _, l := range f.Locked {
		if _, ok := f.Keys[l]; !ok {
			if _, hook := f.Keys[HookPrefix+l]; hook {
				warn(0, "locked = %s locks the key %q, which this file never sets; the hook toggle is %q: write `locked = %s%s`", l, l, HookPrefix+l, HookPrefix, l)
			}
		}
	}
	return f
}

func sectionLabel(name, sub string) string {
	if sub == "" {
		return esc(name)
	}
	return fmt.Sprintf("%s %q", esc(name), sub)
}

// esc makes untrusted text safe to print: no raw control characters.
func esc(s string) string { return strings.Trim(strconv.Quote(s), `"`) }

// gitRejects is appended to warnings about lines git's own parser also refuses:
// there `git config -f .outfit/config` (the non-user fallback of §3.0) dies on
// the WHOLE file, so the author must fix the line, not just read the warning.
const gitRejects = " (git rejects this line too: teammates' `git config -f` fallback fails on the whole file)"

// spellingHint explains a value that looks like a boolean but is not exactly
// `on` or `off`. Hooks compare against the literal string `off`, so `OFF`,
// `false` or `no` would silently keep a check running; a bare key reads as
// `true` for outfit but as the empty string for `git config --get`.
func spellingHint(val string, hasVal bool) string {
	switch strings.ToLower(val) {
	case "on", "off":
		if val == "on" || val == "off" {
			return ""
		}
	case "true", "false", "yes", "no", "1", "0":
	default:
		return ""
	}
	if !hasVal {
		return "a bare key reads as `true` here but as an empty value for `git config --get`: write `key = on` or `key = off`"
	}
	return "toggles are compared as the exact strings `on` and `off`: write `on` or `off`"
}

// isControl reports a byte that has no business in a toggle value: NUL, ESC,
// backspace, CR... (tab is fine). Such values could spoof terminal output.
func isControl(r rune) bool { return (r < 0x20 && r != '\t') || r == 0x7f }

// continues reports whether a physical line ends in an ACTIVE backslash
// continuation, the way git decides it: only inside a value, never inside a
// comment (`# note \`), a header-only line, a bare key, or after an unquoted
// `#`/`;`; and only for an odd number of trailing backslashes.
func continues(raw string) bool {
	line := strings.TrimSpace(raw)
	if line == "" || line[0] == '#' || line[0] == ';' {
		return false
	}
	if line[0] == '[' {
		_, _, rest, err := parseHeader(line)
		if err != nil {
			return false
		}
		line = strings.TrimSpace(rest)
	}
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return false // header-only line or bare key
	}
	value := line[eq+1:]
	inQuote := false
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case c == '\\':
			if i+1 == len(value) {
				return true
			}
			i++
		case c == '"':
			inQuote = !inQuote
		case !inQuote && (c == '#' || c == ';'):
			return false
		}
	}
	return false
}

// parseHeader parses `[name]`, `[name "sub"]` or `[name.sub]` and returns what
// follows the closing bracket.
func parseHeader(line string) (name, sub, rest string, err error) {
	end := strings.IndexByte(line, ']')
	// A quoted subsection may itself contain ']'.
	if q := strings.IndexByte(line, '"'); q >= 0 && q < end {
		close := closingQuote(line, q)
		if close < 0 {
			return "", "", "", fmt.Errorf("unterminated subsection in %q", line)
		}
		end = strings.IndexByte(line[close:], ']')
		if end < 0 {
			return "", "", "", fmt.Errorf("missing ] in %q", line)
		}
		end += close
	}
	if end < 0 {
		return "", "", "", fmt.Errorf("missing ] in %q", line)
	}
	inner, rest := line[1:end], line[end+1:]
	if q := strings.IndexByte(inner, '"'); q >= 0 {
		// git requires whitespace before the quote and rejects an empty name;
		// reading either differently would make outfit users and non-users
		// disagree about the same committed file.
		if q == 0 || (inner[q-1] != ' ' && inner[q-1] != '\t') {
			return "", "", "", fmt.Errorf("a quoted subsection needs whitespace before it")
		}
		name = strings.ToLower(strings.TrimSpace(inner[:q]))
		raw := inner[q:]
		s, err := unquote(raw)
		if err != nil {
			return "", "", "", fmt.Errorf("bad subsection %s: %v", raw, err)
		}
		if s == "" {
			return "", "", "", fmt.Errorf("empty subsection name")
		}
		sub = s
	} else if dot := strings.IndexByte(inner, '.'); dot >= 0 { // deprecated [name.sub]
		name, sub = strings.ToLower(strings.TrimSpace(inner[:dot])), strings.ToLower(strings.TrimSpace(inner[dot+1:]))
	} else {
		name = strings.ToLower(strings.TrimSpace(inner))
	}
	if name == "" {
		return "", "", "", fmt.Errorf("empty section name")
	}
	return name, sub, rest, nil
}

func closingQuote(s string, open int) int {
	for i := open + 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// parseKeyValue parses `key`, `key = value` with git's quoting and comments.
func parseKeyValue(line string) (key, val string, hasVal bool, err error) {
	i := 0
	for i < len(line) && (isKeyChar(line[i])) {
		i++
	}
	key = strings.ToLower(line[:i])
	if key == "" || key[0] < 'a' || key[0] > 'z' {
		return "", "", false, fmt.Errorf("bad key name in %q (keys are lowercase letters, digits and dashes: no underscores)", esc(line))
	}
	rest := strings.TrimSpace(line[i:])
	if rest == "" || rest[0] == '#' || rest[0] == ';' {
		return key, "", false, nil
	}
	if rest[0] != '=' {
		return "", "", false, fmt.Errorf("unexpected %q after key %q: keys are lowercase letters, digits and dashes (no underscores), followed by `=`", esc(rest[:1]), esc(key))
	}
	val, err = parseValue(strings.TrimSpace(rest[1:]))
	if err != nil {
		return "", "", false, err
	}
	return key, val, true, nil
}

// isKeyChar admits the dot too: outfit accepts `hook.pre-commit = off` inside
// [outfit] (HANDOVER.md §3.1) even though git itself would reject that line.
func isKeyChar(c byte) bool {
	return c == '-' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func parseValue(s string) (string, error) {
	var b strings.Builder
	keep := 0 // length of b up to the last character that must stay
	write := func(c byte, quoted bool) {
		b.WriteByte(c)
		if quoted || (c != ' ' && c != '\t') {
			keep = b.Len()
		}
	}
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			i++
			if i >= len(s) {
				return "", fmt.Errorf("trailing backslash")
			}
			switch s[i] {
			case 'n':
				write('\n', true)
			case 't':
				write('\t', true)
			case 'b':
				write('\b', true)
			case '\\', '"':
				write(s[i], true)
			default:
				return "", fmt.Errorf("bad escape \\%c", s[i])
			}
		case c == '"':
			inQuote = !inQuote
			keep = b.Len() // a quote, even an empty "", ends the trimmable tail
		case !inQuote && (c == '#' || c == ';'):
			return b.String()[:keep], nil
		default:
			write(c, inQuote)
		}
	}
	if inQuote {
		return "", fmt.Errorf("unterminated quote")
	}
	return b.String()[:keep], nil
}

// unquote parses a "double quoted" subsection name.
func unquote(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("not a quoted string")
	}
	var b strings.Builder
	for i := 1; i < len(s)-1; i++ {
		if s[i] == '\\' && i+1 < len(s)-1 {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String(), nil
}
