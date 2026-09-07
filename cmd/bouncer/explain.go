package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/pmdcosta/claude-bouncer/internal/hook"
	"github.com/pmdcosta/claude-bouncer/internal/rules"
	"github.com/pmdcosta/claude-bouncer/internal/shellwalk"
	"github.com/urfave/cli/v3"
)

func explainCommand() *cli.Command {
	return &cli.Command{
		Name:      "explain",
		Usage:     "say what bouncer would decide about a command, and why",
		ArgsUsage: "[command]",
		Description: "Answers the same question the hook does, without writing an audit\n" +
			"record, so working out why something prompted does not pollute the log.\n\n" +
			"Reads the command from the argument, or from stdin when it is \"-\".",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "cwd",
				Usage: "working directory the command runs in (default: the current one)",
			},
			&cli.StringFlag{
				Name:  "tool",
				Value: "Bash",
				Usage: "tool name, for matching tool_regex rules",
			},
			&cli.StringFlag{
				Name:  "file",
				Usage: "file path instead of a command, as a Write or Edit would send",
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			command, err := explainInput(cmd.Args().First(), os.Stdin)
			if err != nil {
				return err
			}

			return runExplain(cmd.Root().Writer, explainOptions{
				command: command,
				file:    cmd.String("file"),
				tool:    cmd.String("tool"),
				cwd:     cmd.String("cwd"),
				color:   colorEnabled(os.Stdout),
			})
		},
	}
}

// explainInput takes the command from the argument, or from stdin when the
// argument is "-", which keeps a heredoc-heavy command out of the shell's way.
func explainInput(arg string, stdin io.Reader) (string, error) {
	if arg != "-" {
		return arg, nil
	}

	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("failed to read the command from stdin: %w", err)
	}

	return string(data), nil
}

// explainOptions is what bouncer explain was asked about.
type explainOptions struct {
	command string
	file    string
	tool    string
	cwd     string
	color   bool
}

// runExplain reports the decision and the facts behind it.
//
// The walk is the part worth showing: a rule matches one simple command at one
// effective working directory, and both of those come from parsing the whole
// command rather than from the text you typed.
func runExplain(out io.Writer, opts explainOptions) error {
	if opts.command == "" && opts.file == "" {
		return fmt.Errorf("failed to explain: give a command, or --file for a file path")
	}

	cwd := opts.cwd
	if cwd == "" {
		working, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to determine the working directory: %w", err)
		}

		cwd = working
	}

	loaded, loadErr := rules.Load(configDir())

	p := newPalette(opts.color)

	var (
		cmds    []shellwalk.Command
		walkErr error
	)

	if opts.command != "" {
		cmds, walkErr = shellwalk.Walk(opts.command, cwd)
	}

	if walkErr != nil {
		// an unparseable command is escalated, so say so rather than guessing.
		fmt.Fprintf(out, "%s  %s\n", p.paint(p.yellow, "ask"), p.paint(p.red, walkErr.Error()))
		fmt.Fprintln(out, "  bouncer prompts for anything it cannot parse.")

		return nil
	}

	name, at := rules.MatchDetail(loaded, rules.Request{
		ToolName: opts.tool,
		FilePath: opts.file,
		Commands: cmds,
	})

	printVerdict(out, p, loaded, name)
	printWalk(out, p, cmds, opts, cwd, at)

	if loadErr != nil {
		fmt.Fprintf(out, "\n%s %s\n", p.paint(p.red, "rules file problem, using compiled defaults:"), loadErr)
	}

	return nil
}

// printVerdict prints the decision and, when a rule matched, that rule.
func printVerdict(out io.Writer, p palette, loaded []rules.Rule, name string) {
	if name == "" {
		fmt.Fprintf(out, "%s  no rule matches, so this runs without a prompt\n",
			p.paint(p.green, hook.Allow.String()))

		return
	}

	fmt.Fprintf(out, "%s  %s\n", p.paint(p.yellow, hook.Ask.String()), p.paint(p.cyan, name))

	for _, r := range loaded {
		if r.Name != name {
			continue
		}

		fmt.Fprintf(out, "  rule: %s [%s]  %s\n", r.Type, strings.Join(r.Args, " "),
			p.paint(p.dim, "from "+string(r.Source)))
	}
}

// printWalk prints each simple command with the state a rule judged it in,
// marking the one that matched.
//
// A command string can hold a dozen simple commands, so pointing at the one
// that matched is the difference between an answer and a haystack.
func printWalk(out io.Writer, p palette, cmds []shellwalk.Command, opts explainOptions, cwd string, at int) {
	if opts.file != "" {
		fmt.Fprintf(out, "\n%s %s %s\n", p.paint(p.dim, "file:"), opts.file, p.paint(p.dim, "tool "+opts.tool))
	}

	if len(cmds) == 0 {
		return
	}

	heading := "commands found, in execution order:"
	if at >= 0 && at < len(cmds) {
		heading = "commands found, in execution order (-> is the one that matched):"
	}

	fmt.Fprintf(out, "\n%s\n", p.paint(p.dim, heading))

	// the number column has to fit the largest index, or double digits shove
	// every line along by one.
	digits := len(strconv.Itoa(len(cmds)))

	previous := ""

	for i, c := range cmds {
		marker, colour := strings.Repeat(" ", 2), ""
		if i == at {
			marker, colour = "->", p.yellow
		}

		fmt.Fprintf(out, "%s %*d. %s\n", p.paint(colour, marker), digits, i+1,
			p.paint(colour, words(c)))

		// the state is only worth a line when it changed, or when this is the
		// command that matched. Repeating an identical cwd for every command in
		// a long pipeline buries the one line that matters.
		state := describeState(c, cwd)
		if state != previous || i == at {
			fmt.Fprintf(out, "%s%s\n", strings.Repeat(" ", digits+5), p.paint(p.dim, state))
		}

		previous = state
	}
}

// words renders a command the way it would be typed, marking any word that
// could not be resolved without running the shell.
func words(c shellwalk.Command) string {
	out := []string{c.Name}

	for _, a := range c.Args {
		if !a.Expanded {
			out = append(out, "<unexpanded>")

			continue
		}

		out = append(out, a.Value)
	}

	return strings.Join(out, " ")
}

// describeState renders the working directory, repository boundary and branch
// a command was judged against. Those three are what the path, rm and branch
// rules actually read, and none of them are visible in the command text.
func describeState(c shellwalk.Command, cwd string) string {
	if c.Cwd == "" {
		return "cwd unknown, so every path rule prompts"
	}

	parts := []string{"cwd " + c.Cwd}
	if c.Cwd != cwd {
		parts[0] += " (moved by an earlier cd)"
	}

	root, found := rules.RepoRoot(c.Cwd)
	if !found {
		parts = append(parts, "no git repo above it, so rm has no boundary to be inside of")

		return strings.Join(parts, ", ")
	}

	parts = append(parts, "repo "+root)

	branch, known := rules.HeadBranch(c.Cwd)
	if !known {
		return strings.Join(parts, ", ")
	}

	if branch == "" {
		return strings.Join(append(parts, "detached HEAD"), ", ")
	}

	return strings.Join(append(parts, "branch "+branch), ", ")
}
