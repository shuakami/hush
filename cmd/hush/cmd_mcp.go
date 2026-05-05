package main

import (
	"github.com/shuakami/hush/internal/mcpserver"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run a Model Context Protocol server over stdio (for Claude / Cursor / Cline)",
		Long: `mcp starts a stdio MCP server that wraps the Hush HTTP API.
Tools exposed:

  host_list           list reachable hosts (no creds)
  host_exec           run a single-host command
  host_exec_multi     run a command across all hosts matching a tag
  secret_get          fetch a secret value (audited; only if the API key has secret:read)
  audit_tail          tail the audit log

Connection details: pure stdio (newline-delimited JSON-RPC). Configure your
client to spawn:  hush mcp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dial(true)
			if err != nil {
				return err
			}
			return mcpserver.Serve(cmd.Context(), c)
		},
	}
}
