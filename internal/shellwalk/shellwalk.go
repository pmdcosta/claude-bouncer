// Package shellwalk parses a shell command string into the simple commands
// inside it, in execution order, each carrying the working directory in effect
// when it runs.
//
// A Bash command is not one command: "cd /x && rm -f MEMORY.md" is two, and
// the cd in the first is what decides whether the rm in the second is safe.
// Splitting on separators cannot express that, and mistakes quoted text such
// as `echo "cd /tmp && rm x"` for real commands, so this package parses a real
// shell AST instead.
package shellwalk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Arg is one word of a command after literal flattening.
type Arg struct {
	// Value is the flattened text, with a leading unquoted "~" already
	// expanded. It is only meaningful when Expanded is true.
	Value string
	// Expanded reports whether the whole word was statically knowable. It is
	// false for words containing a parameter, command substitution or
	// arithmetic expansion.
	Expanded bool
	// IsFlag reports whether the word looks like an option rather than an
	// operand.
	IsFlag bool
}

// Command is one simple command found in a shell string.
type Command struct {
	// Name is the leading word, for example "git".
	Name string
	// Args are the remaining words, in order.
	Args []Arg
	// Redirs are the targets of the command's redirections, for example the
	// "f" in "echo x > f".
	Redirs []Arg
	// Cwd is the effective working directory when the command runs, or empty
	// when it can no longer be determined statically.
	Cwd string
}

// Walk parses a shell command string and returns the simple commands inside
// it, in execution order.
//
// Commands whose name is not statically knowable, such as "$CMD -rf /", are
// not reported. Neither are commands reached only through a variable, an eval
// or a shell function call. This package is a noise cutter, not a sandbox.
func Walk(command, cwd string) ([]Command, error) {
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))

	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, fmt.Errorf("failed to parse command: %w", err)
	}

	w := &walker{home: home()}
	w.stmts(file.Stmts, state{path: cwd})

	return w.out, nil
}

// state is the effective working directory for the next command.
type state struct {
	path string
	// unknown records that the cwd can no longer be determined statically, so
	// every later command must fail toward the permission prompt.
	unknown bool
}

// cwd renders the state as a Command.Cwd value.
func (s state) cwd() string {
	if s.unknown {
		return ""
	}

	return s.path
}

type walker struct {
	home string
	out  []Command
	// cds counts directory changes seen so far, so a body walked in a copied
	// state can still report that it moved.
	cds int
}

// stmts walks a statement list in order, threading the state through it.
func (w *walker) stmts(ss []*syntax.Stmt, st state) state {
	for _, s := range ss {
		st = w.stmt(s, st)
	}

	return st
}

// stmt walks one statement and returns the state left behind for the next one.
func (w *walker) stmt(s *syntax.Stmt, st state) state {
	if s == nil {
		return st
	}

	switch cmd := s.Cmd.(type) {
	case *syntax.CallExpr:
		return w.call(cmd, s.Redirs, st)

	case *syntax.BinaryCmd:
		return w.binary(cmd, st)

	case *syntax.Subshell:
		// a cd inside a subshell does not leak out of it.
		w.stmts(cmd.Stmts, st)
		return st

	case *syntax.Block:
		// a plain brace group shares the current shell, so a cd does leak.
		return w.stmts(cmd.Stmts, st)

	case *syntax.IfClause:
		return w.scoped(st, func(inner state) {
			w.ifClause(cmd, inner)
		})

	case *syntax.ForClause:
		return w.scoped(st, func(inner state) {
			w.stmts(cmd.Do, inner)
		})

	case *syntax.WhileClause:
		return w.scoped(st, func(inner state) {
			inner = w.stmts(cmd.Cond, inner)
			w.stmts(cmd.Do, inner)
		})

	case *syntax.CaseClause:
		return w.scoped(st, func(inner state) {
			for _, item := range cmd.Items {
				w.stmts(item.Stmts, inner)
			}
		})

	case *syntax.FuncDecl:
		return w.scoped(st, func(inner state) {
			w.stmt(cmd.Body, inner)
		})

	case *syntax.TimeClause:
		return w.stmt(cmd.Stmt, st)

	case nil:
		return st
	}

	// arithmetic, test and declaration clauses cannot run a command or change
	// the cwd, so they leave the state alone.
	return st
}

// ifClause walks an if statement and its elif/else chain. Each branch is
// exclusive, so each one starts from the state the if was entered with.
func (w *walker) ifClause(c *syntax.IfClause, st state) {
	for ; c != nil; c = c.Else {
		w.stmts(c.Then, w.stmts(c.Cond, st))
	}
}

// scoped walks a body that runs an unknown number of times, or under an
// unknown condition, in a copy of the state. If the body changed directory at
// all, the cwd afterwards is no longer knowable.
func (w *walker) scoped(st state, body func(state)) state {
	before := w.cds

	body(st)

	if w.cds > before {
		st.unknown = true
	}

	return st
}

// binary walks "&&", "||" and pipelines.
func (w *walker) binary(cmd *syntax.BinaryCmd, st state) state {
	if cmd.Op != syntax.OrStmt {
		// "&&" and pipelines run the right side after the left, so the left
		// side's cwd applies.
		return w.stmt(cmd.Y, w.stmt(cmd.X, st))
	}

	// with "||" the left side failed, so its cd may or may not have run.
	// evaluate the right side under both states and report both, so a rule
	// escalates if either one is unsafe.
	before := st
	after := w.stmt(cmd.X, st)

	w.stmt(cmd.Y, before)
	w.stmt(cmd.Y, after)

	if before != after {
		after.unknown = true
	}

	return after
}

// call emits one simple command and applies its effect on the cwd.
func (w *walker) call(cmd *syntax.CallExpr, redirs []*syntax.Redirect, st state) state {
	// commands hidden inside a substitution run first, in their own shell.
	for _, word := range cmd.Args {
		w.substs(word.Parts, st)
	}

	if len(cmd.Args) == 0 {
		// assignments only, no command to run.
		return st
	}

	name := w.flatten(cmd.Args[0])
	if !name.Expanded || name.Value == "" {
		// the command name is not statically knowable, or is an empty quoted
		// word, so there is nothing to match a rule against.
		return st
	}

	args := make([]Arg, 0, len(cmd.Args)-1)
	for _, word := range cmd.Args[1:] {
		args = append(args, w.flatten(word))
	}

	targets := make([]Arg, 0, len(redirs))
	for _, r := range redirs {
		if r.Word == nil {
			continue
		}
		targets = append(targets, w.flatten(r.Word))
	}

	w.out = append(w.out, Command{
		Name:   name.Value,
		Args:   args,
		Redirs: targets,
		Cwd:    st.cwd(),
	})

	return w.chdir(name.Value, args, st)
}

// chdir applies a directory-changing command to the state.
func (w *walker) chdir(name string, args []Arg, st state) state {
	switch name {
	case "cd":
	case "pushd", "popd":
		// the directory stack is not tracked.
		w.cds++
		st.unknown = true
		return st
	default:
		return st
	}

	w.cds++

	if len(args) == 0 {
		// a bare cd goes home, which is outside every repo.
		st.path = w.home
		st.unknown = false
		return st
	}

	// "cd -" returns to the previous directory and any flag pushes the target
	// past the first argument. Neither is worth tracking; fail toward the
	// prompt.
	if !args[0].Expanded || args[0].IsFlag {
		st.unknown = true
		return st
	}

	st.path = resolve(args[0].Value, st.path)
	st.unknown = st.path == ""

	return st
}

// substs walks the commands inside any substitution in the given word parts.
// They run in their own shell, so their cwd changes are discarded.
func (w *walker) substs(parts []syntax.WordPart, st state) {
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.CmdSubst:
			w.stmts(p.Stmts, st)
		case *syntax.ProcSubst:
			w.stmts(p.Stmts, st)
		case *syntax.DblQuoted:
			w.substs(p.Parts, st)
		}
	}
}

// flatten renders a word as literal text, reporting whether the whole word was
// statically knowable.
//
// syntax.Word.Lit cannot be used for this: it returns an empty string as soon
// as any part is not a literal, so a quoted path would be indistinguishable
// from an unexpanded parameter.
func (w *walker) flatten(word *syntax.Word) Arg {
	var b strings.Builder

	ok := appendParts(&b, word.Parts)
	value := b.String()

	// only an unquoted leading tilde is a home directory, as in the shell.
	if lit, isLit := firstLit(word.Parts); isLit && strings.HasPrefix(lit, "~") {
		value = w.home + strings.TrimPrefix(value, "~")
	}

	return Arg{
		Value:    value,
		Expanded: ok,
		IsFlag:   ok && strings.HasPrefix(value, "-"),
	}
}

// appendParts writes the literal text of the parts and reports whether all of
// them were literal.
func appendParts(b *strings.Builder, parts []syntax.WordPart) bool {
	ok := true

	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			if !appendParts(b, p.Parts) {
				ok = false
			}
		default:
			// a parameter, substitution, arithmetic expansion or extended glob
			// cannot be resolved without running the shell.
			ok = false
		}
	}

	return ok
}

// firstLit returns the value of the word's first part when that part is an
// unquoted literal.
func firstLit(parts []syntax.WordPart) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}

	lit, ok := parts[0].(*syntax.Lit)
	if !ok {
		return "", false
	}

	return lit.Value, true
}

// resolve turns a path into an absolute, cleaned one against base, returning
// an empty string when base is unknown.
func resolve(path, base string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	if base == "" {
		return ""
	}

	return filepath.Join(base, path)
}

// home returns the user's home directory, or an empty string if it cannot be
// determined. An empty home makes every tilde path unresolvable, which fails
// toward the permission prompt.
func home() string {
	if dir := os.Getenv("HOME"); dir != "" {
		return dir
	}

	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return dir
}
