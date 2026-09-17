package main

import (
	"context"
	"testing"

	"github.com/aniklavida/words-on-the-street/internal/app"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/mcpserver"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestCLIAndMCPMatch(t *testing.T) {
	store := &evidence.MemoryStore{}
	a := &app.App{Store: store}

	ctx := context.Background()
	url := "https://example.com"
	backend := "echo"
	args := []string{"hello"}

	// 1. "CLI" path - simulate what main() does
	cliRec, err := a.Fetch(ctx, backend, args, url, "1.0", false)
	if err != nil {
		t.Fatalf("CLI path failed: %v", err)
	}
	cliOutput := string(cliRec.Payload)

	// 2. "MCP" path - simulate invoking the MCP tool
	handler := mcpserver.FetchToolHandler(a)
	
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "fetch",
			Arguments: map[string]interface{}{
				"url":     url,
				"backend": backend,
				"args":    []interface{}{"hello"},
			},
		},
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("MCP path failed with error: %v", err)
	}
	if result.IsError {
		t.Fatalf("MCP path failed with tool error")
	}

	mcpOutput := ""
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			mcpOutput += textContent.Text
		}
	}

	if cliOutput != mcpOutput {
		t.Errorf("Mismatch!\nCLI output: %q\nMCP output: %q", cliOutput, mcpOutput)
	}
}
