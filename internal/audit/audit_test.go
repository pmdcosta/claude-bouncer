package audit_test

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pmdcosta/claude-bouncer/internal/audit"
	"github.com/stretchr/testify/require"
)

var when = time.Date(2026, 9, 7, 14, 22, 19, 0, time.UTC)

func TestAppendShape(t *testing.T) {
	dir := t.TempDir()
	l := audit.NewLogger(dir)

	l.Append(audit.Record{
		Time:    when,
		Session: "5d6c09b1",
		Cwd:     "/Users/p/Workspace/example-repo",
		Tool:    "Bash",
		Input:   "cd /x && rm -f MEMORY.md",
		Outcome: "ask",
		Rule:    "rm-outside-repo",
	})

	data, err := os.ReadFile(l.Path(when))
	require.NoError(t, err)

	want := `{"ts":"2026-09-07T14:22:19Z","session":"5d6c09b1",` +
		`"cwd":"/Users/p/Workspace/example-repo","tool":"Bash",` +
		`"input":"cd /x && rm -f MEMORY.md","outcome":"ask","rule":"rm-outside-repo"}` + "\n"
	require.Equal(t, want, string(data))
}

func TestFileNameIsMonthly(t *testing.T) {
	require.Equal(t, "bouncer-2026-09.jsonl", audit.FileName(when))
	require.Equal(t, "bouncer-2026-01.jsonl", audit.FileName(time.Date(2026, 1, 31, 23, 59, 0, 0, time.UTC)))
}

func TestZeroLoggerWritesNowhere(t *testing.T) {
	var l audit.Logger

	// must not panic and must not create anything.
	l.Append(audit.Record{Time: when, Outcome: "allow"})
	require.Empty(t, l.Path(when))

	got, err := l.Read(audit.Query{})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestLineCap(t *testing.T) {
	dir := t.TempDir()
	l := audit.NewLogger(dir)

	tests := []struct {
		name  string
		input string
	}{
		{name: "ascii", input: strings.Repeat("x", 8000)},
		{name: "escaping doubles the size", input: strings.Repeat(`"`, 4000)},
		{name: "multibyte runes", input: strings.Repeat("日", 4000)},
		{name: "emoji", input: strings.Repeat("🙂", 2000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l.Append(audit.Record{Time: when, Session: "s", Tool: "Bash", Input: tt.input, Outcome: "allow"})
		})
	}

	f, err := os.Open(l.Path(when))
	require.NoError(t, err)
	defer f.Close()

	lines := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines++
		require.LessOrEqual(t, len(scanner.Bytes())+1, audit.MaxLine, "line %d over the cap", lines)

		var rec audit.Record
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &rec), "line %d is not valid JSON", lines)
	}

	require.NoError(t, scanner.Err())
	require.Equal(t, len(tests), lines)
}

// TestConcurrentAppends proves the cap holds: several worktree sessions append
// to one file, and over 4096 bytes O_APPEND writes interleave and corrupt.
func TestConcurrentAppends(t *testing.T) {
	dir := t.TempDir()

	const (
		writers = 8
		each    = 200
	)

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			l := audit.NewLogger(dir)
			for i := range each {
				l.Append(audit.Record{
					Time:    when,
					Session: "s",
					Tool:    "Bash",
					Input:   strings.Repeat("x", 900+i%400),
					Outcome: "allow",
					Rule:    "",
				})
			}
			_ = w
		}()
	}
	wg.Wait()

	f, err := os.Open(audit.NewLogger(dir).Path(when))
	require.NoError(t, err)
	defer f.Close()

	lines := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines++

		var rec audit.Record
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &rec), "line %d did not survive", lines)
	}

	require.NoError(t, scanner.Err())
	require.Equal(t, writers*each, lines)
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	l := audit.NewLogger(dir)

	l.Append(audit.Record{Time: when.AddDate(0, 0, -20), Outcome: "allow", Input: "old"})
	l.Append(audit.Record{Time: when, Outcome: "allow", Input: "new"})
	l.Append(audit.Record{Time: when, Outcome: "ask", Input: "asked", Rule: "git-stash"})

	// a torn line must not hide the rest of the month.
	f, err := os.OpenFile(l.Path(when), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("{not json\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	t.Run("everything", func(t *testing.T) {
		got, err := l.Read(audit.Query{Since: when.AddDate(0, 0, -30)})
		require.NoError(t, err)
		require.Len(t, got, 3)
	})

	t.Run("since", func(t *testing.T) {
		got, err := l.Read(audit.Query{Since: when.AddDate(0, 0, -7)})
		require.NoError(t, err)
		require.Len(t, got, 2)
	})

	t.Run("outcome", func(t *testing.T) {
		got, err := l.Read(audit.Query{Since: when.AddDate(0, 0, -30), Outcome: "ask"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, "git-stash", got[0].Rule)
	})

	t.Run("no log yet", func(t *testing.T) {
		got, err := audit.NewLogger(t.TempDir()).Read(audit.Query{})
		require.NoError(t, err)
		require.Empty(t, got)
	})
}
