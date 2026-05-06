package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shuakami/hush/internal/clientlib"
	"github.com/spf13/cobra"
)

func newExecCmd() *cobra.Command {
	var (
		tag      string
		jsonOut  bool
		timeout  time.Duration
		parallel int
		quiet    bool
	)
	cmd := &cobra.Command{
		Use:   "exec [HOST] -- COMMAND...",
		Short: "Run a command on a host (or every host with a tag)",
		Long: `exec runs COMMAND on the named host. With --tag, it runs in parallel
on every host carrying that tag.

By default stdout / stderr are piped through verbatim and the local exit code
mirrors the remote command's exit code. With --json, a structured result is
printed instead.

Recommended agent flow:
  1. Run "hush doctor --host NAME".
  2. Run a read-only smoke command such as "hostname && whoami".
  3. Run write commands only after the user has approved that class of change.`,
		Example: `  hush exec NAME -- "hostname && whoami && uname -a"
  hush exec NAME -- "systemctl status app.service"
  hush exec --tag prod --parallel 5 --json -- "df -h /"`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			host, command, err := splitExecArgs(cmd, args)
			if err != nil {
				return err
			}
			c, err := dial(true)
			if err != nil {
				return err
			}
			to := int(timeout.Seconds())
			if tag != "" {
				results, err := c.ExecMulti(cmd.Context(), clientlib.ExecRequest{
					Selector: tag, Command: command, TimeoutSec: to, Parallel: parallel,
				})
				if err != nil {
					return err
				}
				return printMulti(results, jsonOut, quiet)
			}
			res, err := c.Exec(cmd.Context(), clientlib.ExecRequest{
				Host: host, Command: command, TimeoutSec: to,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			if !quiet {
				_, _ = os.Stdout.Write(res.Stdout)
				_, _ = os.Stderr.Write(res.Stderr)
			} else {
				_, _ = os.Stdout.Write(res.Stdout)
			}
			os.Exit(res.ExitCode)
			return nil
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "run on every host carrying this tag (instead of a single host)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "structured JSON output instead of raw stdout/stderr")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "exec timeout (e.g. 30s, 5m). 0 = server default")
	cmd.Flags().IntVar(&parallel, "parallel", 0, "max concurrent hosts when --tag is used (0 = no limit)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "suppress remote stderr (rarely useful, but pipes love it)")
	return cmd
}

// splitExecArgs accepts "hush exec NAME -- ls /" and "hush exec NAME ls /" and
// "hush exec --tag hk -- ls /". Cobra strips the literal `--` for us.
func splitExecArgs(cmd *cobra.Command, args []string) (host, command string, err error) {
	tag, _ := cmd.Flags().GetString("tag")
	if tag != "" {
		if len(args) == 0 {
			return "", "", fmt.Errorf("missing command")
		}
		return "", strings.Join(args, " "), nil
	}
	if len(args) < 2 {
		return "", "", fmt.Errorf("usage: hush exec HOST -- COMMAND ...")
	}
	return args[0], strings.Join(args[1:], " "), nil
}

func printMulti(results []clientlib.MultiResult, jsonOut, quiet bool) error {
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	worstExit := 0
	for _, r := range results {
		header := fmt.Sprintf("─── %s ───", r.Host)
		fmt.Fprintln(os.Stderr, header)
		if r.Error != "" {
			fmt.Fprintf(os.Stderr, "  ERROR: %s\n", r.Error)
			worstExit = 1
			continue
		}
		if r.Result == nil {
			fmt.Fprintln(os.Stderr, "  (no result)")
			continue
		}
		_, _ = os.Stdout.Write(r.Result.Stdout)
		if !quiet {
			_, _ = os.Stderr.Write(r.Result.Stderr)
		}
		if r.Result.ExitCode != 0 {
			fmt.Fprintf(os.Stderr, "  exit=%d\n", r.Result.ExitCode)
			if r.Result.ExitCode > worstExit {
				worstExit = r.Result.ExitCode
			}
		}
	}
	if worstExit != 0 {
		os.Exit(worstExit)
	}
	return nil
}
