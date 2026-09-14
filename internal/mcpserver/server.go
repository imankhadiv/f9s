// Package mcpserver exposes f9s's tool registry as an MCP server over
// stdio, so any MCP client (Claude Code, Claude Desktop, Cursor, …) can
// operate a Flink cluster through f9s. Read tools are always available;
// mutating tools require --allow-writes and a context that is not read-only.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/tools"
)

func newServer(cfg *config.Config, contextName, version string, allowWrites bool) (*server.MCPServer, error) {
	name, cctx, err := cfg.Resolve(contextName)
	if err != nil {
		return nil, err
	}
	if allowWrites && cctx.ReadOnly {
		return nil, fmt.Errorf("context %q has readOnly: true; refusing --allow-writes", name)
	}
	cl := client.New(cctx)
	registry := tools.New(cl, name)

	s := server.NewMCPServer("f9s", version,
		server.WithInstructions(fmt.Sprintf(
			"Operate the Apache Flink cluster behind f9s context %q (%s). "+
				"Read tools are always available. Mutating tools (savepoint/stop/cancel) "+
				"require --allow-writes and a context that is not marked readOnly.",
			name, cctx.URL)))

	for _, t := range registry.List(allowWrites) {
		opts := []mcp.ToolOption{mcp.WithDescription(t.Description)}
		for _, p := range t.Params {
			po := []mcp.PropertyOption{mcp.Description(p.Description)}
			if p.Required {
				po = append(po, mcp.Required())
			}
			opts = append(opts, mcp.WithString(p.Name, po...))
		}
		tool := t // capture
		s.AddTool(mcp.NewTool(t.Name, opts...), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]string{}
			for _, p := range tool.Params {
				if p.Required {
					v, err := req.RequireString(p.Name)
					if err != nil {
						return mcp.NewToolResultError(err.Error()), nil
					}
					args[p.Name] = v
				} else {
					args[p.Name] = req.GetString(p.Name, "")
				}
			}
			out, err := registry.RunAudited(ctx, tool.Name, args, "mcp")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(out), nil
		})
	}

	return s, nil
}

// Run serves MCP over stdio until the client disconnects.
func Run(cfg *config.Config, contextName, version string, allowWrites bool) error {
	s, err := newServer(cfg, contextName, version, allowWrites)
	if err != nil {
		return err
	}
	return server.ServeStdio(s)
}
