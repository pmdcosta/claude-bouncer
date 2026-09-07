// These tests live in package main because a command has no importable API.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pmdcosta/claude-bouncer/internal/audit"
	"github.com/stretchr/testify/require"
)

const (
	goldenAllow = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}` + "\n"
	goldenAsk   = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest"}}` + "\n"
)

// sandbox points HOME and the config directory at temp directories, so a test
// can never read or write the real machine's settings, rules or logs.
type sandbox struct {
	home   string
	config string
	repo   string
}

func newSandbox(t *testing.T, branch string) sandbox {
	t.Helper()

	home := t.TempDir()
	config := filepath.Join(home, ".config", "bouncer")

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	repo := filepath.Join(home, "Workspace", "insurance-mono")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/"+branch+"\n"), 0o644))

	return sandbox{home: home, config: config, repo: repo}
}

// logLines returns the audit records written this month.
func (s sandbox) logLines(t *testing.T) []audit.Record {
	t.Helper()

	records, err := audit.NewLogger(filepath.Join(s.home, ".claude", "logs")).Read(audit.Query{})
	require.NoError(t, err)

	return records
}

// run feeds a request through the real decide path.
func run(t *testing.T, body string) (stdout, stderr string) {
	t.Helper()

	var out, errs bytes.Buffer
	require.NoError(t, decide(strings.NewReader(body), &out, &errs))

	return out.String(), errs.String()
}

// request builds a realistic PermissionRequest payload.
func request(t *testing.T, cwd, tool string, input any) string {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"session_id":      "5d6c09b1",
		"cwd":             cwd,
		"hook_event_name": "PermissionRequest",
		"tool_name":       tool,
		"tool_input":      input,
	})
	require.NoError(t, err)

	return string(raw)
}

func TestDecideGoldenBytes(t *testing.T) {
	s := newSandbox(t, "ins-2593-foo")

	t.Run("allow", func(t *testing.T) {
		out, errs := run(t, request(t, s.repo, "Bash", map[string]string{"command": "grep -rn foo ./services"}))
		require.Equal(t, goldenAllow, out)
		require.Empty(t, errs)
	})

	t.Run("ask", func(t *testing.T) {
		out, errs := run(t, request(t, s.repo, "Bash", map[string]string{"command": "git stash pop"}))
		require.Equal(t, goldenAsk, out)
		require.Empty(t, errs)
	})
}

// TestDecideNeverAllowsOnAnErrorPath is the rule the whole design rests on.
func TestDecideNeverAllowsOnAnErrorPath(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty stdin", body: ""},
		{name: "not json", body: "not json"},
		{name: "truncated json", body: `{"tool_name":`},
		{name: "wrong event", body: `{"hook_event_name":"PreToolUse","tool_name":"Bash"}`},
		{name: "unparseable command", body: `{"cwd":"/x","tool_name":"Bash","tool_input":{"command":"rm -f 'unterminated"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newSandbox(t, "ins-1")

			out, errs := run(t, tt.body)
			require.Equal(t, goldenAsk, out)
			require.NotEmpty(t, errs, "the reason must reach stderr")
		})
	}
}

// TestDecideWithABrokenRulesFile proves a typo cannot silently shorten the
// live list: the compiled defaults still decide, and the error is recorded.
func TestDecideWithABrokenRulesFile(t *testing.T) {
	s := newSandbox(t, "ins-1")
	require.NoError(t, os.MkdirAll(s.config, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(s.config, "rules.yaml"), []byte("rules:\n  - name: x\n    type: nonsense\n"), 0o600))

	out, errs := run(t, request(t, s.repo, "Bash", map[string]string{"command": "git stash pop"}))
	require.Equal(t, goldenAsk, out, "the default rule still fires")
	require.Contains(t, errs, "unknown type")

	records := s.logLines(t)
	require.Len(t, records, 1)
	require.Equal(t, "git-stash", records[0].Rule)
	require.Contains(t, records[0].Error, "unknown type")
}

func TestDecideWritesTheAuditLog(t *testing.T) {
	s := newSandbox(t, "ins-2593-foo")

	run(t, request(t, s.repo, "Bash", map[string]string{"command": "grep -rn foo ."}))
	run(t, request(t, s.repo, "Bash", map[string]string{"command": "cd " + s.home + " && rm -f x"}))
	run(t, request(t, s.repo, "Write", map[string]string{"file_path": s.home + "/.claude/settings.json"}))

	records := s.logLines(t)
	require.Len(t, records, 3)

	require.Equal(t, "allow", records[0].Outcome)
	require.Empty(t, records[0].Rule)
	require.Equal(t, "5d6c09b1", records[0].Session)
	require.Equal(t, s.repo, records[0].Cwd)
	require.Equal(t, "grep -rn foo .", records[0].Input)

	require.Equal(t, "ask", records[1].Outcome)
	require.Equal(t, "rm-outside-repo", records[1].Rule)

	// a file tool logs its path, since it has no command.
	require.Equal(t, "ask", records[2].Outcome)
	require.Equal(t, "config-paths", records[2].Rule)
	require.Equal(t, s.home+"/.claude/settings.json", records[2].Input)
}

func TestDecideCapsTheLoggedInput(t *testing.T) {
	s := newSandbox(t, "ins-1")

	run(t, request(t, s.repo, "Bash", map[string]string{"command": "echo " + strings.Repeat("x", 9000)}))

	data, err := os.ReadFile(filepath.Join(s.home, ".claude", "logs", audit.FileName(s.logLines(t)[0].Time)))
	require.NoError(t, err)
	require.LessOrEqual(t, len(data), audit.MaxLine)
	require.Contains(t, string(data), "...")
}
