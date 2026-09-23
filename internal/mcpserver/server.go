package mcpserver

import (
	"context"
	"fmt"

	"github.com/aniklavida/words-on-the-street/internal/app"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Exported so we can test the handler without firing up the transport
func FetchToolHandler(a *app.App) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		argsMap, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid arguments map")
		}

		url, ok := argsMap["url"].(string)
		if !ok {
			return nil, fmt.Errorf("url is required")
		}
		backend, ok := argsMap["backend"].(string)
		if !ok {
			return nil, fmt.Errorf("backend is required")
		}

		var args []string
		if argsRaw, ok := argsMap["args"].([]interface{}); ok {
			for _, arg := range argsRaw {
				if s, ok := arg.(string); ok {
					args = append(args, s)
				}
			}
		}

		hash, err := a.Fetch(ctx, backend, args, url, "1.0", false)
		if err != nil {
			return mcp.NewToolResultError(evidence.Redact(err.Error())), nil
		}

		rec, err := a.Store.Get(hash)
		if err != nil {
			return mcp.NewToolResultError(evidence.Redact(fmt.Sprintf("evidence discarded: %v", err))), nil
		}

		// Nothing reaches an agent through a registered credential. The bytes
		// are scrubbed of any registered secret before this returns, so a
		// session cookie cannot ride out inside the payload it fetched.
		return mcp.NewToolResultText(evidence.Redact(string(rec.Payload))), nil
	}
}

func NewServer(a *app.App) *server.MCPServer {
	s := server.NewMCPServer("words-on-the-street", "1.0.0")

	tool := mcp.NewTool("fetch",
		mcp.WithDescription("Fetch a URL using a backend"),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL to fetch")),
		mcp.WithString("backend", mcp.Required(), mcp.Description("Backend to use")),
	)

	s.AddTool(tool, FetchToolHandler(a))

	return s
}
