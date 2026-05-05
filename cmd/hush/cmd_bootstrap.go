package main

import (
	"fmt"
	"os"

	"github.com/shuakami/hush/internal/apikey"
	"github.com/shuakami/hush/internal/audit"
	"github.com/shuakami/hush/internal/config"
	"github.com/shuakami/hush/internal/store"
	"github.com/shuakami/hush/internal/vault"
	"github.com/spf13/cobra"
)

func newBootstrapCmd() *cobra.Command {
	var noSave bool
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Initialise data dir + master key + first admin API key (offline)",
		Long: `bootstrap is the one-shot first-run command.

It opens (or creates) the data directory, opens the vault, mints an admin
API key with scope ["*"], prints the raw key on stdout, and — unless
--no-save is set — writes the endpoint and the key into ~/.hush/config so
that the CLI on this machine works immediately without a separate
"hush login" step.

Pass --no-save when bootstrapping a server whose CLI will run on a
different machine; in that case copy the printed key by hand.`,
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

			if noSave {
				return nil
			}
			ccfg := &config.ClientConfig{
				Endpoint: clientEndpointFromListen(cfg.Listen),
				APIKey:   raw,
			}
			if err := config.SaveClient(ccfg); err != nil {
				fmt.Fprintln(os.Stderr, "warning: could not save client config:", err)
				return nil
			}
			fmt.Fprintf(os.Stderr, "saved client config (~/.hush/config) — endpoint = %s\n", ccfg.Endpoint)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noSave, "no-save", false, "print the api key only; do not touch ~/.hush/config")
	return cmd
}
