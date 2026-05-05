// Package clientlib is the lightweight HTTP client used by the CLI and the
// Python SDK shim. It is deliberately tiny — no retries, no fancy auth — and
// returns raw structures matching the server's JSON.
package clientlib

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shuakami/hush/internal/audit"
	"github.com/shuakami/hush/internal/inventory"
	"github.com/shuakami/hush/internal/transport"
	"github.com/shuakami/hush/internal/vault"
)

// ErrAPI wraps a non-2xx response.
type ErrAPI struct {
	Status int
	Msg    string
}

// Error implements error.
func (e *ErrAPI) Error() string { return fmt.Sprintf("hush server returned %d: %s", e.Status, e.Msg) }

// Client talks to a Hush server over HTTP.
type Client struct {
	Endpoint string
	APIKey   string
	HTTP     *http.Client
}

// New returns a client with sane defaults.
func New(endpoint, apiKey string) *Client {
	return &Client{
		Endpoint: strings.TrimRight(endpoint, "/"),
		APIKey:   apiKey,
		HTTP:     &http.Client{Timeout: 5 * time.Minute},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Endpoint+path, rdr)
	if err != nil {
		return err
	}
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return &ErrAPI{Status: resp.StatusCode, Msg: msg}
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ---- secrets ---------------------------------------------------------------

// GetSecret returns plaintext.
func (c *Client) GetSecret(ctx context.Context, name string) (string, error) {
	var out struct{ Value string }
	if err := c.do(ctx, "GET", "/api/v1/secrets/"+url.PathEscape(name), nil, &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

// PutSecret upserts a secret.
func (c *Client) PutSecret(ctx context.Context, name, value string, metadata map[string]string) (*vault.Secret, error) {
	var out vault.Secret
	body := map[string]interface{}{
		"value":    value,
		"metadata": metadata,
	}
	if err := c.do(ctx, "PUT", "/api/v1/secrets/"+url.PathEscape(name), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSecrets returns metadata for every secret.
func (c *Client) ListSecrets(ctx context.Context) ([]vault.Secret, error) {
	var out struct {
		Secrets []vault.Secret
	}
	if err := c.do(ctx, "GET", "/api/v1/secrets", nil, &out); err != nil {
		return nil, err
	}
	return out.Secrets, nil
}

// DeleteSecret removes a secret.
func (c *Client) DeleteSecret(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", "/api/v1/secrets/"+url.PathEscape(name), nil, nil)
}

// ---- hosts -----------------------------------------------------------------

// PutHost upserts a host record.
func (c *Client) PutHost(ctx context.Context, h *inventory.Host) error {
	return c.do(ctx, "PUT", "/api/v1/hosts/"+url.PathEscape(h.Name), h, nil)
}

// GetHost fetches a single host record.
func (c *Client) GetHost(ctx context.Context, name string) (*inventory.Host, error) {
	var out inventory.Host
	if err := c.do(ctx, "GET", "/api/v1/hosts/"+url.PathEscape(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListHosts returns all hosts (optionally filtered by tag).
func (c *Client) ListHosts(ctx context.Context, tag string) ([]inventory.Host, error) {
	path := "/api/v1/hosts"
	if tag != "" {
		path += "?tag=" + url.QueryEscape(tag)
	}
	var out struct {
		Hosts []inventory.Host
	}
	if err := c.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out.Hosts, nil
}

// DeleteHost removes a host.
func (c *Client) DeleteHost(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", "/api/v1/hosts/"+url.PathEscape(name), nil, nil)
}

// ---- exec ------------------------------------------------------------------

// ExecRequest is the body of POST /api/v1/exec.
type ExecRequest struct {
	Host       string `json:"host,omitempty"`
	Selector   string `json:"selector,omitempty"`
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
	Parallel   int    `json:"parallel,omitempty"`
}

// Exec runs a command on one host.
func (c *Client) Exec(ctx context.Context, req ExecRequest) (*transport.ExecResult, error) {
	var out transport.ExecResult
	if err := c.do(ctx, "POST", "/api/v1/exec", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ExecMulti runs a command on every host matching the selector.
func (c *Client) ExecMulti(ctx context.Context, req ExecRequest) ([]MultiResult, error) {
	var out struct {
		Results []MultiResult
	}
	if err := c.do(ctx, "POST", "/api/v1/exec/multi", req, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// MultiResult is one host's exec outcome.
type MultiResult struct {
	Host   string                `json:"host"`
	Result *transport.ExecResult `json:"result,omitempty"`
	Error  string                `json:"error,omitempty"`
}

// ---- file transfer ---------------------------------------------------------

// Put streams `src` into `dst` on the named host.
func (c *Client) Put(ctx context.Context, host, dst string, src io.Reader, mode int) error {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	errCh := make(chan error, 1)

	go func() {
		var err error
		defer func() {
			_ = mw.Close()
			_ = pw.CloseWithError(err)
			errCh <- err
		}()
		if err = mw.WriteField("host", host); err != nil {
			return
		}
		if err = mw.WriteField("dst", dst); err != nil {
			return
		}
		if mode != 0 {
			if err = mw.WriteField("mode", strconv.FormatInt(int64(mode), 8)); err != nil {
				return
			}
		}
		var fw io.Writer
		fw, err = mw.CreateFormFile("file", "upload")
		if err != nil {
			return
		}
		_, err = io.Copy(fw, src)
	}()

	req, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint+"/api/v1/files/put", pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := <-errCh; err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return &ErrAPI{Status: resp.StatusCode, Msg: strings.TrimSpace(string(raw))}
	}
	return nil
}

// Get streams a remote file out to the writer.
func (c *Client) Get(ctx context.Context, host, src string, dst io.Writer) error {
	body, _ := json.Marshal(map[string]string{"host": host, "src": src})
	req, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint+"/api/v1/files/get", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return &ErrAPI{Status: resp.StatusCode, Msg: strings.TrimSpace(string(raw))}
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		return err
	}
	return nil
}

// ---- audit -----------------------------------------------------------------

// TailAudit returns the most recent N audit records.
func (c *Client) TailAudit(ctx context.Context, limit int) ([]audit.Record, error) {
	path := "/api/v1/audit"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out struct {
		Records []audit.Record
	}
	if err := c.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out.Records, nil
}

// VerifyAudit asks the server to walk the entire chain.
func (c *Client) VerifyAudit(ctx context.Context) (bool, string, error) {
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := c.do(ctx, "GET", "/api/v1/audit/verify", nil, &out); err != nil {
		return false, "", err
	}
	return out.OK, out.Error, nil
}

// ---- api keys --------------------------------------------------------------

// MintAPIKeyResponse is what the server returns from POST /api-keys.
type MintAPIKeyResponse struct {
	Key  string      `json:"key"`
	Meta interface{} `json:"meta"`
}

// MintAPIKey creates a new API key (admin scope required).
func (c *Client) MintAPIKey(ctx context.Context, name string, scopes []string) (*MintAPIKeyResponse, error) {
	var out MintAPIKeyResponse
	body := map[string]interface{}{"name": name, "scopes": scopes}
	if err := c.do(ctx, "POST", "/api/v1/api-keys", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool {
	var e *ErrAPI
	if errors.As(err, &e) {
		return e.Status == http.StatusNotFound
	}
	return false
}
