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
	"runtime/debug"

	"github.com/urfave/cli/v3"
)

// devVersion is what a build from a working tree reports.
const devVersion = "dev"

func main() {
	cmd := &cli.Command{
		Name:  "bouncer",
		Usage: "auto-approve routine Claude Code permission requests and log every decision",
		Description: "bouncer registers as a Claude Code PermissionRequest hook.\n" +
			"It either auto-approves a tool call or lets the normal prompt happen.\n" +
			"It never blocks anything.",
		Version: describeVersion(debug.ReadBuildInfo()),
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

// describeVersion renders the build version from the module build info.
//
// The version is read rather than stamped with -ldflags because
// `go install <module>/cmd/bouncer@v1.0.0` records it here automatically and
// passes no build flags at all, so a released binary reports its tag with no
// build machinery. Go fills the same field for a local build with a
// pseudo-version naming the commit, which is what tells you whether the binary
// registered as the hook is the code you are looking at.
func describeVersion(info *debug.BuildInfo, ok bool) string {
	if !ok {
		return devVersion
	}

	// "(devel)" is what Go reports when it has no VCS information to go on.
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}

	return devVersion
}
