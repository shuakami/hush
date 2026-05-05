package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/shuakami/hush/internal/inventory"
	"github.com/spf13/cobra"
)

func newHostCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "host",
		Aliases: []string{"hosts"},
		Short:   "Manage the host inventory",
	}
	cmd.AddCommand(newHostAddCmd(), newHostLsCmd(), newHostShowCmd(), newHostRmCmd())
	return cmd
}

func newHostAddCmd() *cobra.Command {
	var (
		transport, address, port, sshUser, authKind, authSecret, jumpVia, relaySecret, osType, displayName string
		tags                                                                                               []string
		meta                                                                                               []string
	)
	cmd := &cobra.Command{
		Use:   "add NAME",
		Short: "Register or update a host (re-runnable; same name = update)",
		Args:  cobra.ExactArgs(1),
		Example: `  # SSH host with password from vault
  hush host add hk1 --transport ssh --address 10.0.0.10 --port 22 --user root \
                    --auth-kind password --auth-secret hk1-root-password --tag hk

  # SSH through a jump host
  hush host add panel --transport ssh --address 10.0.0.20 --user root \
                      --auth-kind key --auth-secret panel-key --jump-via hk1

  # ssh.sdjz.wiki/c/<TOKEN> relay (for NAT'd machines)
  hush host add nmg-mac --transport sdjz-relay --relay-secret nmg-mac-relay-token --tag nmg`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dial(true)
			if err != nil {
				return err
			}
			h := &inventory.Host{
				Name:        args[0],
				DisplayName: displayName,
				Transport:   inventory.Transport(transport),
				Address:     address,
				OS:          osType,
				SSHUser:     sshUser,
				AuthKind:    inventory.AuthKind(authKind),
				AuthSecret:  authSecret,
				JumpVia:     jumpVia,
				RelaySecret: relaySecret,
				Tags:        tags,
				Metadata:    map[string]string{},
			}
			if port != "" {
				_, err := fmt.Sscanf(port, "%d", &h.Port)
				if err != nil {
					return fmt.Errorf("invalid --port: %w", err)
				}
			}
			for _, kv := range meta {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("--meta wants k=v, got %q", kv)
				}
				h.Metadata[k] = v
			}
			if err := c.PutHost(cmd.Context(), h); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "registered host %q (transport=%s)\n", h.Name, h.Transport)
			return nil
		},
	}
	cmd.Flags().StringVar(&transport, "transport", "ssh", "transport: ssh | sdjz-relay")
	cmd.Flags().StringVar(&address, "address", "", "ssh: host address")
	cmd.Flags().StringVar(&port, "port", "22", "ssh: port")
	cmd.Flags().StringVar(&sshUser, "user", "root", "ssh: username")
	cmd.Flags().StringVar(&osType, "os", "linux", "target OS: linux | windows")
	cmd.Flags().StringVar(&authKind, "auth-kind", "password", "ssh auth kind: password | key")
	cmd.Flags().StringVar(&authSecret, "auth-secret", "", "ssh auth: vault secret name to read password / key from")
	cmd.Flags().StringVar(&jumpVia, "jump-via", "", "name of another registered host to ProxyJump through")
	cmd.Flags().StringVar(&relaySecret, "relay-secret", "", "sdjz-relay: vault secret name holding the relay token")
	cmd.Flags().StringVar(&displayName, "display-name", "", "human-friendly label")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tag(s) for multi-host selection (repeatable)")
	cmd.Flags().StringSliceVar(&meta, "meta", nil, "metadata k=v (repeatable; e.g. relay_url=https://ssh.example.com)")
	return cmd
}

func newHostLsCmd() *cobra.Command {
	var tag, format string
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List registered hosts",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dial(true)
			if err != nil {
				return err
			}
			hosts, err := c.ListHosts(cmd.Context(), tag)
			if err != nil {
				return err
			}
			if format == "json" {
				return json.NewEncoder(os.Stdout).Encode(hosts)
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tTRANSPORT\tADDRESS\tUSER\tTAGS")
			for _, h := range hosts {
				addr := h.Address
				if h.Port != 0 && h.Port != 22 && h.Transport == inventory.TransportSSH {
					addr = fmt.Sprintf("%s:%d", h.Address, h.Port)
				}
				if h.Transport == inventory.TransportSDJZRelay {
					addr = "(via sdjz-relay)"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					h.Name, h.Transport, addr, h.SSHUser, strings.Join(h.Tags, ","))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "filter by tag")
	cmd.Flags().StringVar(&format, "format", "table", "output format: table | json")
	return cmd
}

func newHostShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show NAME",
		Short: "Print full record for one host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dial(true)
			if err != nil {
				return err
			}
			h, err := c.GetHost(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(h)
		},
	}
}

func newHostRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm NAME",
		Short: "Remove a host record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dial(true)
			if err != nil {
				return err
			}
			return c.DeleteHost(cmd.Context(), args[0])
		},
	}
}
