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
	"strings"
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

func TestCLI_WatchReportsEditedContentAndExits(t *testing.T) {
	binPath := buildCLIBinary(t)
	tmpDir := t.TempDir()
	original := []byte("original post content\n")
	updated := []byte("updated post content\n")
	fixturePath := writeCLIFixture(t, tmpDir, original)
	regPath := writeCLIRegistry(t, tmpDir, map[string][]map[string]any{
		"hacker-news": {cliHelperBackend(t, "fixture")},
	})
	storeDir := filepath.Join(tmpDir, "store")
	env := cliEnv(regPath, storeDir, fixturePath)
	if _, _, err := runCLI(t, binPath, env, "fetch", "hacker-news", "golang"); err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}
	if err := os.WriteFile(fixturePath, updated, 0o644); err != nil {
		t.Fatalf("failed to mutate fixture: %v", err)
	}

	stdout, stderr, err := runCLI(t, binPath, env, "watch", "hacker-news", "golang")
	if err != nil {
		t.Fatalf("watch failed: %v\nstderr: %s", err, stderr)
	}
	for _, want := range []string{"edited: 1", "previous_content:", string(original), "current_content:", string(updated)} {
		if !strings.Contains(string(stdout), want) {
			t.Errorf("watch output does not contain %q:\n%s", want, stdout)
		}
	}
}

// buildCLIBinary compiles the real command into a temporary directory so the
// tests below exercise the binary end to end rather than internal functions.
func buildCLIBinary(t *testing.T) string {
	t.Helper()
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
	return binPath
}

// cliHelperBackend describes a registry backend whose command is this test
// binary re-executed as TestCLIHelperProcess. It is portable: no shell script,
// and it runs identically on Linux, macOS, and Windows.
func cliHelperBackend(t *testing.T, name string) map[string]any {
	t.Helper()
	exe, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("failed to resolve test binary path: %v", err)
	}
	return map[string]any{
		"name":          name,
		"command":       exe,
		"args":          []string{"-test.run=^TestCLIHelperProcess$", "--"},
		"version_args":  []string{"-test.run=^TestCLIHelperProcess$", "--", "--version"},
		"version_range": ">= 1.0.0",
		"licence":       "MIT",
	}
}

// cliMissingBackend describes a backend whose executable does not exist, so its
// health check fails without touching the network or the filesystem.
func cliMissingBackend(name, command string) map[string]any {
	return map[string]any{
		"name":          name,
		"command":       command,
		"version_range": ">= 1.0.0",
		"licence":       "MIT",
	}
}

func writeCLIRegistry(t *testing.T, dir string, doc map[string][]map[string]any) string {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("failed to marshal registry: %v", err)
	}
	path := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("failed to write registry: %v", err)
	}
	return path
}

func writeCLIFixture(t *testing.T, dir string, payload []byte) string {
	t.Helper()
	path := filepath.Join(dir, "fixture.json")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	return path
}

// cliEnv builds a subprocess environment that points the binary at a fixture
// registry and a throwaway store. GO_WANT_CLI_HELPER is always set so a helper
// backend answers a --version health check with a version in range.
func cliEnv(regPath, storeDir, fixturePath string) []string {
	return append(os.Environ(),
		"GO_WANT_CLI_HELPER=1",
		"CLI_FIXTURE_FILE="+fixturePath,
		"WORDS_ON_THE_STREET_REGISTRY="+regPath,
		"WORDS_ON_THE_STREET_STORE="+storeDir,
	)
}

func runCLI(t *testing.T, binPath string, env []string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// Done when: 1. A fixture where the primary backend fails and a fallback
// succeeds: fetch exits 0, stdout carries the fetched bytes unchanged, and
// stderr names the skipped backend and the one that served the bytes.
func TestCLI_FetchFallbackWarnsOnStderrAndKeepsStdoutClean(t *testing.T) {
	binPath := buildCLIBinary(t)
	tmpDir := t.TempDir()

	payload := []byte(`{"source":"hacker-news","stories":[1,2]}`)
	fixturePath := writeCLIFixture(t, tmpDir, payload)
	regPath := writeCLIRegistry(t, tmpDir, map[string][]map[string]any{
		"hacker-news": {
			cliMissingBackend("gh", filepath.Join(tmpDir, "no-such-primary-tool")),
			cliHelperBackend(t, "curl"),
		},
	})
	storeDir := filepath.Join(tmpDir, "store")

	stdout, stderr, err := runCLI(t, binPath, cliEnv(regPath, storeDir, fixturePath), "fetch", "hacker-news", "golang")
	if err != nil {
		t.Fatalf("degraded-but-successful fetch must exit 0, got: %v\nstderr: %s", err, stderr)
	}
	if !bytes.Equal(stdout, payload) {
		t.Fatalf("stdout must be exactly the fetched bytes:\ngot:  %q\nwant: %q", stdout, payload)
	}
	if !strings.Contains(string(stderr), "⚠") {
		t.Errorf("expected a degradation warning on stderr, got: %s", stderr)
	}
	for _, want := range []string{"hacker-news", "gh", "curl"} {
		if !strings.Contains(string(stderr), want) {
			t.Errorf("warning must name %q, got stderr: %s", want, stderr)
		}
	}
}

// Done when: 2. A fixture where every backend is healthy: no warning on stderr.
func TestCLI_FetchHealthyPrimaryEmitsNoWarning(t *testing.T) {
	binPath := buildCLIBinary(t)
	tmpDir := t.TempDir()

	payload := []byte(`{"source":"hacker-news","stories":[5,6]}`)
	fixturePath := writeCLIFixture(t, tmpDir, payload)
	regPath := writeCLIRegistry(t, tmpDir, map[string][]map[string]any{
		"hacker-news": {cliHelperBackend(t, "curl")},
	})
	storeDir := filepath.Join(tmpDir, "store")

	stdout, stderr, err := runCLI(t, binPath, cliEnv(regPath, storeDir, fixturePath), "fetch", "hacker-news", "golang")
	if err != nil {
		t.Fatalf("healthy fetch must exit 0, got: %v\nstderr: %s", err, stderr)
	}
	if !bytes.Equal(stdout, payload) {
		t.Fatalf("stdout must be exactly the fetched bytes:\ngot:  %q\nwant: %q", stdout, payload)
	}
	if strings.Contains(string(stderr), "⚠") || strings.Contains(string(stderr), "fallback") {
		t.Errorf("healthy primary fetch must not warn, got stderr: %s", stderr)
	}
}

// Done when: 3. The status surface reports a source whose primary is down but
// which has a working fallback as degraded, distinct from one where every
// backend is down. This runs live health checks through the real binary.
func TestCLI_StatusDistinguishesDegradedFromDown(t *testing.T) {
	binPath := buildCLIBinary(t)
	tmpDir := t.TempDir()

	fixturePath := writeCLIFixture(t, tmpDir, []byte(`{}`))
	missing := func(name string) string { return filepath.Join(tmpDir, "no-such-"+name) }
	regPath := writeCLIRegistry(t, tmpDir, map[string][]map[string]any{
		"degraded-source": {
			cliMissingBackend("primary-down", missing("primary-down")),
			cliHelperBackend(t, "fallback-up"),
		},
		"down-source": {
			cliMissingBackend("first-down", missing("first-down")),
			cliMissingBackend("second-down", missing("second-down")),
		},
		"healthy-source": {cliHelperBackend(t, "primary-up")},
	})
	storeDir := filepath.Join(tmpDir, "store")

	stdout, stderr, _ := runCLI(t, binPath, cliEnv(regPath, storeDir, fixturePath), "status")
	out := string(stdout)
	for _, want := range []string{
		"source: degraded-source, status: degraded",
		"source: down-source, status: down",
		"source: healthy-source, status: healthy",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q; got:\n%s\nstderr: %s", want, out, stderr)
		}
	}
	if !strings.Contains(out, "fallback-up") {
		t.Errorf("degraded line must name the serving fallback; got:\n%s", out)
	}
}

// Done when: 4. A fixture where every backend for a source fails: the error
// names every attempted backend and its own failure reason, not just the last.
func TestCLI_FetchAllBackendsFailNamesEveryAttempt(t *testing.T) {
	binPath := buildCLIBinary(t)
	tmpDir := t.TempDir()

	missing := func(name string) string { return filepath.Join(tmpDir, "no-such-"+name) }
	regPath := writeCLIRegistry(t, tmpDir, map[string][]map[string]any{
		"hacker-news": {
			cliMissingBackend("missing-first", missing("first")),
			cliMissingBackend("missing-second", missing("second")),
		},
	})
	storeDir := filepath.Join(tmpDir, "store")

	stdout, stderr, err := runCLI(t, binPath, cliEnv(regPath, storeDir, writeCLIFixture(t, tmpDir, []byte(`{}`))), "fetch", "hacker-news", "golang")
	if err == nil {
		t.Fatalf("all backends failing must exit non-zero; stdout: %s", stdout)
	}
	combined := string(stdout) + string(stderr)
	for _, want := range []string{
		"missing-first (unreachable)",
		"missing-second (unreachable)",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("failure must report %q, got:\n%s", want, combined)
		}
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

// Done when: 3. The ban-risk disclosure is plain text at the CLI, at the point
// the user learns how to configure the session cookie, and it names what a
// cookie is and what the platform does to accounts that use one this way.
func TestCLI_ConfigureTwitterShowsBanRiskDisclosure(t *testing.T) {
	binPath := buildCLIBinary(t)

	stdout, stderr, err := runCLI(t, binPath, os.Environ(), "configure", "twitter")
	if err != nil {
		t.Fatalf("configure twitter must exit 0, got: %v\nstderr: %s", err, stderr)
	}

	// The disclosure is wrapped for a terminal, so compare on collapsed
	// whitespace; the wording is what matters, not where the line breaks fall.
	output := strings.Join(strings.Fields(string(stdout)+string(stderr)), " ")
	for _, want := range []string{
		"against Twitter/X's terms",
		"without the password",
		"past two-factor authentication",
		"bans accounts permanently and without warning",
		"separate account",
		"WORDS_ON_THE_STREET_TWITTER_COOKIE",
		"never written to an evidence record",
		"WORDS_ON_THE_STREET_TWITTER_RATE_LIMIT",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("disclosure must contain %q; got:\n%s", want, output)
		}
	}
}

// Done when: 2 and 4. Run the real binary end to end for the twitter source
// with a fake cookie. The cookie is sent to the backend, yet it appears nowhere
// in stdout, stderr, or any file the evidence store wrote, and the fetched
// bytes still reach stdout unchanged.
func TestCLI_TwitterCookieNeverAppearsInOutputOrStore(t *testing.T) {
	binPath := buildCLIBinary(t)
	tmpDir := t.TempDir()

	// An obviously-fake placeholder, never a real credential.
	const cookie = "fake-cli-twitter-cookie-not-real-1234567890"

	payload := []byte(`{"tweet":"the bytes the backend served"}`)
	fixturePath := writeCLIFixture(t, tmpDir, payload)
	regPath := writeCLIRegistry(t, tmpDir, map[string][]map[string]any{
		"twitter": {cliHelperBackend(t, "curl")},
	})
	storeDir := filepath.Join(tmpDir, "store")

	env := append(cliEnv(regPath, storeDir, fixturePath),
		"WORDS_ON_THE_STREET_TWITTER_COOKIE="+cookie,
		"WORDS_ON_THE_STREET_TWITTER_RATE_LIMIT=60000",
	)

	stdout, stderr, err := runCLI(t, binPath, env, "fetch", "twitter", "golang")
	if err != nil {
		t.Fatalf("fetch twitter failed: %v\nstderr: %s", err, stderr)
	}
	if !bytes.Equal(stdout, payload) {
		t.Fatalf("stdout must be exactly the fetched bytes:\ngot:  %q\nwant: %q", stdout, payload)
	}
	if strings.Contains(string(stdout)+string(stderr), cookie) {
		t.Fatalf("CLI output leaked the cookie:\nstdout: %s\nstderr: %s", stdout, stderr)
	}

	scanned := 0
	if err := filepath.Walk(storeDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++
		if bytes.Contains(data, []byte(cookie)) {
			t.Errorf("store file %s contains the cookie", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("error scanning store: %v", err)
	}
	if scanned == 0 {
		t.Fatal("no evidence files were scanned")
	}
}

// Done when: 4. A user-overridden source selection is recorded and distinguishable from a routing-skill-recommended one in the evidence record — proven by a test.
func TestCLI_FetchRoutingOverrideRecorded(t *testing.T) {
	binPath := buildCLIBinary(t)
	tmpDir := t.TempDir()

	payload := []byte(`{"source":"hacker-news","items":[42]}`)
	fixturePath := writeCLIFixture(t, tmpDir, payload)
	regPath := writeCLIRegistry(t, tmpDir, map[string][]map[string]any{
		"hacker-news": {cliHelperBackend(t, "curl")},
	})
	storeDir := filepath.Join(tmpDir, "store")
	env := cliEnv(regPath, storeDir, fixturePath)

	overrideReason := "User chose specific source against routing model recommendation"
	stdout, stderr, err := runCLI(t, binPath, env, "fetch", "--override", "--override-reason", overrideReason, "hacker-news", "golang")
	if err != nil {
		t.Fatalf("fetch with override failed: %v\nstderr: %s", err, stderr)
	}

	if !bytes.Equal(stdout, payload) {
		t.Fatalf("stdout mismatch:\ngot:  %q\nwant: %q", stdout, payload)
	}

	stderrStr := string(stderr)
	if !strings.Contains(stderrStr, "override: true") {
		t.Errorf("stderr must report override, got: %s", stderrStr)
	}
	if !strings.Contains(stderrStr, overrideReason) {
		t.Errorf("stderr must report override reason %q, got: %s", overrideReason, stderrStr)
	}

	// Verify the written evidence record on disk
	recordLine := ""
	for _, line := range strings.Split(stderrStr, "\n") {
		if strings.HasPrefix(line, "record: ") {
			recordLine = strings.TrimPrefix(line, "record: ")
			break
		}
	}
	if recordLine == "" {
		t.Fatalf("stderr did not output record hash: %s", stderrStr)
	}

	recFile := filepath.Join(storeDir, "records", recordLine+".json")
	data, err := os.ReadFile(recFile)
	if err != nil {
		t.Fatalf("failed to read record file %s: %v", recFile, err)
	}

	var rec struct {
		RoutingOverride       bool   `json:"routing_override"`
		RoutingOverrideReason string `json:"routing_override_reason"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("failed to unmarshal record JSON: %v", err)
	}

	if !rec.RoutingOverride {
		t.Errorf("expected record on disk to have routing_override true, got false")
	}
	if rec.RoutingOverrideReason != overrideReason {
		t.Errorf("expected record on disk to have routing_override_reason %q, got %q", overrideReason, rec.RoutingOverrideReason)
	}
}

// Done when: 1. A cookie stored in the keychain is read and used for a fetch,
// without ever needing the environment variable set.
// Uses a fake/mock keychain backend file for CI, since a real OS keychain isn't
// scriptable in an automated test.
func TestCLI_TwitterFetchUsesKeychainCookieWithoutEnvironmentVariable(t *testing.T) {
	binPath := buildCLIBinary(t)
	tmpDir := t.TempDir()
	const fakeCookie = "fake-twitter-cli-keychain-cookie-12345"

	payload := []byte(`{"tweet":"served via keychain in CLI"}`)
	fixturePath := writeCLIFixture(t, tmpDir, payload)
	regPath := writeCLIRegistry(t, tmpDir, map[string][]map[string]any{
		"twitter": {cliHelperBackend(t, "curl")},
	})
	storeDir := filepath.Join(tmpDir, "store")
	keychainFile := filepath.Join(tmpDir, "keychain.json")

	// Store cookie into keychain using configure twitter --store
	envStore := append(os.Environ(), "WORDS_ON_THE_STREET_TEST_KEYCHAIN="+keychainFile)
	stdout, stderr, err := runCLI(t, binPath, envStore, "configure", "twitter", "--store", fakeCookie)
	if err != nil {
		t.Fatalf("configure twitter --store failed: %v\nstderr: %s", err, stderr)
	}
	if strings.Contains(string(stdout)+string(stderr), fakeCookie) {
		t.Errorf("store command echoed cookie")
	}

	// Fetch without WORDS_ON_THE_STREET_TWITTER_COOKIE set in env
	envFetch := append(cliEnv(regPath, storeDir, fixturePath),
		"WORDS_ON_THE_STREET_TEST_KEYCHAIN="+keychainFile,
		"WORDS_ON_THE_STREET_TWITTER_RATE_LIMIT=60000",
	)

	stdout, stderr, err = runCLI(t, binPath, envFetch, "fetch", "twitter", "golang")
	if err != nil {
		t.Fatalf("fetch twitter with keychain failed: %v\nstderr: %s", err, stderr)
	}
	if !bytes.Equal(stdout, payload) {
		t.Fatalf("stdout must be exactly the fetched bytes:\ngot:  %q\nwant: %q", stdout, payload)
	}
	for _, out := range []string{string(stdout), string(stderr)} {
		if strings.Contains(out, fakeCookie) {
			t.Errorf("CLI output contains the cookie")
		}
	}
}

// Done when: 3. The keychain-storage command never prints the cookie value back to
// stdout/stderr after storing it.
func TestCLI_ConfigureTwitterStoreNeverEchoesCookie(t *testing.T) {
	binPath := buildCLIBinary(t)
	tmpDir := t.TempDir()
	const fakeCookie = "secret-twitter-cookie-never-echo"
	keychainFile := filepath.Join(tmpDir, "keychain.json")

	env := append(os.Environ(), "WORDS_ON_THE_STREET_TEST_KEYCHAIN="+keychainFile)
	stdout, stderr, err := runCLI(t, binPath, env, "configure", "twitter", "--store", fakeCookie)
	if err != nil {
		t.Fatalf("configure twitter --store failed: %v\nstderr: %s", err, stderr)
	}

	combined := string(stdout) + string(stderr)
	if strings.Contains(combined, fakeCookie) {
		t.Fatalf("keychain-storage command printed the cookie back to output:\n%s", combined)
	}
	if !strings.Contains(combined, "Successfully stored twitter session cookie in OS keychain.") {
		t.Errorf("expected success message, got:\n%s", combined)
	}
}
