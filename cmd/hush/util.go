package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/shuakami/hush/internal/clientlib"
	"github.com/shuakami/hush/internal/config"
)

// dial sets up an authenticated client from env / config file / flags.
func dial(needsAuth bool) (*clientlib.Client, error) {
	cfg := config.LoadClient()
	if err := cfg.Validate(needsAuth); err != nil {
		return nil, err
	}
	return clientlib.New(cfg.Endpoint, cfg.APIKey), nil
}

// contextWithTimeout returns a child of os.Interrupt-aware context.Background()
// with the given seconds, since cmd_server.go can't import the root context.
func contextWithTimeout(seconds int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
}

// dieIf prints err to stderr and exits 1. Used for terminal exit branches.
func dieIf(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// splitHostPath splits "host:/path" into ("host", "/path"). If the input does
// not contain a colon, returns ("", input).
func splitHostPath(s string) (host, path string) {
	if i := strings.Index(s, ":"); i > 0 {
		if isWindowsDrivePath(s, i) {
			return "", s
		}
		return s[:i], s[i+1:]
	}
	return "", s
}

func isWindowsDrivePath(s string, colon int) bool {
	if colon != 1 || len(s) < 3 {
		return false
	}
	c := s[0]
	if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
		return false
	}
	return s[2] == '\\' || s[2] == '/'
}

// commaList splits "a,b,c" into a slice, trimming whitespace.
func commaList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// truthy returns true for "1", "true", "yes" (case-insensitive).
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// clientEndpointFromListen converts a server "listen" address (e.g.
// "0.0.0.0:8443", ":8443", "[::]:8443") into a usable client endpoint URL
// rooted at localhost. The server may listen on a wildcard, but a CLI on
// the same machine still has to dial 127.0.0.1.
func clientEndpointFromListen(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// errorMsg unwraps an *clientlib.ErrAPI to its message, or stringifies err.
func errorMsg(err error) string {
	var apiErr *clientlib.ErrAPI
	if errors.As(err, &apiErr) {
		return apiErr.Msg
	}
	if err == nil {
		return ""
	}
	return err.Error()
}
