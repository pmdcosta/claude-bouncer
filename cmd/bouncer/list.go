package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pmdcosta/claude-bouncer/internal/audit"
	"github.com/pmdcosta/claude-bouncer/internal/rules"
	"github.com/urfave/cli/v3"
)

func rulesCommand() *cli.Command {
	return &cli.Command{
		Name:  "rules",
		Usage: "print the effective merged rule list",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "validate",
				Usage: "lint the rules file and exit non-zero on any error",
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return runRules(cmd.Root().Writer, cmd.Bool("validate"), colorEnabled(os.Stdout))
		},
	}
}

// runRules prints what is actually live, rather than what the file says.
//
// A load error still prints a list, because the compiled defaults are still
// the live list in that case. The error goes alongside it.
func runRules(out io.Writer, validate, color bool) error {
	dir, err := rules.ConfigDir()
	if err != nil {
		return fmt.Errorf("failed to list rules: %w", err)
	}

	loaded, loadErr := rules.Load(dir)

	if validate {
		if loadErr != nil {
			return loadErr
		}

		fmt.Fprintf(out, "ok: %d rules, %d live\n", len(loaded), countOn(loaded))

		return nil
	}

	p := newPalette(color)

	names, types := 0, 0
	for _, r := range loaded {
		names = max(names, len(r.Name))
		types = max(types, len(string(r.Type)))
	}

	for _, r := range loaded {
		marker, colour := "["+string(r.Source)+"]", p.dim
		if r.Source == rules.SourceFile {
			colour = p.cyan
		}

		if !r.On() {
			marker, colour = "[disabled]", p.yellow
		}

		fmt.Fprintf(out, "%s  %s  %s  %s\n",
			p.pad(colour, marker, 10),
			p.pad("", r.Name, names),
			p.pad(p.dim, string(r.Type), types),
			p.paint(p.dim, strings.Join(r.Args, " ")))
	}

	if loadErr != nil {
		// the list above is the compiled fallback, so say so plainly.
		return fmt.Errorf("the list above is the compiled fallback: %w", loadErr)
	}

	return nil
}

func logCommand() *cli.Command {
	return &cli.Command{
		Name:  "log",
		Usage: "filter the audit log",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "since",
				Usage: "only records newer than this, as a duration such as 7d, 24h or 30m",
			},
			&cli.BoolFlag{
				Name:  "allowed",
				Usage: "only auto-approved records",
			},
			&cli.BoolFlag{
				Name:  "asked",
				Usage: "only records that showed a prompt",
			},
			&cli.BoolFlag{
				Name:  "full",
				Usage: "print each command in full, on its own indented lines",
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return runLog(cmd.Root().Writer, logOptions{
				since:   cmd.String("since"),
				allowed: cmd.Bool("allowed"),
				asked:   cmd.Bool("asked"),
				full:    cmd.Bool("full"),
				color:   colorEnabled(os.Stdout),
				width:   terminalWidth(),
			})
		},
	}
}

// logOptions is what bouncer log was asked for.
type logOptions struct {
	since   string
	allowed bool
	asked   bool
	full    bool
	color   bool
	// width is where a collapsed line is cut. Zero means never cut.
	width int
}

// runLog prints matching audit records. jq over the monthly files stays a
// first-class way in; this is a convenience over the same data.
func runLog(out io.Writer, opts logOptions) error {
	if opts.allowed && opts.asked {
		return fmt.Errorf("failed to filter the log: --allowed and --asked are mutually exclusive")
	}

	query := audit.Query{}

	if opts.since != "" {
		d, err := parseSince(opts.since)
		if err != nil {
			return fmt.Errorf("failed to read the log: %w", err)
		}

		query.Since = time.Now().Add(-d)
	}

	if opts.allowed {
		query.Outcome = "allow"
	}

	if opts.asked {
		query.Outcome = "ask"
	}

	dir, err := audit.DefaultDir()
	if err != nil {
		return fmt.Errorf("failed to locate the log directory: %w", err)
	}

	records, err := audit.NewLogger(dir).Read(query)
	if err != nil {
		return fmt.Errorf("failed to read the log: %w", err)
	}

	if opts.full {
		printFull(out, records, opts)

		return nil
	}

	printCollapsed(out, records, opts)

	return nil
}

// timeLayout is short on purpose: a full RFC 3339 stamp spends twenty columns
// on text that barely changes between rows.
const timeLayout = "01-02 15:04"

// columns are the widths of the metadata columns, measured across the records
// being printed so nothing is cut and nothing is over-padded.
type columns struct {
	rule int
	tool int
}

func measure(records []audit.Record) columns {
	c := columns{}

	for _, r := range records {
		c.rule = max(c.rule, len(ruleOf(r)))
		c.tool = max(c.tool, len(r.Tool))
	}

	return c
}

// ruleOf names the rule that matched, or a dash for a default allow.
func ruleOf(r audit.Record) string {
	if r.Rule == "" {
		return "-"
	}

	return r.Rule
}

// printCollapsed prints one row per record.
//
// A shell command can be twenty lines of heredoc, which destroys the columns
// and makes the log unreadable, so every run of whitespace becomes a single
// space and the row is cut to the terminal width. The full text is always in
// the JSONL file, and behind --full.
func printCollapsed(out io.Writer, records []audit.Record, opts logOptions) {
	p := newPalette(opts.color)
	c := measure(records)

	for _, r := range records {
		prefix := fmt.Sprintf("%s  %s  %s  %s  ",
			p.paint(p.dim, r.Time.Local().Format(timeLayout)),
			p.pad(outcomeColour(p, r), r.Outcome, 5),
			p.pad(ruleColour(p, r), ruleOf(r), c.rule),
			p.pad(p.dim, r.Tool, c.tool))

		fmt.Fprintln(out, prefix+clip(collapse(r.Input), commandWidth(opts.width, c)))
	}
}

// minCommandWidth keeps the command column usable on a narrow terminal, even
// if that means the row wraps.
const minCommandWidth = 24

// commandWidth is how many columns are left for the command. Zero means the
// command is never cut.
func commandWidth(width int, c columns) int {
	if width == 0 {
		return 0
	}

	return max(width-visibleWidth(c), minCommandWidth)
}

// printFull prints the metadata and then the command as it was written.
func printFull(out io.Writer, records []audit.Record, opts logOptions) {
	p := newPalette(opts.color)

	for i, r := range records {
		if i > 0 {
			fmt.Fprintln(out)
		}

		fmt.Fprintf(out, "%s  %s  %s  %s\n",
			p.paint(p.dim, r.Time.Local().Format(timeLayout)),
			p.pad(outcomeColour(p, r), r.Outcome, 5),
			p.paint(ruleColour(p, r), ruleOf(r)),
			p.paint(p.dim, r.Tool))

		for _, line := range strings.Split(strings.TrimRight(r.Input, "\n"), "\n") {
			fmt.Fprintln(out, "    "+line)
		}

		if r.Error != "" {
			fmt.Fprintln(out, "    "+p.paint(p.red, r.Error))
		}
	}
}

// outcomeColour picks the colour for an outcome: green went through silently,
// yellow showed a prompt.
func outcomeColour(p palette, r audit.Record) string {
	if r.Outcome == "allow" {
		return p.green
	}

	return p.yellow
}

// ruleColour dims the dash of a default allow so the named rules stand out.
func ruleColour(p palette, r audit.Record) string {
	if r.Rule == "" {
		return p.dim
	}

	return p.cyan
}

// gap is the spacing between metadata columns.
const gap = 2

// visibleWidth is how many columns the metadata takes, gaps included.
func visibleWidth(c columns) int {
	return len(timeLayout) + gap + len("allow") + gap + c.rule + gap + c.tool + gap
}

// collapse turns every run of whitespace into a single space, so a multi-line
// command still occupies exactly one row.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clip cuts s to at most n columns, marking the cut.
func clip(s string, n int) string {
	if n <= 0 {
		return s
	}

	runes := []rune(s)
	if len(runes) <= n {
		return s
	}

	if n == 1 {
		return "…"
	}

	return string(runes[:n-1]) + "…"
}

// parseSince accepts a Go duration plus a day suffix, so --since 7d works.
func parseSince(s string) (time.Duration, error) {
	if days, found := strings.CutSuffix(s, "d"); found {
		d, err := time.ParseDuration(days + "h")
		if err != nil {
			return 0, fmt.Errorf("failed to parse --since %q: %w", s, err)
		}

		return d * 24, nil
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("failed to parse --since %q: %w", s, err)
	}

	return d, nil
}
