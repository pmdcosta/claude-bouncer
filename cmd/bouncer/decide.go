package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pmdcosta/claude-bouncer/internal/audit"
	"github.com/pmdcosta/claude-bouncer/internal/hook"
	"github.com/pmdcosta/claude-bouncer/internal/rules"
	"github.com/urfave/cli/v3"
)

func decideCommand() *cli.Command {
	return &cli.Command{
		Name:  "decide",
		Usage: "answer one PermissionRequest read from stdin (this is the hook)",
		Action: func(_ context.Context, _ *cli.Command) error {
			return decide(os.Stdin, os.Stdout, os.Stderr)
		},
	}
}

// decide answers one permission request.
//
// It always writes a decision, and that decision is Allow only when a request
// was understood and matched nothing. Malformed input, an unparseable command,
// a broken config or a panic all emit Ask.
func decide(stdin io.Reader, stdout, stderr io.Writer) (err error) {
	decision := hook.Ask
	rec := audit.Record{Time: time.Now(), Outcome: decision.String()}

	logger := logger()

	defer func() {
		// a panic must still produce a decision, and that decision is ask.
		if p := recover(); p != nil {
			rec.Error = fmt.Sprintf("panic: %v", p)
			_ = hook.Write(stdout, hook.Ask)
		}

		rec.Outcome = decision.String()
		logger.Append(rec)

		if rec.Error != "" {
			fmt.Fprintln(stderr, "bouncer:", rec.Error)
		}

		// the exit status must stay zero: a non-zero exit is a non-blocking
		// hook error, which discards the decision just written.
		err = nil
	}()

	loaded, loadErr := rules.Load(configDir())
	if loadErr != nil {
		// the compiled defaults are still in loaded, so carry on and report.
		rec.Error = loadErr.Error()
	}

	req, readErr := hook.Read(stdin)
	if readErr != nil {
		rec.Error = readErr.Error()

		return hook.Write(stdout, hook.Ask)
	}

	in := req.Input()
	rec.Session = req.SessionID
	rec.Cwd = req.Cwd
	rec.Tool = req.ToolName
	rec.Input = in.Command
	if rec.Input == "" {
		rec.Input = in.FilePath
	}

	decided, rule, decideErr := hook.Handler{Rules: loaded}.Decide(req)
	if decideErr != nil {
		rec.Error = decideErr.Error()
	}

	decision = decided
	rec.Rule = rule

	return hook.Write(stdout, decision)
}

// configDir returns the rules directory, or an empty string when it cannot be
// determined. An empty directory loads the compiled defaults.
func configDir() string {
	dir, err := rules.ConfigDir()
	if err != nil {
		return ""
	}

	return dir
}

// logger returns the audit logger, or a zero logger that writes nowhere when
// the log directory cannot be determined.
func logger() audit.Logger {
	dir, err := audit.DefaultDir()
	if err != nil {
		return audit.Logger{}
	}

	return audit.NewLogger(dir)
}
