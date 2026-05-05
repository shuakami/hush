// Package transport defines a uniform interface for "talk to a remote host"
// regardless of the underlying mechanism (SSH, jump chain, sdjz-relay, ...).
//
// All concrete transports take a Resolver that maps host names to records and
// secret references to plaintext, so credentials live in the vault and never
// leak into transport implementations.
package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/shuakami/hush/internal/inventory"
)

// Resolver is what a transport uses to fetch the data it needs from the
// surrounding services (inventory + vault). Decoupling is intentional so that
// transports can be unit-tested with fakes.
type Resolver interface {
	GetHost(ctx context.Context, name string) (*inventory.Host, error)
	GetSecret(ctx context.Context, name string) (string, error)
}

// ExecOptions customises a single Exec call.
type ExecOptions struct {
	Stdin   io.Reader
	Timeout time.Duration
}

// ExecResult is what we report back after running a command.
type ExecResult struct {
	ExitCode int           `json:"exit_code"`
	Stdout   []byte        `json:"stdout"`
	Stderr   []byte        `json:"stderr"`
	Duration time.Duration `json:"duration_ms"`
}

// Transport is a connection to one host.
type Transport interface {
	// Exec runs a command on the remote, returning its full output.
	Exec(ctx context.Context, cmd string, opts ExecOptions) (*ExecResult, error)

	// Put copies a stream into a remote file.
	Put(ctx context.Context, src io.Reader, dst string, mode os.FileMode) error

	// Get streams a remote file out to the writer.
	Get(ctx context.Context, src string, dst io.Writer) error

	// Close releases any underlying connection.
	Close() error
}

// Open dials the host using whatever transport its record specifies.
// The returned Transport must be Close()d by the caller.
func Open(ctx context.Context, h *inventory.Host, r Resolver) (Transport, error) {
	switch h.Transport {
	case inventory.TransportSSH:
		return openSSH(ctx, h, r)
	case inventory.TransportSDJZRelay:
		return openSDJZRelay(ctx, h, r)
	default:
		return nil, fmt.Errorf("unsupported transport %q for host %q", h.Transport, h.Name)
	}
}
