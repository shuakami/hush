package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// `hush migrate` delegates to the Python implementation in sdk/python because
// the actual scanner walks Python AST and that's most natural in Python. We
// just shell out to it and forward stdin/stdout/stderr.
func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Find hardcoded credentials in Python code and rewrite them to use Hush",
		Long: `migrate scans a directory for hardcoded credentials in Python source
(constants like PASSWORD = "...", paramiko.SSHClient().connect(password=...),
inline ED25519 private keys) and either reports them or applies an automatic
rewrite that pushes them into the vault and replaces the literals with
hush.get(...) / hush.paramiko_compat.

Internally this delegates to the Python tool installed via pip:

    pip install hush-cli

…or, if that isn't installed, to ` + "`python3 -m hush.migrate`" + ` (the SDK's source path).`,
	}
	cmd.AddCommand(newMigrateScanCmd(), newMigrateApplyCmd())
	return cmd
}

func newMigrateScanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan PATH",
		Short: "Report hardcoded credentials found in PATH (no changes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPyMigrate(append([]string{"scan"}, args...))
		},
	}
}

func newMigrateApplyCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "apply PATH",
		Short: "Push hardcoded credentials into the vault and rewrite source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pyArgs := []string{"apply"}
			if dryRun {
				pyArgs = append(pyArgs, "--dry-run")
			}
			pyArgs = append(pyArgs, args...)
			return runPyMigrate(pyArgs)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview changes without writing files or vault entries")
	return cmd
}

func runPyMigrate(args []string) error {
	py := os.Getenv("HUSH_PYTHON")
	if py == "" {
		py = "python3"
	}
	if _, err := exec.LookPath(py); err != nil {
		return errors.New("`python3` not found on PATH; install Python 3.10+ then `pip install hush-cli`")
	}
	cmdArgs := append([]string{"-m", "hush.migrate"}, args...)
	c := exec.Command(py, cmdArgs...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("python migrate exited %d", ee.ExitCode())
		}
		return err
	}
	return nil
}
