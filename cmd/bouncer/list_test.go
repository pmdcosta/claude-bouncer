package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// heredoc is a real logged command: twenty lines of Python, which destroyed
// the column layout before the output was collapsed.
const heredoc = `cd "$TMPDIR" && python3 - <<'PY'
import json,os,base64,urllib.request

def get(url, hdrs):
    req=urllib.request.Request(url, headers=hdrs)
    with urllib.request.urlopen(req, timeout=20) as r:
        return r.status, r.read()
PY`

// logRecords writes a mixed set of records into the sandbox's log.
func logRecords(t *testing.T, s sandbox) {
	t.Helper()

	run(t, request(t, s.repo, "Bash", map[string]string{"command": "grep -rn foo ."}))
	run(t, request(t, s.repo, "Bash", map[string]string{"command": heredoc}))
	run(t, request(t, s.repo, "Bash", map[string]string{"command": "git stash pop"}))
}

// TestLogCollapsesMultiLineCommands is the readability fix: one record is
// always exactly one row.
func TestLogCollapsesMultiLineCommands(t *testing.T) {
	s := newSandbox(t, "ins-1")
	logRecords(t, s)

	var out bytes.Buffer
	require.NoError(t, runLog(&out, logOptions{since: "7d", width: 100}))

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	require.Len(t, lines, 3, "three records must print as three rows")

	for _, line := range lines {
		require.LessOrEqual(t, len([]rune(line)), 100, line)
	}

	// the heredoc row keeps the start of the command and marks the cut.
	require.Contains(t, lines[1], `cd "$TMPDIR" && python3 - <<'PY' import json`)
	require.True(t, strings.HasSuffix(lines[1], "…"), lines[1])

	// a short command is neither collapsed nor cut.
	require.Contains(t, lines[2], "git stash pop")
	require.NotContains(t, lines[2], "…")
}

func TestLogFullPrintsEveryLine(t *testing.T) {
	s := newSandbox(t, "ins-1")
	logRecords(t, s)

	var out bytes.Buffer
	require.NoError(t, runLog(&out, logOptions{since: "7d", full: true, width: 100}))

	// every line of the heredoc survives, indented under its header.
	for _, line := range strings.Split(heredoc, "\n") {
		require.Contains(t, out.String(), "    "+line)
	}

	require.NotContains(t, out.String(), "…")
}

// TestLogColorIsOptOut proves a redirect or a pipe gets plain text.
func TestLogColorIsOptOut(t *testing.T) {
	s := newSandbox(t, "ins-1")
	logRecords(t, s)

	var plain, painted bytes.Buffer
	require.NoError(t, runLog(&plain, logOptions{since: "7d", width: 100}))
	require.NoError(t, runLog(&painted, logOptions{since: "7d", width: 100, color: true}))

	require.NotContains(t, plain.String(), "\x1b[")
	require.Contains(t, painted.String(), "\x1b[")

	// stripping the escapes gives back exactly the plain output, which means
	// colour never shifts a column.
	require.Equal(t, plain.String(), stripANSI(painted.String()))

	// allow is green, ask is yellow, a named rule is cyan.
	require.Contains(t, painted.String(), "\x1b[32mallow")
	require.Contains(t, painted.String(), "\x1b[33mask")
	require.Contains(t, painted.String(), "\x1b[36mgit-stash")
}

func TestColorEnabled(t *testing.T) {
	// a plain file is never a terminal.
	f, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	defer f.Close()

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	require.False(t, colorEnabled(f))

	t.Run("NO_COLOR wins", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		require.False(t, colorEnabled(os.Stdout))
	})

	t.Run("dumb terminal", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		t.Setenv("TERM", "dumb")
		require.False(t, colorEnabled(os.Stdout))
	})
}

func TestTerminalWidth(t *testing.T) {
	tests := []struct {
		columns string
		want    int
	}{
		{columns: "", want: defaultWidth},
		{columns: "not a number", want: defaultWidth},
		{columns: "10", want: defaultWidth},
		{columns: "200", want: 200},
	}

	for _, tt := range tests {
		t.Run(tt.columns, func(t *testing.T) {
			t.Setenv("COLUMNS", tt.columns)
			require.Equal(t, tt.want, terminalWidth())
		})
	}
}

func TestCollapse(t *testing.T) {
	require.Equal(t, "a b c", collapse("a\n  b\t\tc"))
	require.Equal(t, "a b", collapse("\n a \n b \n"))
	require.Empty(t, collapse("  \n\t "))
	require.Equal(t, "git stash pop", collapse("git stash pop"))
}

func TestClip(t *testing.T) {
	require.Equal(t, "abcde", clip("abcde", 5))
	require.Equal(t, "abcd…", clip("abcdef", 5))
	require.Equal(t, "…", clip("abcdef", 1))
	require.Equal(t, "abcdef", clip("abcdef", 0), "zero means never cut")
	// cutting counts runes, not bytes, so a multibyte command is not mangled.
	require.Equal(t, "日本…", clip("日本語です", 3))
}

func TestRulesColor(t *testing.T) {
	s := newSandbox(t, "ins-1")
	require.NoError(t, os.MkdirAll(s.config, 0o700))
	require.NoError(t, os.WriteFile(s.config+"/rules.yaml", []byte(
		"rules:\n  - name: git-config\n    enabled: false\n  - name: mine\n    type: cmd\n    args: [terraform]\n"), 0o600))

	var plain, painted bytes.Buffer
	require.NoError(t, runRules(&plain, false, false))
	require.NoError(t, runRules(&painted, false, true))

	require.NotContains(t, plain.String(), "\x1b[")
	require.Equal(t, plain.String(), stripANSI(painted.String()))

	require.Contains(t, painted.String(), "\x1b[33m[disabled]")
	require.Contains(t, painted.String(), "\x1b[36m[file]")
}

// stripANSI removes every escape sequence, so painted and plain output can be
// compared column for column.
func stripANSI(s string) string {
	var b strings.Builder

	for {
		start := strings.Index(s, "\x1b[")
		if start < 0 {
			b.WriteString(s)

			return b.String()
		}

		b.WriteString(s[:start])

		end := strings.IndexByte(s[start:], 'm')
		if end < 0 {
			return b.String()
		}

		s = s[start+end+1:]
	}
}
