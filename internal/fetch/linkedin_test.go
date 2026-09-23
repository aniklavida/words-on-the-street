package fetch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
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

// sourceHelperArgsEnv, when set, makes TestSourceHelperProcess write the exact
// arguments it received. It is declared here because only the LinkedIn leak test
// depends on it.
const sourceHelperArgsEnv = "SOURCE_HELPER_ARGS_FILE"

// Done when: 1. LinkedIn is registered and resolvable through the same
// Sources()/FetchQuery path as the existing sources, producing a complete record.
func TestLinkedIn_RegisteredAndResolvableThroughSameFetchPath(t *testing.T) {
	const fakeCookie = "li_at=FAKE-LINKEDIN-COOKIE-DO-NOT-USE-0000"

	t.Setenv(LinkedInCookieEnv, fakeCookie)
	t.Setenv(LinkedInMinIntervalEnv, "0")

	if _, ok := backend.DefaultRegistry().BackendsForSource("linkedin"); !ok {
		t.Fatal("linkedin has no backends in the shipped registry")
	}
	if _, ok := LookupSource("linkedin"); !ok {
		t.Fatal("linkedin has no resolver in Sources()")
	}

	payload := []byte(`{"source":"linkedin","posts":[{"text":"public post"}]}`)
	fixture, _ := sourceFixture(t, payload)

	store, err := evidence.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	reg := sourceRegistry(t, "linkedin", fixture)

	hash, err := FetchQuery(context.Background(), store, reg, "linkedin", "acme")
	if err != nil {
		t.Fatalf("FetchQuery for linkedin failed: %v", err)
	}

	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}
	if rec.ResolvedURL != "https://www.linkedin.com/search/results/content/?keywords=acme" {
		t.Errorf("resolved URL = %q, want the LinkedIn search URL", rec.ResolvedURL)
	}
	if rec.BackendName != fixture.Name {
		t.Errorf("backend name = %q, want %q", rec.BackendName, fixture.Name)
	}
	if rec.Hash != fmt.Sprintf("%x", sha256.Sum256(payload)) {
		t.Errorf("record hash does not cover the bytes as received")
	}
	if !bytes.Equal(rec.RawBytes(), payload) {
		t.Errorf("raw payload was not preserved as received")
	}
}

// Resolution builds the same kind of URL as the other sources, including the
// search term, and refuses a malformed slug before any backend runs.
func TestLinkedIn_ResolveQueryToConcreteURL(t *testing.T) {
	t.Setenv(LinkedInCookieEnv, "li_at=FAKE-LINKEDIN-COOKIE-DO-NOT-USE-0000")

	cases := []struct {
		query   string
		wantURL string
		wantErr bool
	}{
		{"golang", "https://www.linkedin.com/search/results/content/?keywords=golang", false},
		{"go lang", "https://www.linkedin.com/search/results/content/?keywords=go+lang", false},
		{"company:acme-corp", "https://www.linkedin.com/company/acme-corp/posts/", false},
		{"profile:jane-doe", "https://www.linkedin.com/in/jane-doe/", false},
		{"company:../etc", "", true},
		{"   ", "", true},
	}

	src, ok := LookupSource("linkedin")
	if !ok {
		t.Fatal("linkedin has no resolver")
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			req, err := src.Resolve(tc.query)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected resolution of %q to fail, got %q", tc.query, req.URL)
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
				t.Errorf("Resolve(%q) args must end with the resolved URL %q", tc.query, tc.wantURL)
			}
		})
	}
}

// Done when: 2. The configured LinkedIn session cookie never appears in an
// evidence record, a log line, or the bytes surfaced to an agent. The fixture
// records the arguments it actually received first, so the test proves the
// cookie was genuinely sent to the backend before proving it is absent from
// every surface that is kept or shown.
//
// Sabotage check: make evidence.SanitizeArgs return its input unchanged, and
// this named test fails (the record refuses to save an unredacted credential, or
// the whole-store scan finds it). Restore, and it passes again.
func TestLinkedIn_CookieNeverAppearsInRecordLogOrSynthesisOutput(t *testing.T) {
	const fakeCookie = "li_at=FAKE-LINKEDIN-SESSION-COOKIE-DO-NOT-USE-0000"

	t.Setenv(LinkedInCookieEnv, fakeCookie)
	t.Setenv(LinkedInMinIntervalEnv, "0")

	payload := []byte(`{"source":"linkedin","posts":[{"text":"public post"}]}`)
	fixture, _ := sourceFixture(t, payload)

	argsFile := filepath.Join(t.TempDir(), "received-args.txt")
	t.Setenv(sourceHelperArgsEnv, argsFile)

	storeDir := t.TempDir()
	store, err := evidence.NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	reg := sourceRegistry(t, "linkedin", fixture)

	hash, err := FetchQuery(context.Background(), store, reg, "linkedin", "acme")
	if err != nil {
		t.Fatalf("FetchQuery failed: %v", err)
	}

	// The backend must actually have received the cookie. Without this, the
	// redaction assertions below could pass because the cookie never left.
	received, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("fixture did not record its arguments: %v", err)
	}
	if !strings.Contains(string(received), fakeCookie) {
		t.Fatalf("fixture never received the cookie; the leak test would be vacuous")
	}

	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}

	// Surface: the evidence record.
	if strings.Contains(rec.ResolvedURL, fakeCookie) {
		t.Errorf("resolved URL contains the cookie: %s", rec.ResolvedURL)
	}
	joinedArgs := strings.Join(rec.BackendArgs, " ")
	if strings.Contains(joinedArgs, fakeCookie) {
		t.Errorf("recorded backend arguments contain the cookie: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, "[REDACTED]") {
		t.Errorf("recorded backend arguments do not show the redaction marker: %s", joinedArgs)
	}
	for _, attempt := range rec.BackendAttempts {
		if strings.Contains(attempt.Error, fakeCookie) {
			t.Errorf("recorded backend attempt error contains the cookie: %s", attempt.Error)
		}
	}

	// Surface: the bytes surfaced to an agent are exactly what arrived, and
	// carry no cookie.
	if !bytes.Equal(rec.Payload, payload) {
		t.Errorf("agent-facing payload is not the bytes as received:\ngot:  %q\nwant: %q", rec.Payload, payload)
	}
	if strings.Contains(string(rec.Payload), fakeCookie) {
		t.Errorf("agent-facing payload contains the cookie")
	}

	// Surface: a log line. Every backend fails, and the error the CLI would
	// print must name the failure without the credential.
	failing := backend.Backend{
		Name:         "missing-linkedin-backend",
		Command:      filepath.Join(t.TempDir(), "no-such-linkedin-backend"),
		VersionRange: ">= 1.0.0",
		Licence:      "MIT",
	}
	failReg := sourceRegistry(t, "linkedin", failing)
	_, failErr := FetchQuery(context.Background(), store, failReg, "linkedin", "acme")
	if failErr == nil {
		t.Fatal("expected a fetch with a missing backend to fail")
	}
	if strings.Contains(failErr.Error(), fakeCookie) {
		t.Errorf("the error a log line would carry contains the cookie: %v", failErr)
	}

	// Surface: the whole store, not just this one record.
	filesScanned := 0
	err = filepath.Walk(storeDir, func(path string, info os.FileInfo, walkErr error) error {
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
		filesScanned++
		if bytes.Contains(data, []byte(fakeCookie)) {
			t.Errorf("CREDENTIAL LEAK: store file %s contains the cookie", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("error scanning store directory: %v", err)
	}
	if filesScanned == 0 {
		t.Fatal("no store files were scanned")
	}
}

// A LinkedIn fetch is refused before any backend runs when no session cookie is
// configured, so there is no unauthenticated request and no silent failure.
func TestLinkedIn_RefusesWithoutCookieBeforeFetch(t *testing.T) {
	t.Setenv(LinkedInCookieEnv, "")
	t.Setenv(LinkedInMinIntervalEnv, "0")

	payload := []byte(`{"source":"linkedin"}`)
	fixture, _ := sourceFixture(t, payload)
	marker := filepath.Join(t.TempDir(), "payload-fetch-marker")
	t.Setenv(sourceMarkerEnv, marker)

	store := evidence.NewMemoryStore()
	reg := sourceRegistry(t, "linkedin", fixture)

	_, err := FetchQuery(context.Background(), store, reg, "linkedin", "acme")
	if err == nil {
		t.Fatal("expected a LinkedIn fetch without a cookie to be refused")
	}
	if !errors.Is(err, ErrLinkedInCookieMissing) {
		t.Errorf("expected ErrLinkedInCookieMissing, got: %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Error("backend ran despite no cookie being configured")
	}
}

// The rate limit is conservative by default and can be raised through config,
// without a runtime gate.
func TestLinkedIn_ConservativeDefaultRateLimitConfigurable(t *testing.T) {
	if defaultLinkedInMinInterval < time.Second {
		t.Fatalf("default LinkedIn rate limit %v is not conservative", defaultLinkedInMinInterval)
	}

	t.Setenv(LinkedInMinIntervalEnv, "")
	if got := linkedInMinInterval(); got != defaultLinkedInMinInterval {
		t.Errorf("unset config = %v, want the default %v", got, defaultLinkedInMinInterval)
	}

	t.Setenv(LinkedInMinIntervalEnv, "45s")
	if got := linkedInMinInterval(); got != 45*time.Second {
		t.Errorf("configured interval = %v, want 45s", got)
	}

	t.Setenv(LinkedInMinIntervalEnv, "0")
	if got := linkedInMinInterval(); got != 0 {
		t.Errorf("zero interval = %v, want 0", got)
	}

	t.Setenv(LinkedInMinIntervalEnv, "not-a-duration")
	if got := linkedInMinInterval(); got != defaultLinkedInMinInterval {
		t.Errorf("invalid config = %v, want the default %v", got, defaultLinkedInMinInterval)
	}
}

// A rate-limited wait is cancellable, so a long interval never traps a caller
// whose context is done.
func TestLinkedIn_RateLimitHonoursContext(t *testing.T) {
	var limiter intervalLimiter
	limiter.last = time.Now()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := limiter.wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("wait returned %v, want context.Canceled", err)
	}
}

// Done when: 3. The ban-risk disclosure names LinkedIn's specific severity —
// permanent, unwarned bans and litigation against scrapers — not a generic
// warning.
func TestLinkedInBanRiskDisclosureNamesPermanentBanAndLitigation(t *testing.T) {
	for _, want := range []string{
		"against its terms of service",
		"without the password",
		"permanently and without warning",
		"litigated against scrapers",
		LinkedInCookieEnv,
		"never printed",
	} {
		if !strings.Contains(LinkedInBanRiskDisclosure, want) {
			t.Errorf("disclosure does not state %q", want)
		}
	}
}

// Done when: 1. A cookie stored in the keychain is read and used for a fetch,
// without ever needing the environment variable set.
// Uses a fake/mock keychain backend for CI, since a real OS keychain isn't
// scriptable in an automated test.
func TestLinkedIn_CookieFromKeychainUsedForFetchWithoutEnvVar(t *testing.T) {
	mem := keychain.NewMemoryProvider()
	keychain.SetProvider(mem)
	defer keychain.ResetProvider()

	const fakeCookie = "li_at=FAKE-KEYCHAIN-COOKIE-LINKEDIN-0000"
	if err := keychain.SetLinkedInCookie(fakeCookie); err != nil {
		t.Fatalf("failed to store cookie in keychain: %v", err)
	}

	// Environment variable is completely unset
	t.Setenv(LinkedInCookieEnv, "")
	t.Setenv(LinkedInMinIntervalEnv, "0")

	payload := []byte(`{"source":"linkedin","posts":[{"text":"post from keychain session"}]}`)
	fixture, _ := sourceFixture(t, payload)

	argsFile := filepath.Join(t.TempDir(), "received-args.txt")
	t.Setenv(sourceHelperArgsEnv, argsFile)

	store, err := evidence.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	reg := sourceRegistry(t, "linkedin", fixture)

	hash, err := FetchQuery(context.Background(), store, reg, "linkedin", "acme")
	if err != nil {
		t.Fatalf("FetchQuery failed: %v", err)
	}

	received, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("fixture did not record its arguments: %v", err)
	}
	if !strings.Contains(string(received), fakeCookie) {
		t.Fatalf("fixture never received the keychain cookie")
	}

	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}
	if strings.Contains(strings.Join(rec.BackendArgs, " "), fakeCookie) {
		t.Errorf("backend args in record contain unredacted cookie")
	}
}

// Done when: 2. When no keychain is available or empty, the existing environment-variable
// path is unaffected and falls back cleanly.
func TestLinkedIn_FallbackToEnvironmentVariableWhenKeychainEmptyOrUnavailable(t *testing.T) {
	// Empty keychain provider simulates unset keychain / no secret found
	mem := keychain.NewMemoryProvider()
	keychain.SetProvider(mem)
	defer keychain.ResetProvider()

	const fakeCookie = "li_at=FAKE-ENV-COOKIE-LINKEDIN-1111"
	t.Setenv(LinkedInCookieEnv, fakeCookie)
	t.Setenv(LinkedInMinIntervalEnv, "0")

	payload := []byte(`{"source":"linkedin","posts":[{"text":"post from env fallback"}]}`)
	fixture, _ := sourceFixture(t, payload)

	argsFile := filepath.Join(t.TempDir(), "received-args.txt")
	t.Setenv(sourceHelperArgsEnv, argsFile)

	store, err := evidence.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	reg := sourceRegistry(t, "linkedin", fixture)

	_, err = FetchQuery(context.Background(), store, reg, "linkedin", "acme")
	if err != nil {
		t.Fatalf("FetchQuery with env fallback failed: %v", err)
	}

	received, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("fixture did not record arguments: %v", err)
	}
	if !strings.Contains(string(received), fakeCookie) {
		t.Fatalf("fixture never received the environment variable cookie")
	}
}
