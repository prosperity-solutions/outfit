package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Stable exit codes (HANDOVER.md §4).
const (
	ExitOK         = 0
	ExitError      = 1
	ExitUsage      = 2 // usage, or unknown profile/key
	ExitNeedsHuman = 3 // conflict or quarantine
	ExitOutOfSync  = 4 // out of sync or stale (status only)
)

// Error is a command failure that knows its exit code and the exact fix. Agents
// follow error text, so the text is the guide.
type Error struct {
	Code      int
	Msg       string
	Fix       string // exact command to run, "" when there is none
	Retryable bool
}

func (e *Error) Error() string { return e.Msg }

// Errorf builds an *Error with ExitError.
func Errorf(format string, a ...any) *Error {
	return &Error{Code: ExitError, Msg: fmt.Sprintf(format, a...)}
}

// Usagef builds an *Error with ExitUsage.
func Usagef(format string, a ...any) *Error {
	return &Error{Code: ExitUsage, Msg: fmt.Sprintf(format, a...)}
}

// WithFix returns a copy of e carrying the exact fix command.
func (e *Error) WithFix(fix string) *Error { c := *e; c.Fix = fix; return &c }

// jsonError is the --json shape of a failure.
type jsonError struct {
	Error     string `json:"error"`
	Fix       string `json:"fix,omitempty"`
	Retryable bool   `json:"retryable"`
	Code      int    `json:"code"`
}

// report prints err and returns the exit code to use.
func report(stdout, stderr io.Writer, asJSON bool, err error) int {
	var ce *Error
	if !errors.As(err, &ce) {
		ce = &Error{Code: ExitError, Msg: err.Error()}
	}
	if asJSON {
		b, _ := json.Marshal(jsonError{Error: ce.Msg, Fix: ce.Fix, Retryable: ce.Retryable, Code: ce.Code})
		fmt.Fprintln(stdout, string(b))
		return ce.Code
	}
	fmt.Fprintf(stderr, "outfit: %s\n", ce.Msg)
	if ce.Fix != "" {
		fmt.Fprintf(stderr, "fix: %s\n", ce.Fix)
	}
	return ce.Code
}
