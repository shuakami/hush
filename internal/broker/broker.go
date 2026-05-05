// Package broker glues vault + inventory + transport + audit together.
//
// Every code path that runs a command, copies a file, or reads a secret on
// behalf of a caller goes through here. That makes the audit log
// authoritative: if it didn't pass through Broker, it didn't happen.
package broker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/shuakami/hush/internal/audit"
	"github.com/shuakami/hush/internal/inventory"
	"github.com/shuakami/hush/internal/transport"
	"github.com/shuakami/hush/internal/vault"
	"golang.org/x/sync/errgroup"
)

// Broker is the central coordinator. Construct it once per server.
type Broker struct {
	Inv   *inventory.Manager
	Vault *vault.Vault
	Audit *audit.Logger
}

// New constructs a broker.
func New(inv *inventory.Manager, v *vault.Vault, a *audit.Logger) *Broker {
	return &Broker{Inv: inv, Vault: v, Audit: a}
}

// ---- transport.Resolver implementation ------------------------------------

// GetHost satisfies transport.Resolver.
func (b *Broker) GetHost(ctx context.Context, name string) (*inventory.Host, error) {
	return b.Inv.Get(ctx, name)
}

// GetSecret satisfies transport.Resolver. It retrieves plaintext from the vault.
func (b *Broker) GetSecret(ctx context.Context, name string) (string, error) {
	pt, _, err := b.Vault.Get(ctx, name)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// ---- exec / put / get -----------------------------------------------------

// ExecOptions is the user-facing options struct for Exec.
type ExecOptions struct {
	Stdin   io.Reader
	Timeout time.Duration
}

// HostResult bundles per-host output for multi-host exec.
type HostResult struct {
	Host   string                `json:"host"`
	Result *transport.ExecResult `json:"result,omitempty"`
	Error  string                `json:"error,omitempty"`
}

// Exec runs `command` on a single host. If selector matches a tag instead of
// a host name, ExecMulti should be used.
func (b *Broker) Exec(ctx context.Context, actor, hostName, command string, opts ExecOptions) (*transport.ExecResult, error) {
	h, err := b.Inv.Get(ctx, hostName)
	if err != nil {
		return nil, err
	}
	t, err := transport.Open(ctx, h, b)
	if err != nil {
		_, _ = b.Audit.Append(ctx, actor, "host.exec.failed", h.Name, map[string]interface{}{
			"command_preview": preview(command),
			"error":           err.Error(),
		})
		return nil, err
	}
	defer t.Close()

	res, err := t.Exec(ctx, command, transport.ExecOptions{
		Stdin:   opts.Stdin,
		Timeout: opts.Timeout,
	})
	if err != nil {
		_, _ = b.Audit.Append(ctx, actor, "host.exec.failed", h.Name, map[string]interface{}{
			"command_preview": preview(command),
			"error":           err.Error(),
		})
		return nil, err
	}
	_, _ = b.Audit.Append(ctx, actor, "host.exec", h.Name, map[string]interface{}{
		"command_preview": preview(command),
		"exit_code":       res.ExitCode,
		"stdout_bytes":    len(res.Stdout),
		"stderr_bytes":    len(res.Stderr),
		"duration_ms":     res.Duration.Milliseconds(),
	})
	return res, nil
}

// ExecMulti runs `command` on every host matching `selector` (an exact host
// name or a tag). Hosts run concurrently up to `parallel` goroutines (0 = no
// limit).
func (b *Broker) ExecMulti(ctx context.Context, actor, selector, command string, parallel int, opts ExecOptions) ([]HostResult, error) {
	hosts, err := b.Inv.ResolveTag(ctx, selector)
	if err != nil {
		return nil, err
	}
	out := make([]HostResult, len(hosts))
	g, gctx := errgroup.WithContext(ctx)
	if parallel > 0 {
		g.SetLimit(parallel)
	}
	var mu sync.Mutex
	for i, h := range hosts {
		i, h := i, h
		g.Go(func() error {
			res, err := b.Exec(gctx, actor, h.Name, command, opts)
			mu.Lock()
			defer mu.Unlock()
			out[i] = HostResult{Host: h.Name, Result: res}
			if err != nil {
				out[i].Error = err.Error()
			}
			return nil // never abort siblings
		})
	}
	_ = g.Wait()
	return out, nil
}

// Put streams `src` into `dst` on the named host.
func (b *Broker) Put(ctx context.Context, actor, hostName string, src io.Reader, dst string, mode os.FileMode) error {
	h, err := b.Inv.Get(ctx, hostName)
	if err != nil {
		return err
	}
	t, err := transport.Open(ctx, h, b)
	if err != nil {
		return err
	}
	defer t.Close()

	if err := t.Put(ctx, src, dst, mode); err != nil {
		_, _ = b.Audit.Append(ctx, actor, "host.put.failed", h.Name, map[string]interface{}{
			"dst":   dst,
			"error": err.Error(),
		})
		return err
	}
	_, _ = b.Audit.Append(ctx, actor, "host.put", h.Name, map[string]interface{}{
		"dst": dst,
	})
	return nil
}

// Get streams a remote file out to `dst`.
func (b *Broker) Get(ctx context.Context, actor, hostName, src string, dst io.Writer) error {
	h, err := b.Inv.Get(ctx, hostName)
	if err != nil {
		return err
	}
	t, err := transport.Open(ctx, h, b)
	if err != nil {
		return err
	}
	defer t.Close()

	if err := t.Get(ctx, src, dst); err != nil {
		_, _ = b.Audit.Append(ctx, actor, "host.get.failed", h.Name, map[string]interface{}{
			"src":   src,
			"error": err.Error(),
		})
		return err
	}
	_, _ = b.Audit.Append(ctx, actor, "host.get", h.Name, map[string]interface{}{
		"src": src,
	})
	return nil
}

// ---- secret access (audited) ---------------------------------------------

// SecretGet returns plaintext for a secret name, with audit. Use this whenever
// a secret leaves the server boundary so we have a record.
func (b *Broker) SecretGet(ctx context.Context, actor, name string) (string, error) {
	pt, meta, err := b.Vault.Get(ctx, name)
	if err != nil {
		return "", err
	}
	_, _ = b.Audit.Append(ctx, actor, "secret.get", name, map[string]interface{}{
		"version": meta.Version,
	})
	return string(pt), nil
}

// SecretPut creates or replaces a secret.
func (b *Broker) SecretPut(ctx context.Context, actor, name, value string, metadata map[string]string) (*vault.Secret, error) {
	if name == "" {
		return nil, errors.New("secret name required")
	}
	s, err := b.Vault.Put(ctx, name, []byte(value), metadata)
	if err != nil {
		return nil, err
	}
	_, _ = b.Audit.Append(ctx, actor, "secret.put", name, map[string]interface{}{
		"version": s.Version,
	})
	return s, nil
}

// preview shortens a command for audit so the log isn't dominated by huge
// blobs. We never log secrets — that's the caller's responsibility (don't put
// secrets in the command body!).
func preview(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("…(+%d bytes)", len(s)-max)
}
