package main

import (
	"bufio"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCLIAndMCPMatch_RealBinary(t *testing.T) {
	// Build the real binary
	tmpDir := t.TempDir()
	// Windows refuses to exec a path with no extension, so the built binary
	// must carry .exe there. Without it the test fails with "executable file
	// not found in %PATH%" while pointing at a file that plainly exists.
	binName := "words-on-the-street"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(tmpDir, binName)

	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}

	url := "https://example.com"
	backend := "echo"

	// CLI path
	cliCmd := exec.Command(binPath, "fetch", url, backend, "hello", "world")
	cliOut, err := cliCmd.Output()
	if err != nil {
		t.Fatalf("CLI failed: %v", err)
	}
	cliOutput := string(cliOut)

	// MCP path
	mcpCmd := exec.Command(binPath, "mcp")
	stdin, err := mcpCmd.StdinPipe()
	if err != nil {
		t.Fatalf("Failed to get stdin pipe: %v", err)
	}
	stdout, err := mcpCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to get stdout pipe: %v", err)
	}

	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("Failed to start MCP server: %v", err)
	}

	// Send initialization request
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}` + "\n"
	if _, err := stdin.Write([]byte(initReq)); err != nil {
		t.Fatalf("Failed to write init req: %v", err)
	}

	scanner := bufio.NewScanner(stdout)

	// Read init response
	if !scanner.Scan() {
		t.Fatalf("Failed to read init response")
	}

	// Send initialized notification
	initNotif := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	if _, err := stdin.Write([]byte(initNotif)); err != nil {
		t.Fatalf("Failed to write initialized notif: %v", err)
	}

	// Send tool call request
	reqStr := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fetch","arguments":{"url":"https://example.com","backend":"echo","args":["hello","world"]}}}` + "\n"
	if _, err := stdin.Write([]byte(reqStr)); err != nil {
		t.Fatalf("Failed to write tool call req: %v", err)
	}

	if !scanner.Scan() {
		t.Fatalf("Failed to read tool call response")
	}

	respBytes := scanner.Bytes()

	// Close stdin to terminate the server
	stdin.Close()
	mcpCmd.Wait()

	// Parse JSON-RPC response
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("Failed to parse MCP response %q: %v", string(respBytes), err)
	}

	if resp.Error != nil {
		t.Fatalf("MCP returned error: %v", resp.Error.Message)
	}

	if resp.Result.IsError {
		t.Fatalf("MCP tool returned error")
	}

	mcpOutput := ""
	for _, c := range resp.Result.Content {
		mcpOutput += c.Text
	}

	if cliOutput != mcpOutput {
		t.Errorf("Mismatch!\nCLI output (len=%d): %q\nMCP output (len=%d): %q", len(cliOutput), cliOutput, len(mcpOutput), mcpOutput)
	}
}
