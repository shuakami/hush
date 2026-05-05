package main

import (
	"fmt"

	"github.com/shuakami/hush/internal/clientlib"
	"github.com/spf13/cobra"
)

// `hush ssh HOST [cmd...]` is a thin wrapper that runs the command via the
// HTTP exec endpoint. Interactive PTY support is on the v0.2 list (requires
// the server to expose a streaming exec endpoint). For now, this is a
// drop-in for the common `ssh user@host cmd` non-interactive pattern.
func newSSHCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh HOST [COMMAND...]",
		Short: "Run a command on HOST through hush (drop-in for `ssh user@host cmd`)",
		Long: `ssh is a familiar shorthand for ` + "`" + `hush exec` + "`" + `. With a command, it runs
non-interactively. Without a command, v0.1 prints a helpful error directing
you to v0.2's interactive PTY support; in the meantime use:

    hush exec HOST -- bash -i

…if your server permits it.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := args[0]
			cmdArgs := args[1:]
			if len(cmdArgs) == 0 {
				return fmt.Errorf("interactive PTY shells are a v0.2 feature; use `hush exec %s -- <command>` for now", host)
			}
			c, err := dial(true)
			if err != nil {
				return err
			}
			res, err := c.Exec(cmd.Context(), clientlib.ExecRequest{
				Host:    host,
				Command: joinShellSafe(cmdArgs),
			})
			if err != nil {
				return err
			}
			_, _ = cmd.OutOrStdout().Write(res.Stdout)
			_, _ = cmd.ErrOrStderr().Write(res.Stderr)
			if res.ExitCode != 0 {
				return ExitCodeError{Code: res.ExitCode}
			}
			return nil
		},
	}
	return cmd
}

// ExitCodeError carries an exit code out of cobra so we can propagate it.
type ExitCodeError struct{ Code int }

// Error implements error.
func (e ExitCodeError) Error() string { return fmt.Sprintf("remote exited with code %d", e.Code) }

// joinShellSafe re-quotes cmd args into one shell command. We use single
// quotes; embedded single quotes are escaped via the standard '\” trick.
func joinShellSafe(args []string) string {
	buf := make([]byte, 0, 64)
	for i, a := range args {
		if i > 0 {
			buf = append(buf, ' ')
		}
		buf = append(buf, '\'')
		for _, r := range a {
			if r == '\'' {
				buf = append(buf, '\'', '\\', '\'', '\'')
			} else {
				buf = append(buf, byte(r))
			}
		}
		buf = append(buf, '\'')
	}
	return string(buf)
}
