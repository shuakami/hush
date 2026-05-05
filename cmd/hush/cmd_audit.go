package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect the append-only audit log",
	}
	cmd.AddCommand(newAuditTailCmd(), newAuditVerifyCmd())
	return cmd
}

func newAuditTailCmd() *cobra.Command {
	var n int
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "tail",
		Short: "Print the most recent N audit records",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dial(true)
			if err != nil {
				return err
			}
			records, err := c.TailAudit(cmd.Context(), n)
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(records)
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "TS\tACTOR\tACTION\tTARGET")
			for _, r := range records {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.TS.Format("15:04:05"), r.Actor, r.Action, r.Target)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().IntVarP(&n, "limit", "n", 100, "how many records")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}

func newAuditVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Verify the entire hash chain (slow on large logs, but linear)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dial(true)
			if err != nil {
				return err
			}
			ok, msg, err := c.VerifyAudit(cmd.Context())
			if err != nil {
				return err
			}
			if ok {
				fmt.Println("audit chain ok")
				return nil
			}
			return fmt.Errorf("audit chain broken: %s", msg)
		},
	}
}
