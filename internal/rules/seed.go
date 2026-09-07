package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// seeded is the starter rules file.
//
// It is comments, not a copy of the defaults. Writing the full list out would
// work, since merging by name is upgrade-safe either way, but it duplicates
// every rule into a file kept in sync by eye. Starting empty keeps the binary
// the single source of truth and lets the file record only deltas.
const seeded = `# bouncer rules — github.com/pmdcosta/claude-bouncer
# Defaults are compiled into the binary. This file only *changes* them.
# See the live list with:  bouncer rules
#
# rules:
#   - name: my-rule          # add one
#     type: cmd
#     args: [terraform]
#
#   - name: git-config       # switch a default off
#     enabled: false

rules: []
`

// Seed writes the starter rules file, reporting whether it created one.
//
// An existing file is left exactly as it is.
func Seed(dir string) (bool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("failed to create config directory: %w", err)
	}

	path := filepath.Join(dir, FileName)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("failed to create rules file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(seeded); err != nil {
		return false, fmt.Errorf("failed to write rules file: %w", err)
	}

	return true, nil
}
