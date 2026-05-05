package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/shuakami/hush/internal/inventory"
)

// sdjzRelayTransport drives the ssh.sdjz.wiki/c/<TOKEN> protocol.
//
// The protocol exposes three verbs:
//
//	curl -sSL https://ssh.sdjz.wiki/c/<TOKEN> | sh -s -- "<cmd string>"
//	curl -sSL https://ssh.sdjz.wiki/c/<TOKEN> | sh -s -- put ./local /remote
//	curl -sSL https://ssh.sdjz.wiki/c/<TOKEN> | sh -s -- get /remote ./local
//
// Hush server resolves the token from the vault at request time; it is never
// surfaced to the caller. We invoke `curl | sh -s --` via os/exec rather than
// re-implementing the (private) remote protocol.
type sdjzRelayTransport struct {
	token    string
	relayURL string
}

func openSDJZRelay(ctx context.Context, h *inventory.Host, r Resolver) (Transport, error) {
	if h.RelaySecret == "" {
		return nil, errors.New("sdjz-relay host has no relay_secret reference")
	}
	tok, err := r.GetSecret(ctx, h.RelaySecret)
	if err != nil {
		return nil, fmt.Errorf("fetch relay token from vault: %w", err)
	}
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return nil, errors.New("relay token is empty")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		return nil, errors.New("sdjz-relay transport requires `curl` on the hush server PATH")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		return nil, errors.New("sdjz-relay transport requires `sh` on the hush server PATH")
	}
	return &sdjzRelayTransport{
		token:    tok,
		relayURL: relayURL(h),
	}, nil
}

func relayURL(h *inventory.Host) string {
	if v, ok := h.Metadata["relay_url"]; ok && v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://ssh.sdjz.wiki"
}

// Exec runs `<cmd>` on the relay-targeted host.
func (t *sdjzRelayTransport) Exec(ctx context.Context, command string, opts ExecOptions) (*ExecResult, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pipeline := fmt.Sprintf(`curl -sSL %s | sh -s -- %s`,
		shellQuote(fmt.Sprintf("%s/c/%s", t.relayURL, t.token)),
		shellQuote(command))

	cmd := exec.CommandContext(runCtx, "sh", "-c", pipeline)
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("sdjz-relay exec timed out after %s", timeout)
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
}

// Put uploads a local file to the relay target. Because the sdjz-relay
// protocol's `put` takes a local path, we stage the stream into a temp file
// first and pass that to curl|sh.
func (t *sdjzRelayTransport) Put(ctx context.Context, src io.Reader, dst string, mode os.FileMode) error {
	tmp, err := os.CreateTemp("", "hush-relay-put-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	pipeline := fmt.Sprintf(`curl -sSL %s | sh -s -- put %s %s`,
		shellQuote(fmt.Sprintf("%s/c/%s", t.relayURL, t.token)),
		shellQuote(tmpName),
		shellQuote(dst))
	cmd := exec.CommandContext(ctx, "sh", "-c", pipeline)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sdjz-relay put failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	if mode != 0 {
		// Best-effort chmod on remote (not all relay images support put-with-mode).
		chmodPipeline := fmt.Sprintf(`curl -sSL %s | sh -s -- %s`,
			shellQuote(fmt.Sprintf("%s/c/%s", t.relayURL, t.token)),
			shellQuote(fmt.Sprintf("chmod %o %s", mode.Perm(), shellEscapeForRemote(dst))))
		_ = exec.CommandContext(ctx, "sh", "-c", chmodPipeline).Run()
	}
	return nil
}

// Get downloads a remote file out to `dst`. Mirrors Put: stage to temp, then copy.
func (t *sdjzRelayTransport) Get(ctx context.Context, src string, dst io.Writer) error {
	tmp, err := os.CreateTemp("", "hush-relay-get-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	defer os.Remove(tmpName)

	pipeline := fmt.Sprintf(`curl -sSL %s | sh -s -- get %s %s`,
		shellQuote(fmt.Sprintf("%s/c/%s", t.relayURL, t.token)),
		shellQuote(src),
		shellQuote(tmpName))
	cmd := exec.CommandContext(ctx, "sh", "-c", pipeline)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sdjz-relay get failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	f, err := os.Open(filepath.Clean(tmpName))
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(dst, f); err != nil {
		return err
	}
	return nil
}

// Close is a no-op for the relay; each call is a fresh shell-out.
func (t *sdjzRelayTransport) Close() error { return nil }

// shellQuote wraps s in single quotes safe for /bin/sh -c.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellEscapeForRemote handles a much simpler case: characters likely safe in
// remote sh contexts. Since the remote shell is unknown (could be bash on Linux
// or cmd on Windows under the relay), we conservatively wrap with double
// quotes and escape doubles + backticks + dollars + backslashes.
func shellEscapeForRemote(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"`", "\\`",
		`$`, `\$`,
	)
	return `"` + r.Replace(s) + `"`
}
