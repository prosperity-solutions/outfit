package config

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
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
				warn(start, "%v: line ignored", err)
				section, subsection, accepted = "", "", false
				continue
			}
			section, subsection = name, sub
			accepted = section == "outfit" && (subsection == "" || subsection == "hook")
			if !accepted {
				warn(start, "section [%s] is not allowed in committed outfit config (only [outfit] and [outfit \"hook\"]); its keys are ignored", sectionLabel(name, sub))
			}
			rest = strings.TrimSpace(rest)
			if rest == "" || rest[0] == '#' || rest[0] == ';' {
				continue
			}
			line = rest // `[outfit] key = value` on one line
		}

		key, val, hasVal, err := parseKeyValue(line)
		if err != nil {
			warn(start, "%v: line ignored", err)
			continue
		}
		if section == "" {
			warn(start, "key %q appears before any section header: ignored", key)
			continue
		}
		if !accepted {
			warn(start, "ignored %s.%s: only outfit.* keys are accepted", sectionLabel(section, subsection), key)
			continue
		}
		if subsection == "hook" {
			key = HookPrefix + key
		}
		nk, err := NormalizeKey(key)
		if err != nil {
			warn(start, "%v: ignored", err)
			continue
		}
		key = nk
		if !hasVal {
			val = "true" // git semantics: a bare key is true
		}
		if strings.ContainsFunc(val, isControl) {
			warn(start, "value of %s contains control characters: ignored", key)
			continue
		}
		if subsection == "" && strings.HasPrefix(key, HookPrefix) {
			warn(start, "%s inside [outfit] is not valid git syntax: git, and so every teammate's `git config -f` fallback, rejects the WHOLE file. Write it as [outfit \"hook\"] %s = %s", key, strings.TrimPrefix(key, HookPrefix), val)
		}

		switch key {
		case KeyProfile:
			warn(start, "`profile` is not allowed in committed config (the active profile is personal; use `outfit use`): ignored")
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
			f.Keys[key] = val
		}
	}
	if err := sc.Err(); err != nil {
		warn(lineNo, "cannot read line: %v", err)
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
		return "", "", false, fmt.Errorf("bad key name in %q", line)
	}
	rest := strings.TrimSpace(line[i:])
	if rest == "" || rest[0] == '#' || rest[0] == ';' {
		return key, "", false, nil
	}
	if rest[0] != '=' {
		return "", "", false, fmt.Errorf("expected = after key %q", key)
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
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case '\\', '"':
				b.WriteByte(s[i])
			default:
				return "", fmt.Errorf("bad escape \\%c", s[i])
			}
		case c == '"':
			inQuote = !inQuote
		case !inQuote && (c == '#' || c == ';'):
			return strings.TrimRight(b.String(), " \t"), nil
		default:
			b.WriteByte(c)
		}
	}
	if inQuote {
		return "", fmt.Errorf("unterminated quote")
	}
	return strings.TrimRight(b.String(), " \t"), nil
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
