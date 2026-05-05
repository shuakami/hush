package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/shuakami/hush/internal/inventory"
	"golang.org/x/crypto/ssh"
)

// sshTransport wraps a single golang.org/x/crypto/ssh.Client.
//
// If the host record specifies jump_via, the client is dialed *through* the
// jump host's SSH transport (recursively), giving free ProxyJump-style
// chaining without writing any chain logic at the caller.
type sshTransport struct {
	client *ssh.Client

	// parent is non-nil when this transport is reached through a jump host.
	// We must close it after our own client to avoid leaking the chain.
	parent Transport
}

func openSSH(ctx context.Context, h *inventory.Host, r Resolver) (Transport, error) {
	authMethods, cleanup, err := buildSSHAuth(ctx, h, r)
	if err != nil {
		return nil, err
	}

	cfg := &ssh.ClientConfig{
		User:            h.SSHUser,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // v0.2: pin host keys
		Timeout:         20 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", h.Address, h.Port)

	if h.JumpVia != "" {
		jumpHost, err := r.GetHost(ctx, h.JumpVia)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("resolve jump host %q: %w", h.JumpVia, err)
		}
		jumpT, err := Open(ctx, jumpHost, r)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("open jump host %q: %w", h.JumpVia, err)
		}
		jumpSSH, ok := jumpT.(*sshTransport)
		if !ok {
			_ = jumpT.Close()
			cleanup()
			return nil, fmt.Errorf("jump host %q must be an ssh transport, got %s", h.JumpVia, jumpHost.Transport)
		}

		conn, err := jumpSSH.client.DialContext(ctx, "tcp", addr)
		if err != nil {
			_ = jumpT.Close()
			cleanup()
			return nil, fmt.Errorf("dial through jump host: %w", err)
		}
		clientConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
		if err != nil {
			_ = conn.Close()
			_ = jumpT.Close()
			cleanup()
			return nil, fmt.Errorf("ssh handshake through jump: %w", err)
		}
		client := ssh.NewClient(clientConn, chans, reqs)
		cleanup()
		return &sshTransport{client: client, parent: jumpT}, nil
	}

	dialer := &net.Dialer{Timeout: 20 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		cleanup()
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	cleanup()
	return &sshTransport{client: ssh.NewClient(clientConn, chans, reqs)}, nil
}

func buildSSHAuth(ctx context.Context, h *inventory.Host, r Resolver) ([]ssh.AuthMethod, func(), error) {
	cleanup := func() {}
	switch h.AuthKind {
	case inventory.AuthKindPassword:
		secret, err := r.GetSecret(ctx, h.AuthSecret)
		if err != nil {
			return nil, cleanup, fmt.Errorf("fetch password secret %q: %w", h.AuthSecret, err)
		}
		return []ssh.AuthMethod{ssh.Password(secret)}, cleanup, nil
	case inventory.AuthKindKey:
		secret, err := r.GetSecret(ctx, h.AuthSecret)
		if err != nil {
			return nil, cleanup, fmt.Errorf("fetch key secret %q: %w", h.AuthSecret, err)
		}
		signer, err := ssh.ParsePrivateKey([]byte(secret))
		if err != nil {
			// Try with passphrase, hint the user.
			return nil, cleanup, fmt.Errorf("parse private key: %w (passphrase-protected keys not yet supported in v0.1)", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, cleanup, nil
	default:
		return nil, cleanup, fmt.Errorf("unsupported auth kind %q", h.AuthKind)
	}
}

// Exec runs a single command on the remote.
func (t *sshTransport) Exec(ctx context.Context, command string, opts ExecOptions) (*ExecResult, error) {
	sess, err := t.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	if opts.Stdin != nil {
		sess.Stdin = opts.Stdin
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- sess.Run(command) }()

	select {
	case err := <-done:
		dur := time.Since(start)
		exit := 0
		if err != nil {
			var ee *ssh.ExitError
			if errors.As(err, &ee) {
				exit = ee.ExitStatus()
			} else {
				return nil, err
			}
		}
		return &ExecResult{
			ExitCode: exit,
			Stdout:   stdout.Bytes(),
			Stderr:   stderr.Bytes(),
			Duration: dur,
		}, nil
	case <-runCtx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return nil, fmt.Errorf("exec timed out after %s", timeout)
	}
}

// Put streams src into a remote file.
func (t *sshTransport) Put(ctx context.Context, src io.Reader, dst string, mode os.FileMode) error {
	c, err := sftp.NewClient(t.client)
	if err != nil {
		return fmt.Errorf("new sftp: %w", err)
	}
	defer c.Close()

	dst = strings.TrimSpace(dst)
	if dst == "" {
		return errors.New("empty dst path")
	}

	f, err := c.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, src); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if mode != 0 {
		if err := c.Chmod(dst, mode); err != nil {
			return fmt.Errorf("chmod: %w", err)
		}
	}
	return nil
}

// Get streams a remote file out to dst.
func (t *sshTransport) Get(ctx context.Context, src string, dst io.Writer) error {
	c, err := sftp.NewClient(t.client)
	if err != nil {
		return fmt.Errorf("new sftp: %w", err)
	}
	defer c.Close()
	f, err := c.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer f.Close()
	if _, err := io.Copy(dst, f); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// Close tears down the SSH client (and any parent jump chain).
func (t *sshTransport) Close() error {
	var firstErr error
	if t.client != nil {
		if err := t.client.Close(); err != nil {
			firstErr = err
		}
	}
	if t.parent != nil {
		if err := t.parent.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
