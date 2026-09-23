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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/backend"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/fetch"
	"github.com/aniklavida/words-on-the-street/internal/verify"
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

// Done when: 1. Every new route (/fetches, /verifications, /sources, /fetches/{hash},
// /verifications/{hash}) is registered through the same readOnly wrapper as / and
// /api/state — a POST to each must return 405, proven by a test that iterates all routes
// rather than one copy-pasted per route.
func TestDashboard_RoutesAreReadOnly(t *testing.T) {
	store := evidence.NewMemoryStore()
	seedFetch := []byte("seed fetch payload")
	fetchHash := fmt.Sprintf("%x", sha256.Sum256(seedFetch))
	if err := store.Save(&evidence.Record{
		ResolvedURL:    "https://example.com/fetch",
		Timestamp:      time.Now().UTC(),
		Hash:           fetchHash,
		BackendName:    "example",
		BackendVersion: "1.0.0",
		Version:        "1.0.0",
		Payload:        seedFetch,
		RawPayload:     seedFetch,
		BackendStatus:  "reachable",
	}); err != nil {
		t.Fatalf("failed to seed fetch record: %v", err)
	}

	seedVerify := []byte("seed verify payload")
	verifyHash := fmt.Sprintf("%x", sha256.Sum256(seedVerify))
	if err := store.Save(&evidence.Record{
		ResolvedURL:    "https://example.com/verify",
		Timestamp:      time.Now().UTC(),
		Hash:           verifyHash,
		BackendName:    "example",
		BackendVersion: "1.0.0",
		Version:        "1.0.0",
		Payload:        seedVerify,
		RawPayload:     seedVerify,
		BackendStatus:  verify.StatusVerifiedIdentical,
	}); err != nil {
		t.Fatalf("failed to seed verification record: %v", err)
	}

	before, err := store.List()
	if err != nil {
		t.Fatalf("failed to snapshot store: %v", err)
	}

	srv := &Server{Records: store}
	handler := srv.Handler()

	routes := []string{
		"/",
		"/api/state",
		"/fetches",
		"/verifications",
		"/sources",
		"/fetches/" + fetchHash,
		"/verifications/" + verifyHash,
	}

	for _, path := range routes {
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

// Done when: 2. An empty evidence store renders a calm empty state on every list
// page, not an error and not fabricated rows.
func TestDashboard_EmptyStoreRendersCalmEmptyState(t *testing.T) {
	store := evidence.NewMemoryStore()
	srv := &Server{Records: store}
	handler := srv.Handler()

	pages := []struct {
		path      string
		wantEmpty string
	}{
		{"/", "No fetches recorded yet."},
		{"/", "No verifications recorded yet."},
		{"/fetches", "No fetches recorded yet."},
		{"/verifications", "No verifications recorded yet."},
		{"/sources", "No sources registered."},
	}

	for _, p := range pages {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p.path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s returned %d, want 200", p.path, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, p.wantEmpty) {
			t.Errorf("GET %s body missing calm empty message %q", p.path, p.wantEmpty)
		}
	}

	// Verify no fabricated rows in /fetches and /verifications tables
	for _, path := range []string{"/fetches", "/verifications"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		body := rec.Body.String()
		if strings.Contains(body, `<a class="table-link"`) {
			t.Errorf("GET %s rendered fabricated table rows for empty store", path)
		}
	}
}

// Done when: 3. No official brand logo or trademarked icon appears anywhere in
// the rendered HTML or any committed asset — grep the diff yourself for common
// brand-name strings used as image references/alt text before claiming this,
// and say what you checked for in the pull request body.
func TestDashboard_NoBrandLogosOrTrademarkedIcons(t *testing.T) {
	store := evidence.NewMemoryStore()
	seed := []byte("content for logo test")
	hash := fmt.Sprintf("%x", sha256.Sum256(seed))
	if err := store.Save(&evidence.Record{
		ResolvedURL:    "https://example.com/item",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "curl",
		BackendVersion: "1.0.0",
		Version:        "1.0.0",
		Payload:        seed,
		RawPayload:     seed,
		BackendStatus:  "reachable",
	}); err != nil {
		t.Fatalf("failed to seed store: %v", err)
	}

	srv := &Server{Records: store, Registry: backend.DefaultRegistry()}
	handler := srv.Handler()

	paths := []string{"/", "/fetches", "/verifications", "/sources", "/fetches/" + hash}

	// Forbidden patterns: brand marks, logo files, trademarked icon classes, or brand alt tags
	forbidden := []string{
		"reddit-logo", "twitter-logo", "github-logo", "google-logo", "youtube-logo",
		`alt="reddit"`, `alt="twitter"`, `alt="github"`, `alt="google"`, `alt="youtube"`,
		"logo.svg", "logo.png", "logo.webp", "brand-logo", "brand-icon",
	}

	for _, p := range paths {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d, want 200", p, rec.Code)
		}
		body := strings.ToLower(rec.Body.String())

		// Ensure no <img> tag is used for brand logos
		if strings.Contains(body, "<img") {
			t.Errorf("page %s contains <img> tag, which may introduce external or trademarked images", p)
		}

		for _, term := range forbidden {
			if strings.Contains(body, term) {
				t.Errorf("page %s contains forbidden trademark/logo reference %q", p, term)
			}
		}

		// Verify source avatars are rendered using generic class
		if p == "/sources" || p == "/" {
			if !strings.Contains(body, "source-avatar") {
				t.Errorf("page %s does not use generic .source-avatar class", p)
			}
		}
	}
}

// Done when: 4. The /sources page shows exactly the sources the registry defines
// — no more, no fewer — proven against a fixture registry with a source count
// different from the mockup's, asserting the rendered page matches the fixture,
// not a hardcoded number.
func TestDashboard_SourcesMatchesRegistry(t *testing.T) {
	reg := backend.NewRegistry()
	fixtureSources := []string{"source-one", "source-two", "source-three"}

	for _, name := range fixtureSources {
		if err := reg.RegisterSource(name, backend.Backend{
			Name:         "tool-" + name,
			Command:      "echo",
			VersionRange: ">= 1.0.0",
			Licence:      "MIT",
		}); err != nil {
			t.Fatalf("failed to register %s: %v", name, err)
		}
	}

	srv := &Server{Records: evidence.NewMemoryStore(), Registry: reg}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sources", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sources returned %d, want 200", rec.Code)
	}

	body := rec.Body.String()

	// Assert exactly 3 source cards rendered (mockup had 6)
	cardPattern := regexp.MustCompile(`class="source-card"`)
	matches := cardPattern.FindAllString(body, -1)
	if len(matches) != len(fixtureSources) {
		t.Fatalf("GET /sources rendered %d source cards, want exactly %d", len(matches), len(fixtureSources))
	}

	for _, name := range fixtureSources {
		attr := fmt.Sprintf(`data-source="%s"`, name)
		if !strings.Contains(body, attr) {
			t.Errorf("GET /sources missing expected source card %s", attr)
		}
	}

	// Assert unconfigured mockup sources are not present
	for _, unconfigured := range []string{"reddit", "youtube"} {
		attr := fmt.Sprintf(`data-source="%s"`, unconfigured)
		if strings.Contains(body, attr) {
			t.Errorf("GET /sources contains unconfigured mockup source %s", attr)
		}
	}
}

// Done when: 5. A verification's detail page renders Result.Diff with removed
// and added lines visually distinguished (different CSS classes, at minimum)
// when state is Changed, and renders no diff section at all when state is
// Identical or Gone.
func TestDashboard_VerificationDetailDiff(t *testing.T) {
	t.Run("state changed renders visual diff", func(t *testing.T) {
		store := evidence.NewMemoryStore()

		origPayload := []byte("headline: initial version\nbody: untouched content\n")
		origHash := fmt.Sprintf("%x", sha256.Sum256(origPayload))
		if err := store.Save(&evidence.Record{
			ResolvedURL:    "https://example.com/diff-target",
			Timestamp:      time.Now().UTC().Add(-1 * time.Hour),
			Hash:           origHash,
			BackendName:    "fetcher",
			BackendVersion: "1.0.0",
			Version:        "1.0.0",
			Payload:        origPayload,
			RawPayload:     origPayload,
			BackendStatus:  "reachable",
		}); err != nil {
			t.Fatalf("failed to save original record: %v", err)
		}

		changedPayload := []byte("headline: modified version\nbody: untouched content\nfooter: new addition\n")
		changedHash := fmt.Sprintf("%x", sha256.Sum256(changedPayload))
		if err := store.Save(&evidence.Record{
			ResolvedURL:    "https://example.com/diff-target",
			Timestamp:      time.Now().UTC(),
			Hash:           changedHash,
			BackendName:    "fetcher",
			BackendVersion: "1.0.0",
			Version:        "1.0.0",
			Payload:        changedPayload,
			RawPayload:     changedPayload,
			BackendStatus:  verify.StatusVerifiedChanged,
		}); err != nil {
			t.Fatalf("failed to save changed verification record: %v", err)
		}

		srv := &Server{Records: store}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/verifications/"+changedHash, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /verifications/%s returned %d, want 200", changedHash, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `<div class="diff-container">`) {
			t.Errorf("expected <div class=\"diff-container\"> in Changed verification page")
		}
		if !strings.Contains(body, "diff-line diff-removed") {
			t.Errorf("expected diff-line diff-removed in Changed verification page")
		}
		if !strings.Contains(body, "diff-line diff-added") {
			t.Errorf("expected diff-line diff-added in Changed verification page")
		}
		if !strings.Contains(body, "-headline: initial version") {
			t.Errorf("expected removed line '-headline: initial version' in diff")
		}
		if !strings.Contains(body, "headline: modified version") {
			t.Errorf("expected added line 'headline: modified version' in diff")
		}
	})

	t.Run("state identical renders no diff section", func(t *testing.T) {
		store := evidence.NewMemoryStore()

		identicalPayload := []byte("identical target content\n")
		identicalHash := fmt.Sprintf("%x", sha256.Sum256(identicalPayload))
		if err := store.Save(&evidence.Record{
			ResolvedURL:    "https://example.com/identical-target",
			Timestamp:      time.Now().UTC(),
			Hash:           identicalHash,
			BackendName:    "fetcher",
			BackendVersion: "1.0.0",
			Version:        "1.0.0",
			Payload:        identicalPayload,
			RawPayload:     identicalPayload,
			BackendStatus:  verify.StatusVerifiedIdentical,
		}); err != nil {
			t.Fatalf("failed to save identical verification record: %v", err)
		}

		srv := &Server{Records: store}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/verifications/"+identicalHash, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /verifications/%s returned %d, want 200", identicalHash, rec.Code)
		}
		body := rec.Body.String()

		if strings.Contains(body, `<div class="diff-container">`) {
			t.Errorf("expected no diff-container element in Identical verification page")
		}
		if strings.Contains(body, "diff-line diff-removed") || strings.Contains(body, "diff-line diff-added") {
			t.Errorf("expected no diff lines in Identical verification page")
		}
	})

	t.Run("state gone renders no diff section", func(t *testing.T) {
		store := evidence.NewMemoryStore()

		gonePayload := []byte("source is gone marker")
		goneHash := fmt.Sprintf("%x", sha256.Sum256(gonePayload))
		if err := store.Save(&evidence.Record{
			ResolvedURL:    "https://example.com/gone-target",
			Timestamp:      time.Now().UTC(),
			Hash:           goneHash,
			BackendName:    "fetcher",
			BackendVersion: "1.0.0",
			Version:        "1.0.0",
			Payload:        gonePayload,
			RawPayload:     gonePayload,
			BackendStatus:  string(verify.Gone),
		}); err != nil {
			t.Fatalf("failed to save gone record: %v", err)
		}

		srv := &Server{Records: store}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/verifications/"+goneHash, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /verifications/%s returned %d, want 200", goneHash, rec.Code)
		}
		body := rec.Body.String()

		if strings.Contains(body, `<div class="diff-container">`) {
			t.Errorf("expected no diff-container element in Gone verification page")
		}
		if strings.Contains(body, "diff-line diff-removed") || strings.Contains(body, "diff-line diff-added") {
			t.Errorf("expected no diff lines in Gone verification page")
		}
	})
}

// TestDashboard_FetchDetail tests that a fetch's detail view renders all sanitized evidence fields
// and returns 404 for unknown records.
func TestDashboard_FetchDetail(t *testing.T) {
	store := evidence.NewMemoryStore()
	payload := []byte("stored fetch bytes for detail test")
	hash := fmt.Sprintf("%x", sha256.Sum256(payload))
	now := time.Now().UTC()

	if err := store.Save(&evidence.Record{
		ResolvedURL:    "https://example.com/details/test",
		Timestamp:      now,
		Hash:           hash,
		BackendName:    "fetch-engine",
		BackendVersion: "2.1.0",
		Version:        "2.1.0",
		Payload:        payload,
		RawPayload:     payload,
		BackendStatus:  "reachable",
		IsFallback:     true,
		BackendArgs:    []string{"--timeout=30", "--agent=words"},
	}); err != nil {
		t.Fatalf("failed to save fetch record: %v", err)
	}

	srv := &Server{Records: store}
	handler := srv.Handler()

	// 1. Success case
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fetches/"+hash, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /fetches/%s returned %d, want 200", hash, rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		"https://example.com/details/test",
		"fetch-engine",
		"fallback",
		"reachable",
		"--timeout=30",
		"--agent=words",
		hash,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /fetches/%s missing expected field %q", hash, want)
		}
	}

	// 2. Not found case
	rec404 := httptest.NewRecorder()
	handler.ServeHTTP(rec404, httptest.NewRequest(http.MethodGet, "/fetches/nonexistent-hash", nil))
	if rec404.Code != http.StatusNotFound {
		t.Errorf("GET /fetches/nonexistent-hash returned %d, want 404", rec404.Code)
	}

	recVerify404 := httptest.NewRecorder()
	handler.ServeHTTP(recVerify404, httptest.NewRequest(http.MethodGet, "/verifications/nonexistent-hash", nil))
	if recVerify404.Code != http.StatusNotFound {
		t.Errorf("GET /verifications/nonexistent-hash returned %d, want 404", recVerify404.Code)
	}
}
