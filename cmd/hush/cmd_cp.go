package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newCPCmd() *cobra.Command {
	var mode string
	cmd := &cobra.Command{
		Use:   "cp SRC DST",
		Short: "Copy a file to/from a host (drop-in for `scp local host:/path`)",
		Long: `cp uses the host:/remote prefix exactly like scp. One side must be a host
reference, the other side must be a local path.

  hush cp ./worker hk1:/opt/uapipro/worker
  hush cp hk1:/var/log/proxy.log ./local.log`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			srcHost, srcPath := splitHostPath(args[0])
			dstHost, dstPath := splitHostPath(args[1])
			if srcHost != "" && dstHost != "" {
				return errors.New("host-to-host copies are not supported in v0.1")
			}
			if srcHost == "" && dstHost == "" {
				return errors.New("at least one side must be HOST:/path")
			}
			c, err := dial(true)
			if err != nil {
				return err
			}
			if srcHost == "" {
				// upload local -> host
				f, err := os.Open(srcPath)
				if err != nil {
					return err
				}
				defer f.Close()
				m := 0o644
				if mode != "" {
					_, err := fmt.Sscanf(mode, "%o", &m)
					if err != nil {
						return fmt.Errorf("invalid --mode %q: %w", mode, err)
					}
				}
				if err := c.Put(cmd.Context(), dstHost, dstPath, f, m); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "uploaded %s -> %s:%s\n", srcPath, dstHost, dstPath)
				return nil
			}
			// download host -> local
			out, err := os.Create(dstPath)
			if err != nil {
				return err
			}
			defer out.Close()
			if err := c.Get(cmd.Context(), srcHost, srcPath, out); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "downloaded %s:%s -> %s\n", srcHost, srcPath, dstPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "octal file mode for uploads (e.g. 0755)")
	return cmd
}
