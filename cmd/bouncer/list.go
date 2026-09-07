package main

import (
	"context"
	"fmt"
	"io"
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
			return runRules(cmd.Root().Writer, cmd.Bool("validate"))
		},
	}
}

// runRules prints what is actually live, rather than what the file says.
//
// A load error still prints a list, because the compiled defaults are still
// the live list in that case. The error goes alongside it.
func runRules(out io.Writer, validate bool) error {
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

	width := 0
	for _, r := range loaded {
		width = max(width, len(r.Name))
	}

	for _, r := range loaded {
		marker := "[" + string(r.Source) + "]"
		if !r.On() {
			marker = "[disabled]"
		}

		fmt.Fprintf(out, "%-10s %-*s  %-20s %s\n", marker, width, r.Name, r.Type, strings.Join(r.Args, " "))
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
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return runLog(cmd.Root().Writer, cmd.String("since"), cmd.Bool("allowed"), cmd.Bool("asked"))
		},
	}
}

// runLog prints matching audit records. jq over the monthly files stays a
// first-class way in; this is a convenience over the same data.
func runLog(out io.Writer, since string, allowed, asked bool) error {
	if allowed && asked {
		return fmt.Errorf("failed to filter the log: --allowed and --asked are mutually exclusive")
	}

	query := audit.Query{}

	if since != "" {
		d, err := parseSince(since)
		if err != nil {
			return fmt.Errorf("failed to read the log: %w", err)
		}

		query.Since = time.Now().Add(-d)
	}

	if allowed {
		query.Outcome = "allow"
	}

	if asked {
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

	for _, r := range records {
		rule := r.Rule
		if rule == "" {
			rule = "-"
		}

		fmt.Fprintf(out, "%s  %-5s  %-24s %-14s %s\n",
			r.Time.Format(time.RFC3339), r.Outcome, rule, r.Tool, r.Input)
	}

	return nil
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
