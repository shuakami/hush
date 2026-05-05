package main

import (
	"fmt"

	"github.com/shuakami/hush/internal/apikey"
	"github.com/shuakami/hush/internal/audit"
	"github.com/shuakami/hush/internal/config"
	"github.com/shuakami/hush/internal/store"
	"github.com/shuakami/hush/internal/vault"
	"github.com/spf13/cobra"
)

func newBootstrapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bootstrap",
		Short: "Initialise data dir + master key + first admin API key (offline)",
		Long: `bootstrap is for the offline / out-of-band case where you want to
provision a fresh data directory before starting the server. It mints an admin
API key with scope ["*"] and prints it on stdout.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadServer()
			if err != nil {
				return err
			}
			st, err := store.Open(cfg.DataDir)
			if err != nil {
				return err
			}
			defer st.Close()

			if _, err := vault.Open(st, cfg.KEKKind, cfg.KEKValue); err != nil {
				return fmt.Errorf("open vault (KEK source = %s): %w", cfg.KEKKind, err)
			}
			akm := apikey.New(st)
			alog := audit.New(st)
			raw, k, err := akm.Mint(cmd.Context(), "bootstrap-admin", []string{"*"})
			if err != nil {
				return err
			}
			_, _ = alog.Append(cmd.Context(), "bootstrap-cli", "apikey.bootstrap", k.Name, map[string]interface{}{"id": k.ID})
			fmt.Println(raw)
			return nil
		},
	}
}
