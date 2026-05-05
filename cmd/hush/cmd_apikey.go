package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newAPIKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "apikey",
		Aliases: []string{"key"},
		Short:   "Manage API keys for clients and agents",
	}
	cmd.AddCommand(newAPIKeyMintCmd(), newAPIKeyLsCmd())
	return cmd
}

func newAPIKeyMintCmd() *cobra.Command {
	var name, scopes string
	cmd := &cobra.Command{
		Use:   "mint --name NAME --scopes SCOPE,SCOPE,...",
		Short: "Generate a new API key (printed once)",
		Long: `Mint a new API key. Pass scopes as a comma-separated list. Common scopes:

  *                  unrestricted (admin)
  secret:read        read secret values
  secret:list        list metadata
  secret:write       create / overwrite / delete secrets
  host:list          list / show hosts
  host:write         add / remove hosts
  host:exec          run commands via exec / multi-exec
  host:put           upload files
  host:get           download files
  audit:read         read the audit log
  apikey:list        list api keys
  apikey:write       mint / revoke api keys

A trailing "*" means a wildcard prefix, e.g. "secret:*" means any secret action.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			c, err := dial(true)
			if err != nil {
				return err
			}
			scopeList := strings.Split(scopes, ",")
			cleaned := make([]string, 0, len(scopeList))
			for _, s := range scopeList {
				s = strings.TrimSpace(s)
				if s != "" {
					cleaned = append(cleaned, s)
				}
			}
			out, err := c.MintAPIKey(cmd.Context(), name, cleaned)
			if err != nil {
				return err
			}
			fmt.Println(out.Key)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "human-readable label (required)")
	cmd.Flags().StringVar(&scopes, "scopes", "", "comma-separated scopes (e.g. \"secret:read,host:exec\")")
	return cmd
}

func newAPIKeyLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List API keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			// We don't have a typed wrapper for /api-keys list yet — issue a raw GET
			// via the clientlib's `do` is unexported; for v0.1 just print whoami until
			// list endpoint is wrapped on the client side.
			fmt.Fprintln(os.Stderr, "not yet wrapped in CLI (use HTTP API directly: GET /api/v1/api-keys)")
			return nil
		},
	}
	_ = json.RawMessage(nil)
	return cmd
}
