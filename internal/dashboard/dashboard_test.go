package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/backend"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/fetch"
)

// TestDashboardHelperBackend is re-executed as a portable fixture backend. It is
// not a shell script, so it behaves identically on Linux, macOS, and Windows.
// With --version it answers a health check; otherwise it emits the bytes held in
// DASHBOARD_HELPER_PAYLOAD, which lets a test make a backend echo a credential
// into the fetched payload.
func TestDashboardHelperBackend(t *testing.T) {
	if os.Getenv("GO_WANT_DASHBOARD_HELPER") != "1" {
		return
	}
	for _, arg := range os.Args {
		if arg == "--version" {
			fmt.Println("dashboard-helper version 1.0.0")
			os.Exit(0)
		}
	}
	fmt.Print(os.Getenv("DASHBOARD_HELPER_PAYLOAD"))
	os.Exit(0)
}

func helperCommand(t *testing.T) string {
	t.Helper()
	exe, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("failed to resolve test binary path: %v", err)
	}
	return exe
}

// Done when: 1. The server only binds to a loopback address. This asks the same
// Listen the `serve` command uses for the address it actually bound, and proves
// the guard refuses a wildcard address rather than trusting inspection.
func TestDashboard_BindsLoopbackOnly(t *testing.T) {
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(127.0.0.1:0) failed: %v", err)
	}
	defer ln.Close()

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("expected a *net.TCPAddr, got %T", ln.Addr())
	}
	if !tcpAddr.IP.IsLoopback() {
		t.Fatalf("dashboard bound to %s, which is not a loopback address", tcpAddr.IP)
	}

	for _, wildcard := range []string{"0.0.0.0:0", "[::]:0"} {
		if wildcardLn, err := Listen(wildcard); err == nil {
			wildcardLn.Close()
			t.Fatalf("Listen(%q) must refuse a non-loopback address", wildcard)
		}
	}
}

// Done when: 2. A rendered page never contains a credential value that was
// present in a fetch's original arguments. The fixture backend echoes the
// credential into the stored payload, so the test also proves the dashboard does
// not re-derive a raw value from the bytes it holds.
func TestDashboard_NeverRendersCredentialFromFetch(t *testing.T) {
	const secret = "fake-credential-do-not-leak-0001"
	t.Setenv("GO_WANT_DASHBOARD_HELPER", "1")
	t.Setenv("DASHBOARD_HELPER_PAYLOAD", secret)

	store := evidence.NewMemoryStore()
	exe := helperCommand(t)
	args := []string{"-test.run=^TestDashboardHelperBackend$", "--", "--token=" + secret}
	url := "https://example.com/p?api_key=" + secret

	hash, err := fetch.Fetch(context.Background(), store, exe, args, url, "1.0.0", false)
	if err != nil {
		t.Fatalf("fetch with a credential in its arguments must succeed: %v", err)
	}

	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("failed to load record: %v", err)
	}
	if !strings.Contains(string(rec.Payload), secret) {
		t.Fatalf("fixture is not exercising the leak: stored payload does not contain the credential")
	}
	if strings.Contains(rec.ResolvedURL, secret) {
		t.Fatalf("fixture is invalid: stored URL still contains the credential")
	}
	if joined := strings.Join(rec.BackendArgs, " "); strings.Contains(joined, secret) {
		t.Fatalf("fixture is invalid: stored arguments still contain the credential")
	}

	srv := &Server{Records: store}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("GET / returned %d, body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, secret) {
		t.Fatalf("dashboard rendered the credential; a raw value leaked into the page")
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Fatalf("dashboard did not render the sanitized argument marker")
	}
}

// Done when: 3. The dashboard's status section matches what CheckAllStatus
// reports directly. A healthy helper backend and a missing one give two distinct
// states, and the rendered view is compared field by field rather than loosely.
func TestDashboard_StatusMatchesCheckAllStatus(t *testing.T) {
	const payload = "status-fixture"
	t.Setenv("GO_WANT_DASHBOARD_HELPER", "1")
	t.Setenv("DASHBOARD_HELPER_PAYLOAD", payload)

	reg := backend.NewRegistry()
	missing := filepath.Join(t.TempDir(), "no-such-tool")
	if err := reg.RegisterSource("alpha", backend.Backend{
		Name: "missing-primary", Command: missing, VersionRange: ">= 1.0.0", Licence: "MIT",
	}); err != nil {
		t.Fatalf("failed to register alpha: %v", err)
	}
	if err := reg.RegisterSource("beta", backend.Backend{
		Name:         "helper",
		Command:      helperCommand(t),
		Args:         []string{"-test.run=^TestDashboardHelperBackend$", "--"},
		VersionArgs:  []string{"-test.run=^TestDashboardHelperBackend$", "--", "--version"},
		VersionRange: ">= 1.0.0",
		Licence:      "MIT",
	}); err != nil {
		t.Fatalf("failed to register beta: %v", err)
	}

	srv := &Server{Records: evidence.NewMemoryStore(), Registry: reg}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/state returned %d, body: %s", rr.Code, rr.Body.String())
	}

	var got pageData
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode dashboard state: %v", err)
	}

	want := backend.CheckAllStatus(context.Background(), reg)
	if !reflect.DeepEqual(got.Statuses, want) {
		t.Fatalf("dashboard status does not match CheckAllStatus:\ngot:  %+v\nwant: %+v", got.Statuses, want)
	}

	htmlRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(htmlRecorder, httptest.NewRequest(http.MethodGet, "/", nil))
	html := htmlRecorder.Body.String()
	for _, st := range want {
		if !strings.Contains(html, fmt.Sprintf("data-source=%q", st.Source)) {
			t.Errorf("rendered page is missing source %q", st.Source)
		}
		if !strings.Contains(html, fmt.Sprintf("data-state=%q", st.State)) {
			t.Errorf("rendered page is missing state %q", st.State)
		}
	}
}

// Done when: 4. Every route is read-only. A POST to a GET-only route is refused
// and the store is byte-for-byte unchanged before and after.
func TestDashboard_RoutesAreReadOnly(t *testing.T) {
	store := evidence.NewMemoryStore()
	seed := []byte("seed payload")
	if err := store.Save(&evidence.Record{
		ResolvedURL:    "https://example.com/",
		Timestamp:      time.Now().UTC(),
		Hash:           fmt.Sprintf("%x", sha256.Sum256(seed)),
		BackendName:    "example",
		BackendVersion: "1.0.0",
		Version:        "1.0.0",
		Payload:        seed,
		RawPayload:     seed,
	}); err != nil {
		t.Fatalf("failed to seed store: %v", err)
	}

	before, err := store.List()
	if err != nil {
		t.Fatalf("failed to snapshot store: %v", err)
	}

	srv := &Server{Records: store}
	handler := srv.Handler()

	for _, path := range []string{"/", "/api/state"} {
		getRecorder := httptest.NewRecorder()
		handler.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, path, nil))
		if getRecorder.Code != http.StatusOK {
			t.Errorf("GET %s returned %d, want 200", path, getRecorder.Code)
		}

		postRecorder := httptest.NewRecorder()
		handler.ServeHTTP(postRecorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"url":"https://example.com"}`)))
		if postRecorder.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s returned %d, want 405 (read-only)", path, postRecorder.Code)
		}
	}

	after, err := store.List()
	if err != nil {
		t.Fatalf("failed to re-snapshot store: %v", err)
	}
	if len(before) != len(after) {
		t.Fatalf("read-only route mutated the store: %d records before, %d after", len(before), len(after))
	}
	for i := range before {
		if before[i].RecordHash != after[i].RecordHash {
			t.Fatalf("record %d changed: %s -> %s", i, before[i].RecordHash, after[i].RecordHash)
		}
	}
}
