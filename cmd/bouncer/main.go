// Command bouncer answers Claude Code permission requests, auto-approving
// routine tool calls and logging every decision.
//
// It is a noise cutter and an audit log, not an auth layer: it is
// allow-by-default, it never blocks anything, and it is trivially sidestepped.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

// version is the build version, overridable with -ldflags.
var version = "dev"

func main() {
	cmd := &cli.Command{
		Name:  "bouncer",
		Usage: "auto-approve routine Claude Code permission requests and log every decision",
		Description: "bouncer registers as a Claude Code PermissionRequest hook.\n" +
			"It either auto-approves a tool call or lets the normal prompt happen.\n" +
			"It never blocks anything.",
		Version: version,
		Commands: []*cli.Command{
			decideCommand(),
			explainCommand(),
			enableCommand(),
			disableCommand(),
			rulesCommand(),
			logCommand(),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "bouncer:", err)
		os.Exit(1)
	}
}
