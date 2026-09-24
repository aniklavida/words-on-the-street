package security

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aniklavida/words-on-the-street/internal/backend"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/fetch"
)

// The environment variables below drive the helper process. It is the same
// "re-execute this test binary as a fixture" pattern the fetch and CLI tests
// use, so nothing here needs a shell script and the tests behave identically on
// Linux, macOS and Windows.
const (
	securityHelperEnv   = "WOTS_SECURITY_HELPER"
	securityRecorderEnv = "WOTS_SECURITY_RECORDER"
	securityPayloadEnv  = "WOTS_SECURITY_PAYLOAD"
)

// recordedInvocation is one process the core executed, captured verbatim.
type recordedInvocation struct {
	Argv []string `json:"argv"`
}

// TestSecurityPostureHelperProcess stands in for a network backend. When the
// parent test re-executes the test binary with WOTS_SECURITY_HELPER=1, it
// appends its own argv to the recorder file, answers a --version probe, and
// prints a payload. It never opens a socket itself, so every outbound
// destination in a session is named by an argument the core chose.
func TestSecurityPostureHelperProcess(t *testing.T) {
	if os.Getenv(securityHelperEnv) != "1" {
		return
	}

	if rec := os.Getenv(securityRecorderEnv); rec != "" {
		f, err := os.OpenFile(rec, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		line, err := json.Marshal(recordedInvocation{Argv: os.Args})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		_ = f.Close()
	}

	for _, arg := range os.Args {
		if arg == "--version" {
			fmt.Println("security-fixture version 1.0.0")
			os.Exit(0)
		}
	}

	payload := os.Getenv(securityPayloadEnv)
	if payload == "" {
		payload = `{"ok":true}`
	}
	fmt.Print(payload)
	os.Exit(0)
}

// securityBackend describes a fixture backend whose command is this test binary
// re-executed as TestSecurityPostureHelperProcess.
func securityBackend(t *testing.T, name string) backend.Backend {
	t.Helper()
	exe, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("failed to resolve the test binary path: %v", err)
	}
	return backend.Backend{
		Name:         name,
		Command:      exe,
		Args:         []string{"-test.run=^TestSecurityPostureHelperProcess$", "--"},
		VersionArgs:  []string{"-test.run=^TestSecurityPostureHelperProcess$", "--", "--version"},
		VersionRange: ">= 1.0.0",
		Licence:      "MIT",
	}
}

// readInvocations returns every process invocation the helper recorded.
func readInvocations(t *testing.T, path string) []recordedInvocation {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("failed to read recorder file: %v", err)
	}
	var out []recordedInvocation
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var inv recordedInvocation
		if err := json.Unmarshal([]byte(line), &inv); err != nil {
			t.Fatalf("failed to parse recorded invocation %q: %v", line, err)
		}
		out = append(out, inv)
	}
	return out
}

// egressGuard is installed as http.DefaultTransport. The core is not supposed
// to make any direct HTTP request at all — fetching is delegated to an external
// backend — so any request that reaches it is an undeclared outbound call.
type egressGuard struct {
	mu       sync.Mutex
	requests []string
}

func (g *egressGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	g.mu.Lock()
	g.requests = append(g.requests, req.Method+" "+req.URL.String())
	g.mu.Unlock()
	return nil, fmt.Errorf("egress guard blocked an outbound request to %s", req.URL)
}

func (g *egressGuard) snapshot() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.requests))
	copy(out, g.requests)
	return out
}

// installEgressGuard points http.DefaultTransport at the guard for the duration
// of a test and returns it. http.Get, http.Post and http.DefaultClient all
// route through DefaultTransport, so a direct call added to the core reaches
// the guard and the test fails.
func installEgressGuard(t *testing.T) *egressGuard {
	t.Helper()
	guard := &egressGuard{}
	previous := http.DefaultTransport
	http.DefaultTransport = guard
	t.Cleanup(func() { http.DefaultTransport = previous })
	return guard
}

// urlHost returns the host of an argument that is an absolute http(s) URL.
func urlHost(arg string) (string, bool) {
	u, err := url.Parse(arg)
	if err != nil {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	if u.Host == "" {
		return "", false
	}
	return u.Host, true
}

// sampleQuery returns a query that resolves for every shipped source.
func sampleQuery(string) string { return "golang" }

// resolvableSources returns the names of the shipped sources that have a
// resolver, in a stable order.
func resolvableSources(t *testing.T) []string {
	t.Helper()
	srcs := fetch.Sources()
	names := make([]string, 0, len(srcs))
	for _, name := range []string{"hacker-news", "lobsters", "twitter", "linkedin"} {
		if _, ok := srcs[name]; !ok {
			t.Fatalf("shipped source %q has no resolver", name)
		}
		names = append(names, name)
	}
	return names
}

// configureSession sets the credentials and rate limits a full default session
// needs, and returns a recorder path.
func configureSession(t *testing.T) string {
	t.Helper()
	evidence.ResetSecrets()
	t.Cleanup(evidence.ResetSecrets)

	recorder := filepath.Join(t.TempDir(), "invocations.jsonl")
	t.Setenv(securityHelperEnv, "1")
	t.Setenv(securityRecorderEnv, recorder)
	t.Setenv(securityPayloadEnv, `{"bytes":"the session payload"}`)
	t.Setenv(fetch.TwitterRateLimitEnv, "60000")
	t.Setenv(fetch.LinkedInMinIntervalEnv, "0")
	t.Setenv(fetch.TwitterCookieEnv, "fake-twitter-session-cookie-not-real-0001")
	t.Setenv(fetch.LinkedInCookieEnv, "li_at=FAKE-linkedin-session-cookie-not-real-0002")
	return recorder
}

// Done when: 1. A default run opens no connection outside the configured
// sources.
//
// The core delegates the network to an external backend, so the egress boundary
// is the set of endpoints the core names when it invokes a backend. This test
// runs a full session across every shipped source through the real fetch
// orchestration, records every process invocation, and fails if any destination
// is not one the source's own resolver produced. It also installs an HTTP guard
// so a direct call added to the core is caught. Inject an outbound call into
// FetchQuery and this named test fails.
//
// Sabotage check: add http.Get("https://telemetry.example") to fetch.FetchQuery
// and this test fails at the guard; add a backend invocation to an undeclared
// host and it fails at the recorded-destination check. Restore, and it passes.
func TestEgress_DefaultSessionContactsOnlyConfiguredSourceEndpoints(t *testing.T) {
	recorder := configureSession(t)
	guard := installEgressGuard(t)

	sources := resolvableSources(t)

	// Derive the permitted endpoints from the resolvers themselves, so the test
	// cannot drift from what the sources actually resolve.
	allowedHosts := map[string]bool{}
	queries := map[string]string{}
	for _, name := range sources {
		src, _ := fetch.LookupSource(name)
		q := sampleQuery(name)
		req, err := src.Resolve(q)
		if err != nil {
			t.Fatalf("source %q could not resolve a sample query: %v", name, err)
		}
		host, ok := urlHost(req.URL)
		if !ok {
			t.Fatalf("source %q resolved %q, which is not an absolute http(s) URL", name, req.URL)
		}
		allowedHosts[host] = true
		queries[name] = q
	}

	// Run the session through the real registry and fetch orchestration.
	reg := backend.NewRegistry()
	for _, name := range sources {
		if err := reg.RegisterSource(name, securityBackend(t, "security-fixture-"+name)); err != nil {
			t.Fatalf("RegisterSource(%q) failed: %v", name, err)
		}
	}
	store, err := evidence.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	for _, name := range sources {
		// A distinct payload per source keeps each fetch content-addressed
		// separately; the store is append-only and refuses to alias two
		// records onto one hash.
		t.Setenv(securityPayloadEnv, `{"source":"`+name+`"}`)
		if _, err := fetch.FetchQuery(context.Background(), store, reg, name, queries[name]); err != nil {
			t.Fatalf("FetchQuery(%q) failed: %v", name, err)
		}
	}

	// No direct HTTP request may have been made anywhere in the session.
	if requests := guard.snapshot(); len(requests) != 0 {
		t.Fatalf("the core opened an undeclared outbound HTTP connection: %v", requests)
	}

	invocations := readInvocations(t, recorder)
	if len(invocations) == 0 {
		t.Fatal("no backend invocation was recorded; the session never ran")
	}
	contacts := 0
	for _, inv := range invocations {
		for _, arg := range inv.Argv {
			host, ok := urlHost(arg)
			if !ok {
				continue
			}
			contacts++
			if !allowedHosts[host] {
				t.Errorf("the session reached undeclared destination %q (argument %q); configured endpoints are %v",
					host, arg, allowedHosts)
			}
		}
	}
	if contacts == 0 {
		t.Fatal("no fetched destination was recorded; the endpoint check would be vacuous")
	}
}

// Done when: 2. No telemetry, ever, under any configuration: the binary makes
// zero network calls until a source fetch is explicitly triggered.
//
// Building the app, loading the registry, listing sources and resolving a query
// are pure: none may open a connection or execute a backend. The test then
// triggers a fetch and proves the guards were live by observing the backend run.
// Inject any network call into app construction or resolution and this named
// test fails.
//
// Sabotage check: perform an http.Get from fetch.LookupSource (or FetchQuery's
// resolution phase) and this test fails before the fetch is triggered.
func TestEgress_NoNetworkUntilSourceFetchExplicitlyTriggered(t *testing.T) {
	recorder := configureSession(t)
	guard := installEgressGuard(t)

	// Non-fetch operations. None of these retrieves bytes.
	reg := backend.DefaultRegistry()
	_ = reg.Sources()
	for _, src := range fetch.Sources() {
		_ = src.Name
		_, _ = src.Resolve(sampleQuery(src.Name))
	}

	if requests := guard.snapshot(); len(requests) != 0 {
		t.Fatalf("a network call happened before a fetch was triggered: %v", requests)
	}
	if _, err := os.Stat(recorder); !os.IsNotExist(err) {
		t.Fatalf("a backend executed before a fetch was triggered (recorder exists: %v)", err)
	}

	// Now trigger a fetch. It must execute the backend, proving the guards
	// above were live rather than the fetch path simply being broken.
	store := evidence.NewMemoryStore()
	triggerReg := backend.NewRegistry()
	if err := triggerReg.RegisterSource("hacker-news", securityBackend(t, "recorder")); err != nil {
		t.Fatalf("RegisterSource failed: %v", err)
	}
	if _, err := fetch.FetchQuery(context.Background(), store, triggerReg, "hacker-news", "golang"); err != nil {
		t.Fatalf("triggered fetch failed: %v", err)
	}
	if _, err := os.Stat(recorder); err != nil {
		t.Fatalf("the triggered fetch did not execute a backend; the test is vacuous: %v", err)
	}
	if requests := guard.snapshot(); len(requests) != 0 {
		t.Fatalf("the core opened an undeclared outbound HTTP connection during a fetch: %v", requests)
	}
}

// Done when: 2 (static half). No production Go file opens an outbound HTTP
// connection. Fetching is delegated to an external backend, so a client call in
// the core would be either telemetry or an undeclared destination. The dashboard
// is an inbound loopback server and uses none of these calls.
//
// Sabotage check: add http.Get anywhere under internal/ or cmd/ and this named
// test fails.
func TestEgress_NoOutboundHTTPClientInCore(t *testing.T) {
	root := repoRoot(t)
	patterns := []string{
		"http.Get(",
		"http.Post(",
		"http.PostForm(",
		"http.Head(",
		"http.NewRequest(",
		"http.DefaultClient",
		"http.Client{",
	}

	scanned := 0
	for _, dir := range []string{"internal", "cmd"} {
		walkErr := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			scanned++
			rel, _ := filepath.Rel(root, path)
			for _, pattern := range patterns {
				if strings.Contains(string(data), pattern) {
					t.Errorf("%s contains %q: the core must not open outbound connections", rel, pattern)
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("failed to scan %s: %v", dir, walkErr)
		}
	}
	if scanned == 0 {
		t.Fatal("no production Go files were scanned")
	}
}

// repoRoot locates the repository root from this test file's location.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate the test source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
