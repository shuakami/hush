package main

import (
	"fmt"
	"os"

	"github.com/shuakami/hush/internal/config"
	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var endpoint, apiKey string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Save endpoint + API key to ~/.hush/config",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.LoadClient()
			if endpoint != "" {
				cfg.Endpoint = endpoint
			}
			if apiKey != "" {
				cfg.APIKey = apiKey
			}
			if err := cfg.Validate(true); err != nil {
				return err
			}
			if err := config.SaveClient(cfg); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "saved client config (endpoint =", cfg.Endpoint+")")
			return nil
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "hush server endpoint (e.g. http://127.0.0.1:8443)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key (hush_...)")
	return cmd
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Print the current authenticated identity",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dial(true)
			if err != nil {
				return err
			}
			out, err := c.TailAudit(cmd.Context(), 1) // simple ping that exercises auth
			_ = out
			if err != nil {
				return err
			}
			fmt.Println("ok")
			return nil
		},
	}
}
