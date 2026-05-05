// Package mcpserver wraps the Hush HTTP API as a stdio MCP server.
//
// Intentionally thin: every tool resolves to one or two HTTP calls. The
// headline UX is the CLI; MCP is here for clients that prefer JSON-RPC.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/shuakami/hush/internal/clientlib"
)

// Serve starts the MCP server on stdio and blocks until ctx is done or
// the client disconnects.
func Serve(ctx context.Context, c *clientlib.Client) error {
	s := server.NewMCPServer("hush", "0.1.0")

	s.AddTool(
		mcp.NewTool("host_list",
			mcp.WithDescription("List registered hosts (no credentials returned). Optional tag filter."),
			mcp.WithString("tag",
				mcp.Description("Filter by tag (returns only hosts carrying this tag)."),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tag, _ := req.Params.Arguments["tag"].(string)
			hosts, err := c.ListHosts(ctx, tag)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			body, _ := json.Marshal(map[string]interface{}{"hosts": hosts})
			return mcp.NewToolResultText(string(body)), nil
		})

	s.AddTool(
		mcp.NewTool("host_exec",
			mcp.WithDescription("Run a shell command on one named host. Returns stdout/stderr/exit_code. Credentials never leave the server."),
			mcp.WithString("host", mcp.Required(), mcp.Description("Host name (e.g. 'hk1').")),
			mcp.WithString("command", mcp.Required(), mcp.Description("Shell command line.")),
			mcp.WithNumber("timeout_sec", mcp.Description("Timeout in seconds. Default 300.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			host, ok := req.Params.Arguments["host"].(string)
			if !ok || host == "" {
				return mcp.NewToolResultError("host required"), nil
			}
			command, ok := req.Params.Arguments["command"].(string)
			if !ok || command == "" {
				return mcp.NewToolResultError("command required"), nil
			}
			to, _ := req.Params.Arguments["timeout_sec"].(float64)
			res, err := c.Exec(ctx, clientlib.ExecRequest{
				Host: host, Command: command, TimeoutSec: int(to),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			body, _ := json.Marshal(res)
			return mcp.NewToolResultText(string(body)), nil
		})

	s.AddTool(
		mcp.NewTool("host_exec_multi",
			mcp.WithDescription("Run a shell command on every host matching a tag. Returns one result per host."),
			mcp.WithString("selector", mcp.Required(), mcp.Description("Tag (e.g. 'hk') or exact host name.")),
			mcp.WithString("command", mcp.Required(), mcp.Description("Shell command line.")),
			mcp.WithNumber("timeout_sec", mcp.Description("Per-host timeout in seconds.")),
			mcp.WithNumber("parallel", mcp.Description("Max concurrent hosts. 0 = no limit.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			sel, ok := req.Params.Arguments["selector"].(string)
			if !ok || sel == "" {
				return mcp.NewToolResultError("selector required"), nil
			}
			command, ok := req.Params.Arguments["command"].(string)
			if !ok || command == "" {
				return mcp.NewToolResultError("command required"), nil
			}
			to, _ := req.Params.Arguments["timeout_sec"].(float64)
			par, _ := req.Params.Arguments["parallel"].(float64)
			results, err := c.ExecMulti(ctx, clientlib.ExecRequest{
				Selector: sel, Command: command, TimeoutSec: int(to), Parallel: int(par),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			body, _ := json.Marshal(map[string]interface{}{"results": results})
			return mcp.NewToolResultText(string(body)), nil
		})

	s.AddTool(
		mcp.NewTool("secret_get",
			mcp.WithDescription("Fetch a secret value from the vault. Audited. Requires secret:read scope on the API key."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Secret name.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, ok := req.Params.Arguments["name"].(string)
			if !ok || name == "" {
				return mcp.NewToolResultError("name required"), nil
			}
			val, err := c.GetSecret(ctx, name)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(val), nil
		})

	s.AddTool(
		mcp.NewTool("audit_tail",
			mcp.WithDescription("Tail the most recent audit records."),
			mcp.WithNumber("limit", mcp.Description("Number of records (default 100).")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			limit, _ := req.Params.Arguments["limit"].(float64)
			recs, err := c.TailAudit(ctx, int(limit))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			body, _ := json.Marshal(map[string]interface{}{"records": recs})
			return mcp.NewToolResultText(string(body)), nil
		})

	if err := server.ServeStdio(s); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("mcp stdio: %w", err)
	}
	return nil
}
