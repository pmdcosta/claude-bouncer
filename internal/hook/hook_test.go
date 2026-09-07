package hook_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pmdcosta/claude-bouncer/internal/hook"
	"github.com/pmdcosta/claude-bouncer/internal/rules"
	"github.com/stretchr/testify/require"
)

// The exact stdout bytes. A wrong shape here fails silently at runtime: no
// error, the hook simply does nothing.
const (
	goldenAllow = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}` + "\n"
	goldenAsk   = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest"}}` + "\n"
)

func TestWriteGoldenBytes(t *testing.T) {
	t.Run("allow", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer
		require.NoError(t, hook.Write(&b, hook.Allow))
		require.Equal(t, goldenAllow, b.String())
	})

	t.Run("ask", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer
		require.NoError(t, hook.Write(&b, hook.Ask))
		require.Equal(t, goldenAsk, b.String())
	})
}

// TestAskIsTheZeroValue guards the rule that no error path can ever allow.
func TestAskIsTheZeroValue(t *testing.T) {
	var d hook.Decision
	require.Equal(t, hook.Ask, d)
	require.Equal(t, "ask", d.String())
	require.Equal(t, "allow", hook.Allow.String())
}

func TestReadMalformed(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty stdin", body: ""},
		{name: "not json", body: "not json"},
		{name: "truncated", body: `{"tool_name":`},
		{name: "wrong event", body: `{"hook_event_name":"PreToolUse","tool_name":"Bash"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := hook.Read(strings.NewReader(tt.body))
			require.Error(t, err)

			// whatever went wrong, the emitted decision is ask.
			var b bytes.Buffer
			require.NoError(t, hook.Write(&b, hook.Ask))
			require.Equal(t, goldenAsk, b.String())
		})
	}
}

func TestReadEmptyObject(t *testing.T) {
	// "{}" decodes, but has no command, so nothing can match and it is
	// allowed. The malformed cases above are the ones that must ask.
	req, err := hook.Read(strings.NewReader("{}"))
	require.NoError(t, err)
	require.Equal(t, hook.Input{}, req.Input())
}

func TestReadRealPayload(t *testing.T) {
	req, err := hook.Read(strings.NewReader(`{
      "session_id": "5d6c09b1",
      "transcript_path": "/Users/p/.claude/projects/x/y.jsonl",
      "cwd": "/Users/p/Workspace/example-repo",
      "permission_mode": "default",
      "hook_event_name": "PermissionRequest",
      "tool_name": "Bash",
      "tool_input": {"command": "rm -rf node_modules", "description": "Clean"},
      "permission_suggestions": [{"type": "addRules", "behavior": "allow"}]
    }`))
	require.NoError(t, err)
	require.Equal(t, "5d6c09b1", req.SessionID)
	require.Equal(t, "/Users/p/Workspace/example-repo", req.Cwd)
	require.Equal(t, "Bash", req.ToolName)
	require.Equal(t, "rm -rf node_modules", req.Input().Command)
}

// TestInputOddShapes covers MCP tools whose tool_input is not an object with
// our two keys. They must not fail the decode; tool_regex still applies.
func TestInputOddShapes(t *testing.T) {
	for _, body := range []string{`[1,2]`, `"a string"`, `null`, `{"file_path": 7}`} {
		req, err := hook.Read(strings.NewReader(`{"tool_name":"mcp__x__y","tool_input":` + body + `}`))
		require.NoError(t, err, body)
		require.Equal(t, hook.Input{}, req.Input(), body)
	}
}

func TestDecide(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(home, "Workspace", "example-repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/topic-1\n"), 0o644))

	h := hook.Handler{Rules: rules.Defaults()}

	tests := []struct {
		name     string
		tool     string
		input    string
		decision hook.Decision
		rule     string
		wantErr  bool
	}{
		{
			name: "routine grep", tool: "Bash",
			input: `{"command":"grep -rn foo ./services"}`, decision: hook.Allow,
		},
		{
			name: "the real incident", tool: "Bash",
			input: `{"command":"cd ` + home + `/.claude/projects/x/memory && rm -f MEMORY.md"}`,
			rule:  "rm-outside-repo",
		},
		{
			name: "unparseable command asks", tool: "Bash",
			input: `{"command":"rm -f 'unterminated"}`, wantErr: true,
		},
		{
			name: "no tool input allows", tool: "Read", input: `{}`, decision: hook.Allow,
		},
		{
			name: "mcp delete", tool: "mcp__linear__delete_comment",
			input: `{"id":"1"}`, rule: "mcp-destructive",
		},
		{
			name: "write to settings", tool: "Write",
			input: `{"file_path":"` + home + `/.claude/settings.json"}`, rule: "config-paths",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := hook.Read(strings.NewReader(`{"cwd":"` + repo + `","tool_name":"` + tt.tool + `","tool_input":` + tt.input + `}`))
			require.NoError(t, err)

			got, rule, err := h.Decide(req)
			if tt.wantErr {
				require.Error(t, err)
			}
			if !tt.wantErr {
				require.NoError(t, err)
			}

			require.Equal(t, tt.decision, got)
			require.Equal(t, tt.rule, rule)
		})
	}
}
