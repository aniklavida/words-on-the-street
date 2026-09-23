package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/aniklavida/words-on-the-street/internal/app"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/mark3labs/mcp-go/mcp"
)

// The value below is an obviously-fake placeholder, never a real credential.
const testAgentCookie = "fake-agent-facing-cookie-not-real-9876543210"

// cannedStore reports the record it was given, whatever fetch ran. It lets the
// test drive the agent-facing boundary without the record boundary refusing the
// payload first, so the two surfaces are asserted separately.
type cannedStore struct{ rec *evidence.Record }

func (c *cannedStore) Save(*evidence.Record) error { return nil }

func (c *cannedStore) Get(string) (*evidence.Record, error) { return c.rec, nil }

// Done when: 2. The agent-facing surface -- the bytes an MCP caller receives --
// carries no registered credential, even if the payload it was handed does.
func TestMCPFetchTool_RedactsRegisteredSecretFromAgentOutput(t *testing.T) {
	evidence.ResetSecrets()
	t.Cleanup(evidence.ResetSecrets)
	evidence.RegisterSecret(testAgentCookie)

	a := &app.App{Store: &cannedStore{rec: &evidence.Record{
		Payload: []byte("the source echoed " + testAgentCookie + " back"),
	}}}

	handler := FetchToolHandler(a)
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "fetch",
		Arguments: map[string]interface{}{
			"url":     "https://example.com",
			"backend": "echo",
		},
	}}

	result, err := handler(context.Background(), request)
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	text := ""
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			text += textContent.Text
		}
	}
	if strings.Contains(text, testAgentCookie) {
		t.Fatalf("agent output leaked the credential: %q", text)
	}
	if !strings.Contains(text, evidence.RedactionPlaceholder) {
		t.Errorf("agent output did not show the redaction placeholder: %q", text)
	}
}

func TestMCPFetchTool_PassesRoutingOverride(t *testing.T) {
	store := evidence.NewMemoryStore()
	a := &app.App{Store: store}

	handler := FetchToolHandler(a)
	overrideReason := "User directed MCP agent to use twitter instead of technical forum"
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "fetch",
		Arguments: map[string]interface{}{
			"url":                     "https://example.com/mcp-override",
			"backend":                 "echo",
			"routing_override":        true,
			"routing_override_reason": overrideReason,
		},
	}}

	result, err := handler(context.Background(), request)
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected successful tool call, got error")
	}

	records, err := store.List()
	if err != nil {
		t.Fatalf("store.List failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record in store, got %d", len(records))
	}

	rec := records[0]
	if !rec.RoutingOverride {
		t.Errorf("expected rec.RoutingOverride true, got false")
	}
	if rec.RoutingOverrideReason != overrideReason {
		t.Errorf("expected rec.RoutingOverrideReason %q, got %q", overrideReason, rec.RoutingOverrideReason)
	}
}
