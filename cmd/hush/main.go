// Command hush is the single-binary entrypoint for everything Hush:
// server, CLI, MCP server, and migration helpers.
//
// Subcommand layout deliberately mirrors `ssh` / `scp` / common cli verbs so
// muscle memory transfers and AI agents trained on shell history can drive it
// without a manual.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// Version is overridden at build time via -ldflags.
var Version = "0.1.0-dev"

func main() {
	root := &cobra.Command{
		Use:   "hush",
		Short: "Zero-trust credential & access broker",
		Long: `Hush is a credential vault + named hosts + multi-transport exec broker.

First local setup:
  hush bootstrap
  hush server
  hush doctor

Daily workflow:
  hush host ls
  hush doctor --host NAME
  hush exec NAME -- "hostname && whoami"

Host records reference vault secret names. Avoid printing raw secret values;
use hush secret set --stdin or --from-file when storing credentials.`,
		SilenceUsage:  true,
		SilenceErrors: false,
		Version:       Version,
	}

	root.AddCommand(
		newServerCmd(),
		newBootstrapCmd(),
		newLoginCmd(),
		newWhoamiCmd(),
		newDoctorCmd(),
		newSecretCmd(),
		newGetCmd(),
		newHostCmd(),
		newExecCmd(),
		newSSHCmd(),
		newCPCmd(),
		newAuditCmd(),
		newAPIKeyCmd(),
		newMigrateCmd(),
		newCompletionCmd(root),
		newMCPCmd(),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
