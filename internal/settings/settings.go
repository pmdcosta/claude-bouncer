// Package settings makes atomic, key-preserving edits to Claude Code's user
// settings file.
//
// Corrupting that file breaks Claude Code entirely, so every write goes to a
// temporary file and is renamed into place, a timestamped backup is taken
// first, and the document is decoded into a map rather than a struct so keys
// bouncer knows nothing about survive untouched.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Event is the hook event bouncer registers under.
const Event = "PermissionRequest"

// Timeout is the per-hook timeout in seconds. The default is 600, which is far
// too long for a decision that should take milliseconds.
const Timeout = 10

// marker identifies bouncer's own hook entry by its command.
const marker = "bouncer"

// remoteApprover identifies the tool bouncer refuses to share this event with.
const remoteApprover = "claude-remote-approver"

// ErrRemoteApprover is returned when another handler already owns this event.
//
// Two handlers on PermissionRequest race, and the docs do not define how
// competing decisions resolve, so bouncer refuses rather than quietly creating
// the race.
var ErrRemoteApprover = errors.New("claude-remote-approver is registered for PermissionRequest")

// File is a settings document read from disk.
//
// The document keeps every key it was read with, in order, and every value it
// does not need to change as its original bytes, so keys such as statusLine,
// env, enabledPlugins and sandbox come back untouched.
type File struct {
	// Path is where the document was read from and will be written to.
	Path string

	doc object
	// indent is the indentation the file was written with, so a rewrite looks
	// like the original.
	indent string
	// existed records whether the file was on disk.
	existed bool
}

// DefaultPath returns the user-wide settings path.
//
// Only the user-wide file is supported. This is personal tooling, and one
// place to look beats the flexibility of per-project settings.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to locate home directory: %w", err)
	}

	return filepath.Join(home, ".claude", "settings.json"), nil
}

// Read loads a settings file. A missing file reads as an empty document.
//
// Always read immediately before writing: another tool may have edited the
// file, so a cached document must never be written back.
func Read(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{Path: path, indent: "  "}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to read settings: %w", err)
	}

	var doc object
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("failed to parse settings: %w", err)
		}
	}

	return &File{Path: path, doc: doc, indent: detectIndent(data), existed: true}, nil
}

// detectIndent reads the indentation of the document's first nested line.
func detectIndent(data []byte) string {
	for _, line := range strings.Split(string(data), "\n")[1:] {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || trimmed == "}" {
			continue
		}

		if indent := line[:len(line)-len(trimmed)]; indent != "" {
			return indent
		}
	}

	return "  "
}

// Write saves the document, backing up any existing file first.
//
// It writes a temporary file in the same directory and renames it, so a crash
// mid-write leaves the old file intact.
func (f *File) Write() error {
	if f.existed {
		if err := f.backup(); err != nil {
			return fmt.Errorf("failed to back up settings before writing: %w", err)
		}
	}

	var b bytes.Buffer

	enc := json.NewEncoder(&b)
	enc.SetIndent("", f.indent)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(f.doc); err != nil {
		return fmt.Errorf("failed to encode settings: %w", err)
	}

	dir := filepath.Dir(f.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create settings directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".bouncer-settings-*")
	if err != nil {
		return fmt.Errorf("failed to create temp settings file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(b.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write temp settings file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp settings file: %w", err)
	}

	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("failed to set settings permissions: %w", err)
	}

	if err := os.Rename(tmp.Name(), f.Path); err != nil {
		return fmt.Errorf("failed to replace settings file: %w", err)
	}

	return nil
}

// backup copies the current file aside.
//
// The name carries a timestamp and bouncer's own name: settings.json.bak
// already exists on this machine and belongs to something else.
func (f *File) backup() error {
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return fmt.Errorf("failed to read settings for backup: %w", err)
	}

	name := fmt.Sprintf("%s.bouncer-%d.bak", f.Path, time.Now().Unix())
	if err := os.WriteFile(name, data, 0o600); err != nil {
		return fmt.Errorf("failed to write settings backup: %w", err)
	}

	return nil
}
