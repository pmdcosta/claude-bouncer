package rules_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pmdcosta/claude-bouncer/internal/rules"
	"github.com/stretchr/testify/require"
)

// writeRules puts a rules.yaml in a fresh config directory.
func writeRules(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, rules.FileName), []byte(body), 0o600))

	return dir
}

// byName finds one rule in an effective list.
func byName(t *testing.T, rs []rules.Rule, name string) rules.Rule {
	t.Helper()

	for _, r := range rs {
		if r.Name == name {
			return r
		}
	}

	t.Fatalf("rule %q not in the effective list", name)

	return rules.Rule{}
}

func TestLoadNoFile(t *testing.T) {
	got, err := rules.Load(t.TempDir())
	require.NoError(t, err)
	require.Equal(t, rules.Defaults(), got)
}

func TestLoadEmptyRuleList(t *testing.T) {
	got, err := rules.Load(writeRules(t, "rules: []\n"))
	require.NoError(t, err)
	require.Equal(t, rules.Defaults(), got)
}

func TestLoadDisablesADefault(t *testing.T) {
	got, err := rules.Load(writeRules(t, `
rules:
  - name: git-config
    enabled: false
`))
	require.NoError(t, err)
	require.Len(t, got, len(rules.Defaults()))

	disabled := byName(t, got, "git-config")
	require.False(t, disabled.On())
	require.Equal(t, rules.SourceFile, disabled.Source)
	// an entry that only flips enabled keeps the default's body.
	require.Equal(t, rules.TypeSub, disabled.Type)
	require.Equal(t, []string{"git", "config"}, disabled.Args)

	// and the rule stops firing.
	require.Empty(t, rules.Match(got, rules.Request{ToolName: "Bash", Commands: nil}))
}

func TestLoadReplacesADefault(t *testing.T) {
	got, err := rules.Load(writeRules(t, `
rules:
  - name: network-fetch
    type: cmd
    args: [curl]
`))
	require.NoError(t, err)
	require.Len(t, got, len(rules.Defaults()))

	replaced := byName(t, got, "network-fetch")
	require.Equal(t, []string{"curl"}, replaced.Args)
	require.Equal(t, rules.SourceFile, replaced.Source)
}

func TestLoadAddsARule(t *testing.T) {
	got, err := rules.Load(writeRules(t, `
rules:
  - name: my-rule
    type: cmd
    args: [terraform]
`))
	require.NoError(t, err)
	require.Len(t, got, len(rules.Defaults())+1)

	added := byName(t, got, "my-rule")
	require.Equal(t, rules.SourceFile, added.Source)
	// defaults keep their order, so the new rule is last.
	require.Equal(t, "my-rule", got[len(got)-1].Name)
}

// TestLoadFallsBackWhole is the failure this design exists to prevent: a typo
// must never silently shorten the live list.
func TestLoadFallsBackWhole(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed yaml", body: "rules: [ - name:\n\tbroken"},
		{name: "not a mapping", body: "just a string\n"},
		{name: "unknown type", body: "rules:\n  - name: x\n    type: nonsense\n    args: [y]\n"},
		{name: "uncompilable regex", body: "rules:\n  - name: x\n    type: tool_regex\n    args: [\"mcp__(\"]\n"},
		{name: "too few args", body: "rules:\n  - name: x\n    type: flag\n    args: [git]\n"},
		{name: "empty arg", body: "rules:\n  - name: x\n    type: cmd\n    args: [\"\"]\n"},
		{name: "missing name", body: "rules:\n  - type: cmd\n    args: [x]\n"},
		{name: "duplicate name", body: "rules:\n  - name: x\n    type: cmd\n    args: [a]\n  - name: x\n    type: cmd\n    args: [b]\n"},
		{name: "disabling an unknown rule", body: "rules:\n  - name: not-a-default\n    enabled: false\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := rules.Load(writeRules(t, tt.body))
			require.Error(t, err)
			require.Equal(t, rules.Defaults(), got, "the fallback must be the full default list")
		})
	}
}

func TestLoadEmptyFile(t *testing.T) {
	got, err := rules.Load(writeRules(t, ""))
	require.NoError(t, err)
	require.Equal(t, rules.Defaults(), got)
}

func TestConfigDirRespectsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	dir, err := rules.ConfigDir()
	require.NoError(t, err)
	require.Equal(t, "/xdg/bouncer", dir)
}

func TestConfigDirDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/h")

	dir, err := rules.ConfigDir()
	require.NoError(t, err)
	require.Equal(t, "/h/.config/bouncer", dir)
}

func TestSeed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bouncer")

	created, err := rules.Seed(dir)
	require.NoError(t, err)
	require.True(t, created)

	// the seeded file parses and changes nothing.
	got, err := rules.Load(dir)
	require.NoError(t, err)
	require.Equal(t, rules.Defaults(), got)

	// the mode is tight and a second seed leaves the file alone.
	info, err := os.Stat(filepath.Join(dir, rules.FileName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	require.NoError(t, os.WriteFile(filepath.Join(dir, rules.FileName), []byte("rules: []\n# mine\n"), 0o600))

	created, err = rules.Seed(dir)
	require.NoError(t, err)
	require.False(t, created)

	body, err := os.ReadFile(filepath.Join(dir, rules.FileName))
	require.NoError(t, err)
	require.Equal(t, "rules: []\n# mine\n", string(body))
}
