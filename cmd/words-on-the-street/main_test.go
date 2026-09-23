package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCLIHelperProcess is re-executed by the built CLI as a portable fixture
// backend. It serves the bytes held in CLI_FIXTURE_FILE, so the CLI can be run
// end to end without touching the network and without a shell script that would
// break on Windows.
func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CLI_HELPER") != "1" {
		return
	}
	for _, arg := range os.Args {
		if arg == "--version" {
			fmt.Println("cli-fixture version 1.0.0")
			os.Exit(0)
		}
	}

	path := os.Getenv("CLI_FIXTURE_FILE")
	if path == "" {
		fmt.Fprintln(os.Stderr, "CLI_FIXTURE_FILE is not set")
		os.Exit(2)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Stdout.Write(data)
	os.Exit(0)
}

// Done when: 3. The CLI command runs end to end against fixture backends through
// the real registry and store — this invokes the built binary, not internals.
func TestCLI_FetchSourceEndToEnd_FixtureBackends(t *testing.T) {
	tmpDir := t.TempDir()

	binName := "words-on-the-street"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(tmpDir, binName)

	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("failed to build binary: %v\n%s", err, out)
	}

	exe, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("failed to resolve test binary path: %v", err)
	}

	backendDoc := func() map[string]any {
		return map[string]any{
			"name":          exe,
			"command":       exe,
			"args":          []string{"-test.run=^TestCLIHelperProcess$", "--"},
			"version_args":  []string{"-test.run=^TestCLIHelperProcess$", "--", "--version"},
			"version_range": ">= 1.0.0",
			"licence":       "MIT",
		}
	}
	regDoc := map[string][]map[string]any{
		"hacker-news": {backendDoc()},
		"lobsters":    {backendDoc()},
	}
	regBytes, err := json.Marshal(regDoc)
	if err != nil {
		t.Fatalf("failed to marshal registry: %v", err)
	}
	regPath := filepath.Join(tmpDir, "registry.json")
	if err := os.WriteFile(regPath, regBytes, 0o644); err != nil {
		t.Fatalf("failed to write registry: %v", err)
	}

	cases := []struct {
		source  string
		payload []byte
	}{
		{"hacker-news", []byte(`{"source":"hacker-news","stories":[1,2]}`)},
		{"lobsters", []byte(`{"source":"lobsters","stories":[3,4]}`)},
	}

	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			sourceDir := filepath.Join(tmpDir, tc.source)
			if err := os.MkdirAll(sourceDir, 0o755); err != nil {
				t.Fatalf("failed to create source dir: %v", err)
			}
			fixturePath := filepath.Join(sourceDir, "fixture.json")
			if err := os.WriteFile(fixturePath, tc.payload, 0o644); err != nil {
				t.Fatalf("failed to write fixture: %v", err)
			}
			storeDir := filepath.Join(sourceDir, "store")

			cmd := exec.Command(binPath, "fetch", tc.source, "golang")
			cmd.Env = append(os.Environ(),
				"GO_WANT_CLI_HELPER=1",
				"CLI_FIXTURE_FILE="+fixturePath,
				"WORDS_ON_THE_STREET_REGISTRY="+regPath,
				"WORDS_ON_THE_STREET_STORE="+storeDir,
			)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("CLI fetch failed: %v\nstderr: %s", err, stderr.String())
			}
			if !bytes.Equal(stdout.Bytes(), tc.payload) {
				t.Fatalf("CLI did not return the fetched bytes:\ngot:  %q\nwant: %q", stdout.Bytes(), tc.payload)
			}
		})
	}
}

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
