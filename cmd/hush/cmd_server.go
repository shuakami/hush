package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/shuakami/hush/internal/apikey"
	"github.com/shuakami/hush/internal/audit"
	"github.com/shuakami/hush/internal/broker"
	"github.com/shuakami/hush/internal/config"
	"github.com/shuakami/hush/internal/inventory"
	"github.com/shuakami/hush/internal/server"
	"github.com/shuakami/hush/internal/store"
	"github.com/shuakami/hush/internal/vault"
	"github.com/spf13/cobra"
)

func newServerCmd() *cobra.Command {
	var listen string
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run the Hush HTTP server",
		Long: `Run the Hush server. Reads HUSH_LISTEN, HUSH_DATA_DIR,
HUSH_KEK_KIND (env|file), HUSH_KEK_ENV / HUSH_KEK_FILE, and
HUSH_BOOTSTRAP_API_KEY env vars.

On first boot, if no API keys exist and HUSH_BOOTSTRAP_API_KEY is empty, an
admin key is minted automatically and printed once on stderr.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadServer()
			if err != nil {
				return err
			}
			if listen != "" {
				cfg.Listen = listen
			}
			return runServer(cmd.Context(), cfg)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "address:port (overrides $HUSH_LISTEN)")
	return cmd
}

func runServer(ctx context.Context, cfg *config.ServerConfig) error {
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	v, err := vault.Open(st, cfg.KEKKind, cfg.KEKValue)
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}

	inv := inventory.New(st)
	akm := apikey.New(st)
	alog := audit.New(st)
	br := broker.New(inv, v, alog)

	if err := bootstrapIfNeeded(ctx, akm, alog, cfg); err != nil {
		return err
	}

	srv := server.New(br, akm, alog, cfg.Listen)
	srv.Handler() // warm

	httpSrv := &http.Server{
		Addr:    cfg.Listen,
		Handler: srv.Handler(),
	}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "[hush] listening on %s (data=%s, kek=%s)\n",
			cfg.Listen, cfg.DataDir, cfg.KEKKind)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "[hush] shutting down")
		shutCtx, cancel := contextWithTimeout(5)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func bootstrapIfNeeded(ctx context.Context, akm *apikey.Manager, alog *audit.Logger, cfg *config.ServerConfig) error {
	keys, err := akm.List(ctx)
	if err != nil {
		return fmt.Errorf("list api keys: %w", err)
	}
	if len(keys) > 0 {
		return nil
	}
	if cfg.BootKey != "" {
		fmt.Fprintln(os.Stderr, "[hush] bootstrap api key provided via env (HUSH_BOOTSTRAP_API_KEY)")
		// Insert as wildcard scope
		raw := cfg.BootKey
		_ = raw // we don't store user-supplied raw keys
		fmt.Fprintln(os.Stderr, "[hush] note: HUSH_BOOTSTRAP_API_KEY auto-import not supported in v0.1; minting a fresh admin key instead")
	}
	raw, k, err := akm.Mint(ctx, "bootstrap-admin", []string{"*"})
	if err != nil {
		return fmt.Errorf("mint bootstrap api key: %w", err)
	}
	_, _ = alog.Append(ctx, "bootstrap", "apikey.bootstrap", k.Name, map[string]interface{}{"id": k.ID})

	endpoint := clientEndpointFromListen(cfg.Listen)
	saved := false
	if !truthy(os.Getenv("HUSH_NO_SAVE_CONFIG")) {
		ccfg := &config.ClientConfig{Endpoint: endpoint, APIKey: raw}
		if err := config.SaveClient(ccfg); err == nil {
			saved = true
		}
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "========================================")
	fmt.Fprintln(os.Stderr, " hush bootstrap: no api keys existed; minted an admin key")
	if saved {
		fmt.Fprintln(os.Stderr, " saved client config to ~/.hush/config; `hush exec ...` works.")
		fmt.Fprintln(os.Stderr, " raw api key was not printed because it was saved locally.")
	} else {
		fmt.Fprintln(os.Stderr, " save this NOW (it will not be shown again):")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "    "+raw)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, " on the client run (or copy the key into ~/.hush/config):")
		fmt.Fprintln(os.Stderr, "    hush login --endpoint "+endpoint+" --api-key "+raw)
	}
	fmt.Fprintln(os.Stderr, "========================================")
	return nil
}
