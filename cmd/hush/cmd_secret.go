package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secret",
		Aliases: []string{"sec"},
		Short:   "Manage credentials in the vault",
	}
	cmd.AddCommand(newSecretGetCmd(), newSecretSetCmd(), newSecretLsCmd(), newSecretRmCmd())
	return cmd
}

// `hush get NAME` is an alias of `hush secret get NAME`. Pure ergonomics:
// agents and humans both reach for the short form. (Note: the file-transfer
// "scp"-style verb lives at `hush cp` to avoid the clash.)
func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get NAME",
		Short: "Print a secret to stdout (alias of `hush secret get`)",
		Args:  cobra.ExactArgs(1),
		RunE:  runSecretGet,
	}
}

func newSecretGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get NAME",
		Short: "Print a secret value to stdout (no trailing newline)",
		Args:  cobra.ExactArgs(1),
		RunE:  runSecretGet,
	}
}

func runSecretGet(cmd *cobra.Command, args []string) error {
	c, err := dial(true)
	if err != nil {
		return err
	}
	v, err := c.GetSecret(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	_, _ = io.WriteString(os.Stdout, v)
	return nil
}

func newSecretSetCmd() *cobra.Command {
	var value string
	var fromFile string
	var fromStdin bool
	var meta []string
	cmd := &cobra.Command{
		Use:   "set NAME [--value V | --stdin | --from-file PATH]",
		Short: "Create or rotate a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dial(true)
			if err != nil {
				return err
			}
			val, err := readValue(value, fromFile, fromStdin)
			if err != nil {
				return err
			}
			metaMap := map[string]string{}
			for _, kv := range meta {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("--meta requires k=v form, got %q", kv)
				}
				metaMap[k] = v
			}
			out, err := c.PutSecret(cmd.Context(), args[0], val, metaMap)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "stored %s (version=%d)\n", out.Name, out.Version)
			return nil
		},
	}
	cmd.Flags().StringVar(&value, "value", "", "secret value (NOT recommended; ends up in shell history)")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "read value from file path")
	cmd.Flags().BoolVar(&fromStdin, "stdin", false, "read value from stdin (best for piping)")
	cmd.Flags().StringSliceVar(&meta, "meta", nil, "metadata k=v pairs (repeatable)")
	return cmd
}

func readValue(value, fromFile string, fromStdin bool) (string, error) {
	count := 0
	if value != "" {
		count++
	}
	if fromFile != "" {
		count++
	}
	if fromStdin {
		count++
	}
	if count != 1 {
		return "", errors.New("exactly one of --value / --from-file / --stdin is required")
	}
	switch {
	case value != "":
		return value, nil
	case fromFile != "":
		body, err := os.ReadFile(fromFile)
		if err != nil {
			return "", err
		}
		return normalizeSecretBody(body), nil
	default:
		body, err := io.ReadAll(bufio.NewReader(os.Stdin))
		if err != nil {
			return "", err
		}
		return normalizeSecretBody(body), nil
	}
}

func normalizeSecretBody(body []byte) string {
	v := strings.ReplaceAll(string(body), "\r\n", "\n")
	v = strings.ReplaceAll(v, "\r", "\n")
	return strings.TrimRight(v, "\n")
}

func newSecretLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List all secrets (metadata only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dial(true)
			if err != nil {
				return err
			}
			list, err := c.ListSecrets(cmd.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tVERSION\tUPDATED")
			for _, s := range list {
				fmt.Fprintf(tw, "%s\t%d\t%s\n", s.Name, s.Version, s.UpdatedAt.Format(time.RFC3339))
			}
			return tw.Flush()
		},
	}
}

func newSecretRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm NAME",
		Short: "Delete a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dial(true)
			if err != nil {
				return err
			}
			return c.DeleteSecret(cmd.Context(), args[0])
		},
	}
}
