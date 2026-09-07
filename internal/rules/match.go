package rules

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pmdcosta/claude-bouncer/internal/shellwalk"
)

// Request is one permission request reduced to what the rules need.
type Request struct {
	// ToolName is the tool asking for permission, for example "Bash".
	ToolName string
	// FilePath is the target of a file-writing tool, empty otherwise.
	FilePath string
	// Commands are the simple commands inside a Bash call, in execution
	// order, empty for other tools.
	Commands []shellwalk.Command
}

// NoCommand is the command index reported when a rule matched something other
// than a single command, such as a tool name or a file path.
const NoCommand = -1

// Match returns the name of the first live rule requiring a prompt, or an
// empty string when nothing matches and the request can be auto-approved.
func Match(rs []Rule, req Request) string {
	name, _ := MatchDetail(rs, req)

	return name
}

// MatchDetail also reports which command in the request the rule matched, as
// an index into req.Commands, or NoCommand.
//
// Knowing the command matters when explaining a decision: a command string can
// hold a dozen simple commands, and pointing at the one that matched is the
// difference between an answer and a haystack.
func MatchDetail(rs []Rule, req Request) (string, int) {
	for _, r := range rs {
		if !r.On() {
			continue
		}

		if at, found := matches(r, req); found {
			return r.Name, at
		}
	}

	return "", NoCommand
}

func matches(r Rule, req Request) (int, bool) {
	if r.Type == TypeToolRegex {
		return NoCommand, matchToolRegex(r.Args, req.ToolName)
	}

	if r.Type == TypePathGlob {
		return matchPathGlob(r.Args, req)
	}

	// every remaining type inspects the commands of a Bash call.
	for i, cmd := range req.Commands {
		if matchCommand(r, cmd) {
			return i, true
		}
	}

	return NoCommand, false
}

func matchCommand(r Rule, cmd shellwalk.Command) bool {
	switch r.Type {
	case TypeCmd:
		return contains(r.Args, cmd.Name)

	case TypeSub:
		return cmd.Name == r.Args[0] && contains(r.Args[1:], subcommand(cmd))

	case TypeFlag:
		if cmd.Name != r.Args[0] || subcommand(cmd) != r.Args[1] {
			return false
		}
		// flags compare exactly, so "--force" does not catch
		// "--force-with-lease".
		return anyArg(cmd, r.Args[2:], true)

	case TypeArgIn:
		if cmd.Name != r.Args[0] || subcommand(cmd) != r.Args[1] {
			return false
		}
		return anyArg(cmd, r.Args[2:], false)

	case TypeArgsOutsideRepo:
		return cmd.Name == r.Args[0] && targetsOutsideRepo(cmd)

	case TypeOnProtectedBranch:
		if cmd.Name != r.Args[0] {
			return false
		}
		if len(r.Args) > 1 && subcommand(cmd) != r.Args[1] {
			return false
		}
		return onProtectedBranch(cmd.Cwd)
	}

	return false
}

func matchToolRegex(patterns []string, tool string) bool {
	for _, p := range patterns {
		// the pattern compiled during validation, so a failure here can only
		// mean the compiled defaults are broken.
		re, err := regexp.Compile(p)
		if err != nil {
			return true
		}

		if re.MatchString(tool) {
			return true
		}
	}

	return false
}

// matchPathGlob checks a file tool's target and every path-shaped word of
// every command in a Bash call, including redirection targets.
func matchPathGlob(patterns []string, req Request) (int, bool) {
	if req.FilePath != "" && matchAnyGlob(patterns, req.FilePath, "") {
		return NoCommand, true
	}

	for i, cmd := range req.Commands {
		for _, a := range append(append([]shellwalk.Arg{}, cmd.Args...), cmd.Redirs...) {
			if !a.Expanded || a.IsFlag {
				continue
			}

			if matchAnyGlob(patterns, a.Value, cmd.Cwd) {
				return i, true
			}
		}
	}

	return NoCommand, false
}

// matchAnyGlob tries every pattern against the path as written and, when it is
// relative and the cwd is known, against its resolved form too.
func matchAnyGlob(patterns []string, path, cwd string) bool {
	candidates := []string{path}
	if resolved := resolve(path, cwd); resolved != "" && resolved != path {
		candidates = append(candidates, resolved)
	}

	for _, p := range patterns {
		pattern := expandHome(p)

		for _, c := range candidates {
			if matchGlob(pattern, c) {
				return true
			}
		}
	}

	return false
}

// targetsOutsideRepo reports whether any operand of the command resolves
// outside the git repository containing its effective working directory.
//
// It fails toward the prompt at every step where the target cannot be known.
func targetsOutsideRepo(cmd shellwalk.Command) bool {
	root, found := repoRoot(cmd.Cwd)
	if !found {
		// with no repository above the cwd there is no boundary to be inside
		// of, so there is nothing to vouch for the target.
		return true
	}

	for _, a := range cmd.Args {
		if a.IsFlag {
			continue
		}

		if !a.Expanded {
			// an unexpanded parameter could name anything.
			return true
		}

		target := resolve(a.Value, cmd.Cwd)
		if target == "" {
			return true
		}

		if hasGlobMeta(a.Value) {
			// a glob only matches within its own parent directory, so the
			// parent is what has to be inside the repo. The repo root itself
			// is fine here: "rm *.tmp" at the root deletes files, unlike
			// "rm -rf ." which deletes the root.
			if !within(root, filepath.Dir(target)) {
				return true
			}

			continue
		}

		// removing the repo root wipes the working tree, untracked files and
		// .git alike, even though it resolves inside the repo.
		if target == root {
			return true
		}

		if !within(root, target) {
			return true
		}
	}

	return false
}

// within reports whether path is root or sits underneath it.
//
// Symlinks are deliberately not resolved: rm on a symlink deletes the link,
// not its target, so resolving would make a harmless in-repo symlink pointing
// outside look like an outside delete.
func within(root, path string) bool {
	if path == root {
		return true
	}

	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

// subcommand returns the command's first operand, which is where every tool
// in the default list carries its verb.
func subcommand(cmd shellwalk.Command) string {
	for _, a := range cmd.Args {
		if a.Expanded && !a.IsFlag {
			return a.Value
		}
	}

	return ""
}

// anyArg reports whether any of the wanted values appears among the command's
// words, restricted to flags or to operands.
func anyArg(cmd shellwalk.Command, wanted []string, flags bool) bool {
	for _, a := range cmd.Args {
		if !a.Expanded || a.IsFlag != flags {
			continue
		}

		if contains(wanted, a.Value) {
			return true
		}
	}

	return false
}

func contains(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}

	for _, h := range haystack {
		if h == needle {
			return true
		}
	}

	return false
}

// resolve turns a path into an absolute cleaned one against base, returning an
// empty string when it cannot be resolved.
func resolve(path, base string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	if base == "" {
		return ""
	}

	return filepath.Join(base, path)
}

// expandHome replaces a leading "~" in a glob pattern with the home
// directory.
func expandHome(pattern string) string {
	if !strings.HasPrefix(pattern, "~") {
		return pattern
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return pattern
	}

	return home + strings.TrimPrefix(pattern, "~")
}
