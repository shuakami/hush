package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/shuakami/hush/internal/clientlib"
	"github.com/shuakami/hush/internal/config"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var host string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check local config, server health, auth, inventory, and optional host connectivity",
		Long: `doctor is the first command to run when Hush does not behave as expected.

Without flags it checks the client config, server /healthz endpoint, API auth,
and inventory access. With --host it also verifies that the host record exists
and can execute a minimal remote "true" command.

doctor prints endpoint and inventory counts, but never prints API keys or vault
secret values. Fix the first failing row before running deploy, restart, or
other write commands.`,
		Example: `  hush doctor
  hush doctor --host NAME
  hush doctor --host NAME --timeout 20s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				timeout = 10 * time.Second
			}
			return runDoctor(cmd, host, timeout)
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "also run a minimal remote connectivity check against this host")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "timeout for each check")
	return cmd
}

func runDoctor(cmd *cobra.Command, host string, timeout time.Duration) error {
	cfg := config.LoadClient()
	c := clientlib.New(cfg.Endpoint, cfg.APIKey)
	c.HTTP = &http.Client{Timeout: timeout}

	checks := []doctorCheck{
		checkConfig(cfg),
		checkHealth(cmd, c, timeout),
		checkAuth(cmd, c, timeout),
		checkInventory(cmd, c, timeout),
	}
	if host != "" {
		checks = append(checks, checkHost(cmd, c, host, timeout))
	}

	printDoctor(checks)
	for _, check := range checks {
		if check.Status == "FAIL" {
			return errors.New("doctor found failing checks")
		}
	}
	return nil
}

type doctorCheck struct {
	Name   string
	Status string
	Detail string
	Next   string
}

func checkConfig(cfg *config.ClientConfig) doctorCheck {
	if cfg.Endpoint == "" {
		return doctorCheck{
			Name:   "config",
			Status: "FAIL",
			Detail: "endpoint is empty",
			Next:   "run `hush bootstrap` or set HUSH_ENDPOINT",
		}
	}
	if cfg.APIKey == "" {
		return doctorCheck{
			Name:   "config",
			Status: "FAIL",
			Detail: fmt.Sprintf("endpoint=%s; api key missing", cfg.Endpoint),
			Next:   "run `hush bootstrap` or set HUSH_API_KEY",
		}
	}
	return doctorCheck{
		Name:   "config",
		Status: "OK",
		Detail: fmt.Sprintf("endpoint=%s; api key configured", cfg.Endpoint),
	}
}

func checkHealth(cmd *cobra.Command, c *clientlib.Client, timeout time.Duration) doctorCheck {
	ctx, cancel := contextWithTimeoutDuration(cmd, timeout)
	defer cancel()
	if err := c.Health(ctx); err != nil {
		return doctorCheck{
			Name:   "server",
			Status: "FAIL",
			Detail: friendlyErr(err),
			Next:   "start `hush server`, or check HUSH_ENDPOINT",
		}
	}
	return doctorCheck{Name: "server", Status: "OK", Detail: "healthz ok"}
}

func checkAuth(cmd *cobra.Command, c *clientlib.Client, timeout time.Duration) doctorCheck {
	ctx, cancel := contextWithTimeoutDuration(cmd, timeout)
	defer cancel()
	k, err := c.Whoami(ctx)
	if err != nil {
		return doctorCheck{
			Name:   "auth",
			Status: "FAIL",
			Detail: friendlyErr(err),
			Next:   "run `hush bootstrap`, or replace the API key with `hush login --endpoint ... --api-key ...`",
		}
	}
	return doctorCheck{
		Name:   "auth",
		Status: "OK",
		Detail: fmt.Sprintf("key=%s scopes=%s", k.Name, strings.Join(k.Scopes, ",")),
	}
}

func checkInventory(cmd *cobra.Command, c *clientlib.Client, timeout time.Duration) doctorCheck {
	ctx, cancel := contextWithTimeoutDuration(cmd, timeout)
	defer cancel()
	hosts, hErr := c.ListHosts(ctx, "")
	if hErr != nil {
		return doctorCheck{
			Name:   "inventory",
			Status: "FAIL",
			Detail: "hosts: " + friendlyErr(hErr),
			Next:   "ensure the API key has host:list scope",
		}
	}
	ctx, cancel = contextWithTimeoutDuration(cmd, timeout)
	defer cancel()
	secrets, sErr := c.ListSecrets(ctx)
	if sErr != nil {
		return doctorCheck{
			Name:   "inventory",
			Status: "WARN",
			Detail: fmt.Sprintf("hosts=%d; secrets unavailable: %s", len(hosts), friendlyErr(sErr)),
			Next:   "grant secret:list scope if secret inventory checks are needed",
		}
	}
	return doctorCheck{
		Name:   "inventory",
		Status: "OK",
		Detail: fmt.Sprintf("hosts=%d secrets=%d", len(hosts), len(secrets)),
	}
}

func checkHost(cmd *cobra.Command, c *clientlib.Client, host string, timeout time.Duration) doctorCheck {
	ctx, cancel := contextWithTimeoutDuration(cmd, timeout)
	defer cancel()
	if _, err := c.GetHost(ctx, host); err != nil {
		return doctorCheck{
			Name:   "host:" + host,
			Status: "FAIL",
			Detail: friendlyErr(err),
			Next:   "register it with `hush host add`, then store the referenced secret with `hush secret set`",
		}
	}
	ctx, cancel = contextWithTimeoutDuration(cmd, timeout)
	defer cancel()
	res, err := c.Exec(ctx, clientlib.ExecRequest{
		Host:       host,
		Command:    "true",
		TimeoutSec: int(timeout.Seconds()),
	})
	if err != nil {
		return doctorCheck{
			Name:   "host:" + host,
			Status: "FAIL",
			Detail: friendlyErr(err),
			Next:   "check address, port, username, auth kind, and the host's referenced secret",
		}
	}
	if res.ExitCode != 0 {
		detail := fmt.Sprintf("remote `true` exited %d", res.ExitCode)
		if msg := strings.TrimSpace(string(res.Stderr)); msg != "" {
			detail += ": " + trimForDoctor(msg)
		} else if msg := strings.TrimSpace(string(res.Stdout)); msg != "" {
			detail += ": " + trimForDoctor(msg)
		}
		return doctorCheck{
			Name:   "host:" + host,
			Status: "FAIL",
			Detail: detail,
			Next:   "try `hush exec " + host + " -- \"hostname && whoami\"` for more detail",
		}
	}
	return doctorCheck{Name: "host:" + host, Status: "OK", Detail: "remote exec ok"}
}

func printDoctor(checks []doctorCheck) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CHECK\tSTATUS\tDETAIL\tNEXT")
	for _, check := range checks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", check.Name, check.Status, check.Detail, check.Next)
	}
	_ = tw.Flush()
}

func friendlyErr(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *clientlib.ErrAPI
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized:
			return "unauthorized: API key missing, invalid, or revoked"
		case http.StatusForbidden:
			return "forbidden: " + apiErr.Msg
		case http.StatusNotFound:
			return apiErr.Msg
		case http.StatusBadGateway:
			return "transport failed: " + apiErr.Msg
		default:
			return apiErr.Error()
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout: " + err.Error()
	}
	return err.Error()
}

func trimForDoctor(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 160
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func contextWithTimeoutDuration(cmd *cobra.Command, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := cmd.Context().Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	return context.WithTimeout(cmd.Context(), timeout)
}
