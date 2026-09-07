package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pmdcosta/claude-bouncer/internal/rules"
	"github.com/pmdcosta/claude-bouncer/internal/settings"
	"github.com/stretchr/testify/require"
)

// realShape is the settings file this machine actually has: an rtk PreToolUse
// hook plus keys bouncer knows nothing about.
const realShape = `{
  "statusLine": {
    "type": "command",
    "command": "rtk statusline"
  },
  "env": {
    "DOCKER_HOST": "unix:///Users/pmdcosta/.colima/docker.sock"
  },
  "enabledPlugins": [
    "anthropic-skills"
  ],
  "sandbox": {
    "enabled": true
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "rtk hook"
          }
        ]
      }
    ]
  }
}
`

// settingsFile writes the fixture into the sandbox home and returns its path.
func settingsFile(t *testing.T, s sandbox, body string) string {
	t.Helper()

	path := filepath.Join(s.home, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	return path
}

func TestEnableDisableRoundTripIsByteIdentical(t *testing.T) {
	s := newSandbox(t, "ins-1")
	path := settingsFile(t, s, realShape)

	var out bytes.Buffer
	require.NoError(t, runEnable(&out))
	require.Contains(t, out.String(), "registered")
	require.Contains(t, out.String(), "29 rules live, 0 disabled")

	// the hook really is in there.
	f, err := settings.Read(path)
	require.NoError(t, err)
	present, err := f.Registered()
	require.NoError(t, err)
	require.True(t, present)

	out.Reset()
	require.NoError(t, runDisable(&out, false))
	require.Contains(t, out.String(), "removed")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, realShape, string(got), "key loss, reordering and empty leftovers all show up here")
}

func TestEnableIsIdempotent(t *testing.T) {
	s := newSandbox(t, "ins-1")
	path := settingsFile(t, s, realShape)

	var out bytes.Buffer
	require.NoError(t, runEnable(&out))

	out.Reset()
	require.NoError(t, runEnable(&out))
	require.Contains(t, out.String(), "already registered")

	out.Reset()
	require.NoError(t, runEnable(&out))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var doc struct {
		Hooks struct {
			PermissionRequest []struct {
				Hooks []struct {
					Command string `json:"command"`
					Timeout int    `json:"timeout"`
				} `json:"hooks"`
			} `json:"PermissionRequest"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	require.Len(t, doc.Hooks.PermissionRequest, 1, "exactly one hook entry after three enables")
	require.Len(t, doc.Hooks.PermissionRequest[0].Hooks, 1)
	require.Equal(t, 10, doc.Hooks.PermissionRequest[0].Hooks[0].Timeout)

	// the command is an absolute path plus the subcommand: hooks do not
	// inherit an interactive shell's PATH, so a bare "bouncer" is not found.
	command := doc.Hooks.PermissionRequest[0].Hooks[0].Command
	binary, subcommand, split := strings.Cut(command, " ")
	require.True(t, split, command)
	require.True(t, filepath.IsAbs(binary), command)
	require.Equal(t, "decide", subcommand)
}

func TestEnableSeedsTheRulesFile(t *testing.T) {
	s := newSandbox(t, "ins-1")
	settingsFile(t, s, realShape)

	var out bytes.Buffer
	require.NoError(t, runEnable(&out))
	require.Contains(t, out.String(), "created")

	body, err := os.ReadFile(filepath.Join(s.config, rules.FileName))
	require.NoError(t, err)
	require.Contains(t, string(body), "rules: []")
	require.Contains(t, string(body), "# Defaults are compiled into the binary")

	info, err := os.Stat(filepath.Join(s.config, rules.FileName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestEnableRefusesToRaceWithRemoteApprover covers the first guard: two
// handlers on this event race and the docs do not define who wins.
func TestEnableRefusesToRaceWithRemoteApprover(t *testing.T) {
	s := newSandbox(t, "ins-1")
	path := settingsFile(t, s, `{
  "hooks": {
    "PermissionRequest": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/claude-remote-approver hook"
          }
        ]
      }
    ]
  }
}
`)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	var out bytes.Buffer
	err = runEnable(&out)
	require.ErrorIs(t, err, settings.ErrRemoteApprover)
	require.Contains(t, err.Error(), "claude-remote-approver uninstall")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "a refused enable changes nothing")
}

// TestEnableRefusesABrokenRulesFile covers the second guard.
func TestEnableRefusesABrokenRulesFile(t *testing.T) {
	s := newSandbox(t, "ins-1")
	path := settingsFile(t, s, realShape)

	require.NoError(t, os.MkdirAll(s.config, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(s.config, rules.FileName), []byte("rules:\n  - name: x\n    type: nope\n"), 0o600))

	var out bytes.Buffer
	err := runEnable(&out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown type")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, realShape, string(after), "never enable into a broken config")
}

// TestDisableLeavesTuningAlone: disable is a switch, not an uninstall.
func TestDisableLeavesTuningAlone(t *testing.T) {
	s := newSandbox(t, "ins-1")
	settingsFile(t, s, realShape)

	var out bytes.Buffer
	require.NoError(t, runEnable(&out))

	tuned := "rules:\n  - name: git-config\n    enabled: false\n"
	require.NoError(t, os.WriteFile(filepath.Join(s.config, rules.FileName), []byte(tuned), 0o600))

	out.Reset()
	require.NoError(t, runDisable(&out, false))

	body, err := os.ReadFile(filepath.Join(s.config, rules.FileName))
	require.NoError(t, err)
	require.Equal(t, tuned, string(body))

	// and enabling again picks the tuning back up.
	out.Reset()
	require.NoError(t, runEnable(&out))
	require.Contains(t, out.String(), "28 rules live, 1 disabled")
}

func TestDisablePurgeDeletesTheRulesFile(t *testing.T) {
	s := newSandbox(t, "ins-1")
	settingsFile(t, s, realShape)

	var out bytes.Buffer
	require.NoError(t, runEnable(&out))

	out.Reset()
	require.NoError(t, runDisable(&out, true))
	require.Contains(t, out.String(), "deleted")

	_, err := os.Stat(filepath.Join(s.config, rules.FileName))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDisableWhenNotRegistered(t *testing.T) {
	s := newSandbox(t, "ins-1")
	path := settingsFile(t, s, realShape)

	var out bytes.Buffer
	require.NoError(t, runDisable(&out, false))
	require.Contains(t, out.String(), "no bouncer hook registered")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, realShape, string(got))
}

func TestRunRules(t *testing.T) {
	s := newSandbox(t, "ins-1")
	require.NoError(t, os.MkdirAll(s.config, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(s.config, rules.FileName), []byte(
		"rules:\n  - name: git-config\n    enabled: false\n  - name: mine\n    type: cmd\n    args: [terraform]\n"), 0o600))

	var out bytes.Buffer
	require.NoError(t, runRules(&out, false, false))

	// collapsed so the assertions do not depend on column widths.
	listed := collapse(out.String())
	require.Contains(t, listed, "[default] rm-outside-repo args_outside_repo rm")
	require.Contains(t, listed, "[disabled] git-config sub git config")
	require.Contains(t, listed, "[file] mine cmd terraform")

	out.Reset()
	require.NoError(t, runRules(&out, true, false))
	require.Contains(t, out.String(), "ok: 30 rules, 29 live")
}

func TestRunRulesReportsAFallback(t *testing.T) {
	s := newSandbox(t, "ins-1")
	require.NoError(t, os.MkdirAll(s.config, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(s.config, rules.FileName), []byte("rules:\n  - name: x\n    type: nope\n"), 0o600))

	var out bytes.Buffer
	err := runRules(&out, false, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "the list above is the compiled fallback")
	// the printed list is still the whole default list, not a partial one.
	require.Equal(t, len(rules.Defaults()), countLines(out.String()))

	out.Reset()
	require.Error(t, runRules(&out, true, false))
}

func countLines(s string) int {
	n := 0
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}

	return n
}

func TestRunLog(t *testing.T) {
	s := newSandbox(t, "ins-2593-foo")

	run(t, request(t, s.repo, "Bash", map[string]string{"command": "grep -rn foo ."}))
	run(t, request(t, s.repo, "Bash", map[string]string{"command": "git stash pop"}))

	var out bytes.Buffer
	require.NoError(t, runLog(&out, logOptions{since: "7d"}))
	require.Contains(t, out.String(), "grep -rn foo .")
	require.Contains(t, out.String(), "git-stash")

	out.Reset()
	require.NoError(t, runLog(&out, logOptions{since: "7d", allowed: true}))
	require.Contains(t, out.String(), "grep -rn foo .")
	require.NotContains(t, out.String(), "git stash pop")

	out.Reset()
	require.NoError(t, runLog(&out, logOptions{since: "7d", asked: true}))
	require.NotContains(t, out.String(), "grep -rn foo .")
	require.Contains(t, out.String(), "git stash pop")

	out.Reset()
	require.Error(t, runLog(&out, logOptions{since: "7d", allowed: true, asked: true}))
	require.Error(t, runLog(&out, logOptions{since: "next tuesday"}))
}

func TestParseSince(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{in: "7d", want: 7 * 24 * time.Hour},
		{in: "1d", want: 24 * time.Hour},
		{in: "24h", want: 24 * time.Hour},
		{in: "30m", want: 30 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := parseSince(tt.in)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	_, err := parseSince("tomorrow")
	require.Error(t, err)
}
