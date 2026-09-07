package settings_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pmdcosta/claude-bouncer/internal/settings"
	"github.com/stretchr/testify/require"
)

const binary = "/Users/pmdcosta/go/bin/bouncer"

// fixture copies the real-shaped settings file into a temp directory.
func fixture(t *testing.T, name string) (path string, original []byte) {
	t.Helper()

	original, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	path = filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, os.WriteFile(path, original, 0o600))

	return path, original
}

// enable registers the hook and writes the file.
func enable(t *testing.T, path string) bool {
	t.Helper()

	f, err := settings.Read(path)
	require.NoError(t, err)

	changed, err := f.Register(settings.HookCommand(binary))
	require.NoError(t, err)

	if changed {
		require.NoError(t, f.Write())
	}

	return changed
}

// disable removes the hook and writes the file.
func disable(t *testing.T, path string) bool {
	t.Helper()

	f, err := settings.Read(path)
	require.NoError(t, err)

	changed, err := f.Unregister()
	require.NoError(t, err)

	if changed {
		require.NoError(t, f.Write())
	}

	return changed
}

// TestRoundTripIsByteIdentical is the one assertion that catches key loss,
// reordering and leftover empty keys together.
func TestRoundTripIsByteIdentical(t *testing.T) {
	path, original := fixture(t, "settings.json")

	require.True(t, enable(t, path))
	require.True(t, disable(t, path))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(original), string(got))
}

func TestRegisterPreservesEverythingElse(t *testing.T) {
	path, original := fixture(t, "settings.json")

	require.True(t, enable(t, path))

	got, err := os.ReadFile(path)
	require.NoError(t, err)

	var before, after map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(original, &before))
	require.NoError(t, json.Unmarshal(got, &after))

	// every key survives, and every one except hooks is untouched.
	require.Len(t, after, len(before))
	for key, raw := range before {
		require.Contains(t, after, key)

		if key == "hooks" {
			continue
		}

		require.JSONEq(t, string(raw), string(after[key]), key)
	}

	// the rtk PreToolUse hook is still there, alongside the new event.
	var hooks map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(after["hooks"], &hooks))
	require.Contains(t, hooks, "PreToolUse")
	require.Contains(t, hooks, "PermissionRequest")
	require.Contains(t, string(hooks["PreToolUse"]), "rtk hook")

	// and the registered command is the absolute path with a short timeout.
	require.Contains(t, string(hooks["PermissionRequest"]), binary+" decide")
	require.Contains(t, string(hooks["PermissionRequest"]), `"timeout": 10`)
}

func TestRegisterIsIdempotent(t *testing.T) {
	path, _ := fixture(t, "settings.json")

	require.True(t, enable(t, path))
	require.False(t, enable(t, path), "a second enable must change nothing")
	require.False(t, enable(t, path))

	f, err := settings.Read(path)
	require.NoError(t, err)

	present, err := f.Registered()
	require.NoError(t, err)
	require.True(t, present)

	got, err := os.ReadFile(path)
	require.NoError(t, err)

	var doc struct {
		Hooks struct {
			PermissionRequest []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PermissionRequest"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(got, &doc))
	require.Len(t, doc.Hooks.PermissionRequest, 1, "exactly one hook entry")
	require.Len(t, doc.Hooks.PermissionRequest[0].Hooks, 1)
}

func TestUnregisterOnAnUnregisteredFile(t *testing.T) {
	path, original := fixture(t, "settings.json")

	require.False(t, disable(t, path))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(original), string(got))
}

// TestUnregisterDropsTheEmptyKeys covers a file that had no hooks at all: the
// hooks key must not be left behind empty.
func TestUnregisterDropsTheEmptyKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, os.WriteFile(path, []byte("{\n  \"theme\": \"dark\"\n}\n"), 0o600))

	require.True(t, enable(t, path))
	require.True(t, disable(t, path))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "{\n  \"theme\": \"dark\"\n}\n", string(got))
}

// TestUnregisterKeepsOtherHandlers proves disable never removes someone
// else's handler for the same event.
func TestUnregisterKeepsOtherHandlers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	body := `{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "Bash",
        "if": "Bash(rm *)",
        "hooks": [
          {
            "type": "command",
            "command": "/opt/other/tool check"
          }
        ]
      }
    ]
  }
}
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	require.True(t, enable(t, path))
	require.True(t, disable(t, path))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, body, string(got), "the other handler and its if condition survive intact")
}

func TestRemoteApproverDetection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"hooks":{"PermissionRequest":[{"hooks":[{"type":"command","command":"/usr/local/bin/claude-remote-approver hook"}]}]}}`), 0o600))

	f, err := settings.Read(path)
	require.NoError(t, err)

	present, err := f.RemoteApproverRegistered()
	require.NoError(t, err)
	require.True(t, present)

	clean, _ := fixture(t, "settings.json")
	g, err := settings.Read(clean)
	require.NoError(t, err)

	present, err = g.RemoteApproverRegistered()
	require.NoError(t, err)
	require.False(t, present)
}

func TestReadMissingFile(t *testing.T) {
	f, err := settings.Read(filepath.Join(t.TempDir(), "nope", "settings.json"))
	require.NoError(t, err)

	present, err := f.Registered()
	require.NoError(t, err)
	require.False(t, present)
}

func TestBackupIsTaken(t *testing.T) {
	path, original := fixture(t, "settings.json")

	require.True(t, enable(t, path))

	entries, err := filepath.Glob(path + ".bouncer-*.bak")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// the backup is the file as it was, not the file as it became.
	backup, err := os.ReadFile(entries[0])
	require.NoError(t, err)
	require.Equal(t, string(original), string(backup))
}

func TestWriteRefusesToLoseUnreadableSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	_, err := settings.Read(path)
	require.Error(t, err)
}
