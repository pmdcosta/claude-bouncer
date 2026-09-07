// Package rules decides which permission requests deserve a prompt.
//
// The default list is compiled into the binary and is the safety floor: any
// problem loading or validating the user's file falls back to it whole, never
// to a partial or empty list. A silently shortened list is the failure this
// design exists to prevent.
package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the name of the optional rules file inside the config directory.
const FileName = "rules.yaml"

// ErrNoConfigDir is returned when the config directory cannot be located.
var ErrNoConfigDir = errors.New("cannot locate a config directory")

// Type is one of the fixed rule vocabulary. The vocabulary is closed so the
// user's file can be validated rather than failing silently at runtime.
type Type string

// The supported rule types.
const (
	// TypeCmd matches a command name, for example "sudo".
	TypeCmd Type = "cmd"
	// TypeSub matches a command and any of its subcommands, for example
	// "git stash".
	TypeSub Type = "sub"
	// TypeFlag matches a command, a subcommand and any of its flags, for
	// example "git reset --hard". Flags compare exactly, so "--force" does not
	// match "--force-with-lease".
	TypeFlag Type = "flag"
	// TypeArgIn matches a command, a subcommand and any of its operands, for
	// example "git push" naming "main".
	TypeArgIn Type = "arg_in"
	// TypePathGlob matches a path against globs in which "**" spans any number
	// of directories.
	TypePathGlob Type = "path_glob"
	// TypeArgsOutsideRepo matches a command whose operands resolve outside the
	// git repository containing the effective working directory.
	TypeArgsOutsideRepo Type = "args_outside_repo"
	// TypeOnProtectedBranch matches a command run while HEAD is on main or
	// master.
	TypeOnProtectedBranch Type = "on_protected_branch"
	// TypeToolRegex matches a regular expression against the tool name.
	TypeToolRegex Type = "tool_regex"
)

// Source records where an effective rule came from, for bouncer rules.
type Source string

// The rule sources.
const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
)

// Rule is one condition that requires a permission prompt.
//
// Two conditions on the same command are simply two rules, which is how
// "git push" gets three independent conditions without the file needing
// boolean operators.
type Rule struct {
	// Name identifies the rule and is the merge key against the defaults.
	Name string `yaml:"name"`
	// Type selects the matching logic.
	Type Type `yaml:"type"`
	// Args parameterises the type.
	Args []string `yaml:"args"`
	// Enabled switches a rule off when false. A nil pointer means unset, which
	// is how "enabled: false" is told apart from an omitted field.
	Enabled *bool `yaml:"enabled"`

	// Source is filled in by Load and is not read from the file.
	Source Source `yaml:"-"`
}

// On reports whether the rule is live.
func (r Rule) On() bool {
	return r.Enabled == nil || *r.Enabled
}

// file is the top-level shape of rules.yaml.
type file struct {
	Rules []Rule `yaml:"rules"`
}

// ConfigDir returns the directory holding rules.yaml, respecting
// XDG_CONFIG_HOME.
func ConfigDir() (string, error) {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "bouncer"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to locate home directory: %w", ErrNoConfigDir)
	}

	return filepath.Join(home, ".config", "bouncer"), nil
}

// Load returns the effective rule list for the given config directory.
//
// It always returns a usable list. On any read, parse or validation problem it
// returns the full compiled defaults together with the error, so the caller
// can report the problem without changing any decision.
func Load(dir string) ([]Rule, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		// no file is the normal case; the compiled list stands alone.
		return Defaults(), nil
	}

	if err != nil {
		return Defaults(), fmt.Errorf("failed to read rules file: %w", err)
	}

	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return Defaults(), fmt.Errorf("failed to parse rules file: %w", err)
	}

	if err := validateFile(f.Rules); err != nil {
		return Defaults(), err
	}

	merged := merge(Defaults(), f.Rules)
	if err := Validate(merged); err != nil {
		return Defaults(), err
	}

	return merged, nil
}

// merge folds the file's entries into the defaults by name. A matching name
// replaces the default, a new name is appended, and an entry carrying only
// "enabled" keeps the default's type and arguments.
func merge(defaults, overrides []Rule) []Rule {
	merged := make([]Rule, len(defaults))
	copy(merged, defaults)

	at := make(map[string]int, len(merged))
	for i, r := range merged {
		at[r.Name] = i
	}

	for _, o := range overrides {
		o.Source = SourceFile

		i, found := at[o.Name]
		if !found {
			at[o.Name] = len(merged)
			merged = append(merged, o)
			continue
		}

		if o.Type == "" && len(o.Args) == 0 {
			// an entry that only flips "enabled" keeps the default's body.
			merged[i].Enabled = o.Enabled
			merged[i].Source = SourceFile
			continue
		}

		merged[i] = o
	}

	return merged
}

// validateFile checks the entries as written, before they are merged, so a
// duplicate or nameless entry is reported against the file the user wrote.
func validateFile(rs []Rule) error {
	var problems []error

	seen := make(map[string]bool, len(rs))
	for i, r := range rs {
		if r.Name == "" {
			problems = append(problems, fmt.Errorf("rule %d: missing name", i+1))
			continue
		}

		if seen[r.Name] {
			problems = append(problems, fmt.Errorf("rule %q: duplicate name", r.Name))
		}

		seen[r.Name] = true
	}

	if len(problems) == 0 {
		return nil
	}

	return fmt.Errorf("failed to validate rules file: %w", errors.Join(problems...))
}

// Validate reports every problem in a rule list at once, so one run of
// bouncer rules --validate shows all of them.
func Validate(rs []Rule) error {
	var problems []error

	for _, r := range rs {
		if err := validateRule(r); err != nil {
			problems = append(problems, fmt.Errorf("rule %q: %w", r.Name, err))
		}
	}

	if len(problems) == 0 {
		return nil
	}

	return fmt.Errorf("failed to validate rules: %w", errors.Join(problems...))
}

// argCount is the minimum number of arguments each type needs.
var argCount = map[Type]int{
	TypeCmd:               1,
	TypeSub:               2,
	TypeFlag:              3,
	TypeArgIn:             3,
	TypePathGlob:          1,
	TypeArgsOutsideRepo:   1,
	TypeOnProtectedBranch: 1,
	TypeToolRegex:         1,
}

func validateRule(r Rule) error {
	minimum, known := argCount[r.Type]
	if !known {
		return fmt.Errorf("unknown type %q", r.Type)
	}

	if len(r.Args) < minimum {
		return fmt.Errorf("type %q needs at least %d args, got %d", r.Type, minimum, len(r.Args))
	}

	if r.Type == TypeArgsOutsideRepo && len(r.Args) != 1 {
		return fmt.Errorf("type %q needs exactly 1 arg, got %d", r.Type, len(r.Args))
	}

	if r.Type == TypeOnProtectedBranch && len(r.Args) > 2 {
		return fmt.Errorf("type %q takes at most 2 args, got %d", r.Type, len(r.Args))
	}

	for _, a := range r.Args {
		if strings.TrimSpace(a) == "" {
			return errors.New("empty arg")
		}
	}

	if r.Type != TypeToolRegex {
		return nil
	}

	for _, a := range r.Args {
		if _, err := regexp.Compile(a); err != nil {
			return fmt.Errorf("failed to compile regex %q: %w", a, err)
		}
	}

	return nil
}
