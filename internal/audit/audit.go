// Package audit appends one JSON line per permission decision.
//
// Claude Code only persists denials, so without this there is no record of
// what was approved. JSONL keeps jq a first-class way in, which is how the
// rule list gets tuned.
package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

// MaxLine is the hard cap on one written line, including its newline.
//
// Several worktree sessions append to the same file at once, and POSIX
// guarantees an atomic O_APPEND write only below 4096 bytes. Staying an order
// of magnitude under that keeps lines from interleaving and corrupting each
// other. This is the reason for the cap, not tidiness.
const MaxLine = 1024

// Record is one decision.
type Record struct {
	Time    time.Time `json:"ts"`
	Session string    `json:"session,omitempty"`
	Cwd     string    `json:"cwd,omitempty"`
	Tool    string    `json:"tool,omitempty"`
	Input   string    `json:"input,omitempty"`
	Outcome string    `json:"outcome"`
	// Rule is the rule that required a prompt, empty for a default allow.
	Rule string `json:"rule,omitempty"`
	// Error records a problem bouncer hit while deciding, such as a broken
	// rules file. Hook errors surface only under claude --debug, so this is
	// where they are actually visible.
	Error string `json:"error,omitempty"`
}

// Logger appends records to a monthly file in a directory.
//
// The zero value writes nowhere and never fails, so a caller that could not
// determine a log directory can carry on with a zero Logger.
type Logger struct {
	dir string
}

// NewLogger returns a logger writing to dir.
func NewLogger(dir string) Logger {
	return Logger{dir: dir}
}

// DefaultDir returns the log directory beside Claude Code's own state.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to locate home directory: %w", err)
	}

	return filepath.Join(home, ".claude", "logs"), nil
}

// FileName returns the log file name for a month.
func FileName(t time.Time) string {
	return fmt.Sprintf("bouncer-%s.jsonl", t.UTC().Format("2006-01"))
}

// Path returns the full path of the log file a time belongs in.
func (l Logger) Path(t time.Time) string {
	if l.dir == "" {
		return ""
	}

	return filepath.Join(l.dir, FileName(t))
}

// Append writes one record.
//
// It never returns an error and never panics: a failed log write must not be
// able to change a permission decision.
func (l Logger) Append(rec Record) {
	defer func() {
		// a write problem is not worth crashing a permission decision over.
		_ = recover()
	}()

	if l.dir == "" {
		return
	}

	line := encode(rec)
	if line == nil {
		return
	}

	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return
	}

	f, err := os.OpenFile(l.Path(rec.Time), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()

	// one write, under MaxLine, so concurrent appends cannot interleave.
	_, _ = f.Write(line)
}

// encode marshals the record and shrinks it until the line fits MaxLine.
//
// HTML escaping is off: "&&" and ">" are everywhere in shell commands, and
// \u0026\u0026 makes the log unreadable by eye for no benefit.
func encode(rec Record) []byte {
	if rec.Time.IsZero() {
		rec.Time = time.Now()
	}
	rec.Time = rec.Time.UTC().Truncate(time.Second)

	line, err := marshal(rec)
	if err != nil {
		return nil
	}

	// the input is the only unbounded field, so it is what gets cut.
	for len(line) > MaxLine && rec.Input != "" {
		rec.Input = shorten(rec.Input, len(line)-MaxLine)

		line, err = marshal(rec)
		if err != nil {
			return nil
		}
	}

	if len(line) > MaxLine {
		// nothing left to cut but the record itself, so keep only the fields
		// that make the line worth having.
		line, err = marshal(Record{Time: rec.Time, Session: rec.Session, Outcome: rec.Outcome, Rule: rec.Rule})
		if err != nil || len(line) > MaxLine {
			return nil
		}
	}

	return line
}

// marshal encodes one record as a line, newline included.
func marshal(rec Record) ([]byte, error) {
	var b bytes.Buffer

	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(rec); err != nil {
		return nil, fmt.Errorf("failed to encode audit record: %w", err)
	}

	return b.Bytes(), nil
}

// ellipsis marks a truncated input.
const ellipsis = "..."

// shorten removes at least over bytes from s, cutting on a rune boundary so
// the result stays valid UTF-8 and cannot grow again once escaped.
func shorten(s string, over int) string {
	keep := len(s) - over - len(ellipsis)
	if keep <= 0 {
		return ""
	}

	for keep > 0 && !utf8.ValidString(s[:keep]) {
		keep--
	}

	if keep == 0 {
		return ""
	}

	return s[:keep] + ellipsis
}
