package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// explain runs the subcommand's logic and returns its plain output.
func explain(t *testing.T, opts explainOptions) string {
	t.Helper()

	var out bytes.Buffer
	require.NoError(t, runExplain(&out, opts))

	return out.String()
}

func TestExplainVerdicts(t *testing.T) {
	s := newSandbox(t, "feature-branch")

	tests := []struct {
		name    string
		command string
		wants   []string
	}{
		{
			name:    "routine work says why nothing happens",
			command: "grep -rn foo ./services",
			wants:   []string{"allow", "no rule matches"},
		},
		{
			name:    "a matching rule is named with its type and args",
			command: "git stash pop",
			wants:   []string{"ask", "git-stash", "sub [git stash]", "from default"},
		},
		{
			name:    "the walk shows a cd moving out of the repo",
			command: "cd " + s.home + "/elsewhere && rm -f x",
			wants: []string{
				"ask", "rm-outside-repo", "args_outside_repo",
				"moved by an earlier cd",
				"no git repo above it",
			},
		},
		{
			name:    "an unknown cwd is called out",
			command: "cd $D && rm x",
			wants:   []string{"ask", "cwd unknown"},
		},
		{
			name:    "quoted text is not a command",
			command: `git commit -m "fix; rm cruft"`,
			wants:   []string{"allow"},
		},
		{
			name:    "an unparseable command explains the escalation",
			command: "rm -f 'unterminated",
			wants:   []string{"ask", "cannot parse"},
		},
		{
			name:    "an unexpanded argument is marked",
			command: "rm -f $TARGET",
			wants:   []string{"ask", "<unexpanded>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := explain(t, explainOptions{command: tt.command, tool: "Bash", cwd: s.repo})

			for _, want := range tt.wants {
				require.Contains(t, got, want)
			}
		})
	}
}

// TestExplainBranchDecidesTheOutcome is the pair the design rests on: one
// command, opposite answers, and explain has to show which fact decided it.
func TestExplainBranchDecidesTheOutcome(t *testing.T) {
	t.Run("feature branch", func(t *testing.T) {
		s := newSandbox(t, "feature-branch")

		got := explain(t, explainOptions{command: "git rebase origin/main", tool: "Bash", cwd: s.repo})
		require.Contains(t, got, "allow")
		require.Contains(t, got, "branch feature-branch")
	})

	t.Run("master", func(t *testing.T) {
		s := newSandbox(t, "master")

		got := explain(t, explainOptions{command: "git rebase origin/main", tool: "Bash", cwd: s.repo})
		require.Contains(t, got, "ask")
		require.Contains(t, got, "git-rebase-on-protected-branch")
		require.Contains(t, got, "branch master")
	})
}

func TestExplainFileTools(t *testing.T) {
	s := newSandbox(t, "topic-1")

	got := explain(t, explainOptions{file: s.home + "/.claude/settings.json", tool: "Write", cwd: s.repo})
	require.Contains(t, got, "ask")
	require.Contains(t, got, "config-paths")
	require.Contains(t, got, "tool Write")

	got = explain(t, explainOptions{file: s.repo + "/services/x.go", tool: "Write", cwd: s.repo})
	require.Contains(t, got, "allow")
}

func TestExplainToolRegex(t *testing.T) {
	s := newSandbox(t, "topic-1")

	got := explain(t, explainOptions{tool: "mcp__linear__delete_comment", file: "-", cwd: s.repo})
	require.Contains(t, got, "mcp-destructive")

	got = explain(t, explainOptions{tool: "mcp__linear__save_document", file: "-", cwd: s.repo})
	require.Contains(t, got, "allow")
}

// TestExplainReportsARulesFileProblem matters because a broken file silently
// falls back to the compiled defaults, and this is where that becomes visible.
func TestExplainReportsARulesFileProblem(t *testing.T) {
	s := newSandbox(t, "topic-1")
	require.NoError(t, os.MkdirAll(s.config, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(s.config, "rules.yaml"), []byte("rules:\n  - name: x\n    type: nope\n"), 0o600))

	got := explain(t, explainOptions{command: "git stash pop", tool: "Bash", cwd: s.repo})
	require.Contains(t, got, "git-stash", "the compiled default still decides")
	require.Contains(t, got, "rules file problem")
	require.Contains(t, got, "unknown type")
}

// TestExplainNeverLogs is the whole reason this subcommand exists: diagnosing
// a prompt must not add records to the log being diagnosed.
func TestExplainNeverLogs(t *testing.T) {
	s := newSandbox(t, "topic-1")

	explain(t, explainOptions{command: "git stash pop", tool: "Bash", cwd: s.repo})
	explain(t, explainOptions{command: "curl https://x", tool: "Bash", cwd: s.repo})

	require.Empty(t, s.logLines(t))

	_, err := os.Stat(filepath.Join(s.home, ".claude", "logs"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestExplainNeedsSomethingToExplain(t *testing.T) {
	newSandbox(t, "topic-1")

	var out bytes.Buffer
	err := runExplain(&out, explainOptions{tool: "Bash"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "give a command")
}

func TestExplainInputFromStdin(t *testing.T) {
	got, err := explainInput("-", strings.NewReader("git stash pop"))
	require.NoError(t, err)
	require.Equal(t, "git stash pop", got)

	got, err = explainInput("git push", strings.NewReader("ignored"))
	require.NoError(t, err)
	require.Equal(t, "git push", got)
}

func TestExplainColorIsOptOut(t *testing.T) {
	s := newSandbox(t, "topic-1")

	plain := explain(t, explainOptions{command: "git stash pop", tool: "Bash", cwd: s.repo})
	painted := explain(t, explainOptions{command: "git stash pop", tool: "Bash", cwd: s.repo, color: true})

	require.NotContains(t, plain, "\x1b[")
	require.Equal(t, plain, stripANSI(painted))
}
