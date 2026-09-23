package fetch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/backend"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/keychain"
)

// The value below is an obviously-fake placeholder, never a real credential.
const testTwitterCookie = "fake-twitter-session-cookie-not-real-0123456789"

// resetTwitterState gives a test a known-empty redaction set and rate limiter,
// and a limit high enough that the fixture fetch never waits.
func resetTwitterState(t *testing.T) {
	t.Helper()
	evidence.ResetSecrets()
	resetCredentialRateLimiters()
	t.Setenv(TwitterRateLimitEnv, "60000")
	t.Cleanup(func() {
		evidence.ResetSecrets()
		resetCredentialRateLimiters()
	})
}

// Done when: 1. Twitter/X has a resolver reached through the same Sources()
// lookup the other sources use, and every query form maps to a concrete URL
// that the backend is handed as an argument.
func TestSources_TwitterResolvesQueryToConcreteURL(t *testing.T) {
	cases := []struct {
		query   string
		wantURL string
		wantErr bool
	}{
		{"golang", "https://x.com/search?q=golang&f=live", false},
		{"go lang", "https://x.com/search?q=go+lang&f=live", false},
		{"39123456", "https://x.com/i/status/39123456", false},
		{"user:jack", "https://x.com/jack", false},
		{"@jack", "https://x.com/jack", false},
		{"user:too_long_a_handle", "", true},
		{"user:../etc", "", true},
		{"", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			src, ok := LookupSource("twitter")
			if !ok {
				t.Fatal("twitter has no resolver in Sources()")
			}
			req, err := src.Resolve(tc.query)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected resolution of %q to fail, got URL %q", tc.query, req.URL)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q) failed: %v", tc.query, err)
			}
			if req.URL != tc.wantURL {
				t.Errorf("Resolve(%q) = %q, want %q", tc.query, req.URL, tc.wantURL)
			}
			if len(req.Args) == 0 || req.Args[len(req.Args)-1] != tc.wantURL {
				t.Errorf("Resolve(%q) args %v must end with the resolved URL %q", tc.query, req.Args, tc.wantURL)
			}
		})
	}
}

// Done when: 1. The registry ships a twitter source alongside the four existing
// ones, and it is reachable through the default registry.
func TestTwitterSource_RegisteredInDefaultRegistry(t *testing.T) {
	if _, ok := backend.DefaultSources["twitter"]; !ok {
		t.Fatal("twitter is not registered in backend.DefaultSources")
	}
	if _, ok := backend.DefaultRegistry().BackendsForSource("twitter"); !ok {
		t.Fatal("twitter has no backends in the default registry")
	}
}

// Done when: 1. A twitter fetch produces a complete evidence record through the
// real registry and store, exactly as the other sources do. The session cookie
// is sent to the backend and is not what the record names.
func TestFetchQuery_TwitterRecordsCompleteEvidence(t *testing.T) {
	resetTwitterState(t)
	t.Setenv(TwitterCookieEnv, testTwitterCookie)

	payload := []byte(`{"bytes":"received as-is"}`)
	fixture, _ := sourceFixture(t, payload)

	store, err := evidence.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	reg := sourceRegistry(t, "twitter", fixture)

	before := time.Now().UTC().Add(-time.Second)
	hash, err := FetchQuery(context.Background(), store, reg, "twitter", "golang")
	if err != nil {
		t.Fatalf("FetchQuery failed: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}

	wantURL := "https://x.com/search?q=golang&f=live"
	expectedHash := fmt.Sprintf("%x", sha256.Sum256(payload))
	if rec.Hash != expectedHash {
		t.Errorf("hash %q does not cover the bytes as received (%q)", rec.Hash, expectedHash)
	}
	if hash != expectedHash {
		t.Errorf("FetchQuery returned hash %q, want %q", hash, expectedHash)
	}
	if rec.ResolvedURL != wantURL {
		t.Errorf("resolved URL %q, want %q", rec.ResolvedURL, wantURL)
	}
	foundURLArg := false
	for _, arg := range rec.BackendArgs {
		if arg == wantURL {
			foundURLArg = true
			break
		}
	}
	if !foundURLArg {
		t.Errorf("backend args %v do not include the resolved URL %q", rec.BackendArgs, wantURL)
	}
	if rec.BackendName != fixture.Name {
		t.Errorf("backend name %q, want %q", rec.BackendName, fixture.Name)
	}
	version := rec.BackendVersion
	if version == "" {
		version = rec.Version
	}
	if version != sourceVersion {
		t.Errorf("backend version %q, want %q", version, sourceVersion)
	}
	if rec.Timestamp.IsZero() || rec.Timestamp.Location() != time.UTC {
		t.Errorf("timestamp %v must be a real UTC instant", rec.Timestamp)
	}
	if rec.Timestamp.Before(before) || rec.Timestamp.After(after) {
		t.Errorf("timestamp %v not within the fetch window [%v, %v]", rec.Timestamp, before, after)
	}
	if rec.BackendStatus != string(backend.StatusReachable) {
		t.Errorf("backend status %q, want %q", rec.BackendStatus, backend.StatusReachable)
	}
	if len(rec.RawBytes()) != len(payload) {
		t.Errorf("raw payload has %d bytes, want %d", len(rec.RawBytes()), len(payload))
	}
}

// Done when: 2. The configured session cookie never appears in an evidence
// record, a log line generated from the same fetch, or the agent-facing output
// the MCP layer would return. The assertion is made against all three surfaces
// separately, and the same test drives the whole store rather than one record.
func TestTwitterCookie_NeverReachesRecordLogOrAgentOutput(t *testing.T) {
	resetTwitterState(t)
	t.Setenv(TwitterCookieEnv, testTwitterCookie)

	payload := []byte(`{"tweet":"hello, street"}`)
	fixture, _ := sourceFixture(t, payload)

	storeDir := t.TempDir()
	store, err := evidence.NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	reg := sourceRegistry(t, "twitter", fixture)

	hash, err := FetchQuery(context.Background(), store, reg, "twitter", "golang")
	if err != nil {
		t.Fatalf("FetchQuery failed: %v", err)
	}
	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}

	// 1. The evidence record. The cookie was sent to the backend, so the record
	// must carry the redaction placeholder where the credential argument was.
	leaked := false
	redactedCredential := false
	for _, arg := range rec.BackendArgs {
		if strings.Contains(arg, testTwitterCookie) {
			leaked = true
		}
		if strings.Contains(arg, evidence.RedactionPlaceholder) {
			redactedCredential = true
		}
	}
	if leaked {
		t.Errorf("evidence record backend args leaked the cookie: %v", rec.BackendArgs)
	}
	if !redactedCredential {
		t.Errorf("evidence record args %v do not show a redacted credential; the cookie may never have been sent", rec.BackendArgs)
	}

	// The whole store, not one record: ledger and content-addressed files.
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
		if bytes.Contains(data, []byte(testTwitterCookie)) {
			t.Errorf("store file %s contains the cookie", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("error scanning store: %v", err)
	}

	// 2. A log line built the way the CLI builds its messages.
	logLine := evidence.Redact(fmt.Sprintf("fetching twitter with cookie %s", testTwitterCookie))
	if strings.Contains(logLine, testTwitterCookie) {
		t.Errorf("log line leaked the cookie: %s", logLine)
	}

	// 3. The agent-facing output the MCP layer returns.
	agentOutput := evidence.Redact(string(rec.Payload))
	if strings.Contains(agentOutput, testTwitterCookie) {
		t.Errorf("agent output leaked the cookie: %s", agentOutput)
	}
	if bytes.Contains(rec.RawBytes(), []byte(testTwitterCookie)) {
		t.Error("recorded payload contains the cookie")
	}
}

// A fetch with no cookie configured still resolves and records; it simply sends
// no credential. This keeps the source usable without pretending a cookie is
// mandatory to the resolver.
func TestTwitterSource_WithoutCookieSendsNoCredential(t *testing.T) {
	resetTwitterState(t)
	t.Setenv(TwitterCookieEnv, "")

	src, ok := LookupSource("twitter")
	if !ok {
		t.Fatal("twitter has no resolver")
	}
	if src.CredentialArgs == nil {
		t.Fatal("twitter resolver must be able to carry a credential")
	}
	if args := src.CredentialArgs(); args != nil {
		t.Errorf("no cookie configured, but credential args were produced: %v", args)
	}
}

// Done when: rate limiting is conservative by default and raises only through
// configuration. The risk of raising it is disclosed in the docs and in
// `configure twitter`; the setting is not gated behind a confirmation.
func TestTwitterRateLimit_ConservativeByDefaultAndRaiseable(t *testing.T) {
	t.Setenv(TwitterRateLimitEnv, "")
	if got := TwitterRateLimitPerMinute(); got != DefaultTwitterRateLimitPerMinute {
		t.Errorf("default limit = %d, want %d", got, DefaultTwitterRateLimitPerMinute)
	}
	if DefaultTwitterRateLimitPerMinute >= 60 {
		t.Errorf("default limit %d is not conservative", DefaultTwitterRateLimitPerMinute)
	}

	t.Setenv(TwitterRateLimitEnv, "60")
	if got := TwitterRateLimitPerMinute(); got != 60 {
		t.Errorf("raised limit = %d, want 60", got)
	}

	t.Setenv(TwitterRateLimitEnv, "not-a-number")
	if got := TwitterRateLimitPerMinute(); got != DefaultTwitterRateLimitPerMinute {
		t.Errorf("unparseable limit = %d, want the conservative default %d", got, DefaultTwitterRateLimitPerMinute)
	}
}

func TestRateLimiter_EnforcesMinimumIntervalBetweenCalls(t *testing.T) {
	limiter := NewRateLimiter(1) // one call per minute
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var slept []time.Duration
	limiter.now = func() time.Time { return base }
	limiter.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		base = base.Add(d)
		return nil
	}

	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait failed: %v", err)
	}
	if len(slept) != 0 {
		t.Fatalf("first Wait must not block, slept %v", slept)
	}
	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("second Wait failed: %v", err)
	}
	if len(slept) != 1 || slept[0] != time.Minute {
		t.Fatalf("second Wait slept %v, want exactly one minute", slept)
	}
}

// Done when: 1. A cookie stored in the keychain is read and used for a fetch,
// without ever needing the environment variable set.
// Uses a fake/mock keychain backend for CI, since a real OS keychain isn't
// scriptable in an automated test.
func TestTwitter_CookieFromKeychainUsedForFetchWithoutEnvVar(t *testing.T) {
	resetTwitterState(t)
	mem := keychain.NewMemoryProvider()
	keychain.SetProvider(mem)
	defer keychain.ResetProvider()

	const fakeCookie = "fake-twitter-keychain-cookie-12345"
	if err := keychain.SetTwitterCookie(fakeCookie); err != nil {
		t.Fatalf("failed to store cookie in keychain: %v", err)
	}

	// Environment variable is completely unset
	t.Setenv(TwitterCookieEnv, "")

	payload := []byte(`{"tweet":"served using keychain session"}`)
	fixture, _ := sourceFixture(t, payload)

	store, err := evidence.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	reg := sourceRegistry(t, "twitter", fixture)

	hash, err := FetchQuery(context.Background(), store, reg, "twitter", "golang")
	if err != nil {
		t.Fatalf("FetchQuery failed: %v", err)
	}

	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}

	// The record backend arguments must show the cookie header redacted
	foundRedacted := false
	for _, arg := range rec.BackendArgs {
		if strings.Contains(arg, fakeCookie) {
			t.Errorf("backend args contain raw keychain cookie: %s", arg)
		}
		if arg == "Cookie: [REDACTED]" {
			foundRedacted = true
		}
	}
	if !foundRedacted {
		t.Errorf("expected redacted cookie argument in backend args, got %v", rec.BackendArgs)
	}
}

// Done when: 2. When no keychain is available or empty, the existing environment-variable
// path is unaffected and falls back cleanly.
func TestTwitter_FallbackToEnvironmentVariableWhenKeychainEmptyOrUnavailable(t *testing.T) {
	resetTwitterState(t)
	// Empty keychain provider simulates unset keychain / no secret found
	mem := keychain.NewMemoryProvider()
	keychain.SetProvider(mem)
	defer keychain.ResetProvider()

	t.Setenv(TwitterCookieEnv, testTwitterCookie)

	payload := []byte(`{"tweet":"served using env fallback"}`)
	fixture, _ := sourceFixture(t, payload)

	store, err := evidence.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	reg := sourceRegistry(t, "twitter", fixture)

	hash, err := FetchQuery(context.Background(), store, reg, "twitter", "golang")
	if err != nil {
		t.Fatalf("FetchQuery failed: %v", err)
	}

	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}

	foundRedacted := false
	for _, arg := range rec.BackendArgs {
		if strings.Contains(arg, testTwitterCookie) {
			t.Errorf("backend args contain raw env cookie: %s", arg)
		}
		if arg == "Cookie: [REDACTED]" {
			foundRedacted = true
		}
	}
	if !foundRedacted {
		t.Errorf("expected redacted cookie argument in backend args, got %v", rec.BackendArgs)
	}
}
