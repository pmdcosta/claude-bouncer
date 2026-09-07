package rules_test

import (
	"testing"

	"github.com/pmdcosta/claude-bouncer/internal/rules"
	"github.com/stretchr/testify/require"
)

// TestConfigGlobs exercises the "**" matcher through the real default
// patterns, which is the only place it is used.
func TestConfigGlobs(t *testing.T) {
	tests := []struct {
		name string
		path string
		rule string
	}{
		{name: "settings at home", path: "/Users/p/.claude/settings.json", rule: "config-paths"},
		{name: "settings in a project", path: "/Users/p/Workspace/x/.claude/settings.json", rule: "config-paths"},
		{name: "settings at the root", path: "/settings.json", rule: "config-paths"},
		{name: "hooks directory itself", path: "/Users/p/.claude/hooks", rule: "config-paths"},
		{name: "file under hooks", path: "/Users/p/.claude/hooks/a/b.sh", rule: "config-paths"},
		{name: "bare dotenv", path: "/x/.env", rule: "config-paths"},
		{name: "suffixed dotenv", path: "/x/.env.production", rule: "config-paths"},
		{name: "keyring suffix", path: "/x/y/dev-strongbox-keyring", rule: "config-paths"},

		{name: "not a settings file", path: "/Users/p/.claude/settings.jsonc"},
		{name: "dotenv is a prefix not a substring", path: "/x/my.env"},
		{name: "hooks as a filename", path: "/Users/p/.claude/hooksfile"},
		{name: "ordinary go file", path: "/Users/p/Workspace/x/main.go"},
		{name: "a plan", path: "/Users/p/.claude/plans/x.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rules.Match(rules.Defaults(), rules.Request{ToolName: "Write", FilePath: tt.path})
			require.Equal(t, tt.rule, got)
		})
	}
}

// TestHomeGlobs covers the patterns written with a leading tilde.
func TestHomeGlobs(t *testing.T) {
	t.Setenv("HOME", "/Users/p")

	asks := []string{
		"/Users/p/.ssh/id_ed25519",
		"/Users/p/.ssh",
		"/Users/p/.aws/config",
		"/Users/p/.kube/config",
		"/Users/p/.config/bouncer/rules.yaml",
	}
	for _, path := range asks {
		require.Equal(t, "config-paths", rules.Match(rules.Defaults(), rules.Request{ToolName: "Write", FilePath: path}), path)
	}

	// another user's home is not this user's home.
	require.Empty(t, rules.Match(rules.Defaults(), rules.Request{ToolName: "Write", FilePath: "/Users/other/.ssh/id_ed25519"}))
}
