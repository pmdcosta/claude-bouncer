package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pmdcosta/claude-bouncer/internal/rules"
	"github.com/pmdcosta/claude-bouncer/internal/settings"
	"github.com/urfave/cli/v3"
)

func enableCommand() *cli.Command {
	return &cli.Command{
		Name:  "enable",
		Usage: "seed the rules file if absent and register the hook",
		Action: func(_ context.Context, cmd *cli.Command) error {
			return runEnable(cmd.Root().Writer)
		},
	}
}

func disableCommand() *cli.Command {
	return &cli.Command{
		Name:  "disable",
		Usage: "remove the hook entry, leaving rules.yaml alone",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "purge",
				Usage: "also delete the rules file",
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return runDisable(cmd.Root().Writer, cmd.Bool("purge"))
		},
	}
}

// runEnable turns bouncer on.
//
// It refuses rather than enabling into a race or a broken config, and running
// it twice is a no-op.
func runEnable(out io.Writer) error {
	dir, err := rules.ConfigDir()
	if err != nil {
		return fmt.Errorf("failed to enable: %w", err)
	}

	created, err := rules.Seed(dir)
	if err != nil {
		return fmt.Errorf("failed to seed the rules file: %w", err)
	}

	path := filepath.Join(dir, rules.FileName)
	if created {
		fmt.Fprintln(out, "created", path)
	}

	if !created {
		fmt.Fprintln(out, "kept existing", path)
	}

	loaded, err := rules.Load(dir)
	if err != nil {
		// never enable into a broken config.
		return fmt.Errorf("failed to enable: %w", err)
	}

	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate the bouncer binary: %w", err)
	}

	binary, err = filepath.Abs(binary)
	if err != nil {
		return fmt.Errorf("failed to resolve the bouncer binary path: %w", err)
	}

	settingsPath, err := settings.DefaultPath()
	if err != nil {
		return fmt.Errorf("failed to locate the settings file: %w", err)
	}

	f, err := settings.Read(settingsPath)
	if err != nil {
		return fmt.Errorf("failed to enable: %w", err)
	}

	racing, err := f.RemoteApproverRegistered()
	if err != nil {
		return fmt.Errorf("failed to check for a competing hook: %w", err)
	}

	if racing {
		// two handlers on this event race, and the docs do not define how
		// competing decisions resolve.
		return fmt.Errorf("%w: remove it first with `claude-remote-approver uninstall`", settings.ErrRemoteApprover)
	}

	changed, err := f.Register(settings.HookCommand(binary))
	if err != nil {
		return fmt.Errorf("failed to register the hook: %w", err)
	}

	if !changed {
		fmt.Fprintln(out, "hook already registered in", settingsPath)
		fmt.Fprintf(out, "%d rules live, %d disabled\n", countOn(loaded), len(loaded)-countOn(loaded))

		return nil
	}

	if err := f.Write(); err != nil {
		return fmt.Errorf("failed to save the settings file: %w", err)
	}

	fmt.Fprintln(out, "registered", settings.HookCommand(binary), "in", settingsPath)
	fmt.Fprintf(out, "%d rules live, %d disabled\n", countOn(loaded), len(loaded)-countOn(loaded))

	return nil
}

// runDisable turns bouncer off.
//
// It is a switch, not an uninstall: rules.yaml is left alone so tuning
// survives, unless purge is asked for explicitly.
func runDisable(out io.Writer, purge bool) error {
	settingsPath, err := settings.DefaultPath()
	if err != nil {
		return fmt.Errorf("failed to locate the settings file: %w", err)
	}

	f, err := settings.Read(settingsPath)
	if err != nil {
		return fmt.Errorf("failed to disable: %w", err)
	}

	changed, err := f.Unregister()
	if err != nil {
		return fmt.Errorf("failed to remove the hook: %w", err)
	}

	if !changed {
		fmt.Fprintln(out, "no bouncer hook registered in", settingsPath)
	}

	if changed {
		if err := f.Write(); err != nil {
			return fmt.Errorf("failed to save the settings file: %w", err)
		}

		fmt.Fprintln(out, "removed the bouncer hook from", settingsPath)
	}

	if !purge {
		return nil
	}

	dir, err := rules.ConfigDir()
	if err != nil {
		return fmt.Errorf("failed to purge the rules file: %w", err)
	}

	path := filepath.Join(dir, rules.FileName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete rules file: %w", err)
	}

	fmt.Fprintln(out, "deleted", path)

	return nil
}

// countOn counts the live rules in an effective list.
func countOn(rs []rules.Rule) int {
	n := 0
	for _, r := range rs {
		if r.On() {
			n++
		}
	}

	return n
}
