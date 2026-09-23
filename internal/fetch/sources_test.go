package fetch

import (
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
	"github.com/aniklavida/words-on-the-street/internal/verify"
)

const (
	sourceHelperEnv  = "GO_WANT_SOURCE_HELPER"
	sourceFixtureEnv = "SOURCE_FIXTURE_FILE"
	sourceVersionEnv = "SOURCE_HELPER_VERSION"
	sourceMarkerEnv  = "SOURCE_HELPER_MARKER"
	sourceVersion    = "1.0.0"
)

// TestSourceHelperProcess is re-executed by the tests as a portable fixture
// backend standing in for the network. It serves the bytes currently held in the
// file named by SOURCE_FIXTURE_FILE, so a test can mutate the "source" between
// runs; if that file is removed it exits with verify.ExitGone, which is how a
// deleted source is signalled without a shell script and without any
// platform-specific code. A payload fetch also touches SOURCE_HELPER_MARKER when
// that is set, so a test can prove the backend was never executed.
func TestSourceHelperProcess(t *testing.T) {
	if os.Getenv(sourceHelperEnv) != "1" {
		return
	}
	for _, arg := range os.Args {
		if arg == "--version" {
			version := os.Getenv(sourceVersionEnv)
			if version == "" {
				version = sourceVersion
			}
			fmt.Printf("source-fixture version %s\n", version)
			os.Exit(0)
		}
	}

	if marker := os.Getenv(sourceMarkerEnv); marker != "" {
		if err := os.WriteFile(marker, []byte("payload fetch executed"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

	// When set, dump the exact arguments this process received. A test uses this
	// to prove a credential really was sent to the backend, so that a later
	// assertion that it is absent from the record is not vacuous.
	if argsFile := os.Getenv(sourceHelperArgsEnv); argsFile != "" {
		if err := os.WriteFile(argsFile, []byte(strings.Join(os.Args, "\n")), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

	path := os.Getenv(sourceFixtureEnv)
	if path == "" {
		fmt.Fprintln(os.Stderr, sourceFixtureEnv+" is not set")
		os.Exit(2)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "fixture source is gone")
			os.Exit(verify.ExitGone)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Stdout.Write(data)
	os.Exit(0)
}

// sourceFixture installs a fixture backend that re-executes this test binary as
// TestSourceHelperProcess, and returns it together with the file the fixture
// serves. It uses the test-helper-process pattern rather than any shell script so
// it runs identically on Linux, macOS and Windows.
func sourceFixture(t *testing.T, payload []byte) (backend.Backend, string) {
	t.Helper()
	t.Setenv(sourceHelperEnv, "1")
	t.Setenv(sourceVersionEnv, sourceVersion)

	fixturePath := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(fixturePath, payload, 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	t.Setenv(sourceFixtureEnv, fixturePath)

	exe, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("failed to resolve test binary path: %v", err)
	}

	// Name and Command are the same: the record names the process that actually
	// served the bytes, and verify re-invokes exactly that process later.
	return backend.Backend{
		Name:         exe,
		Command:      exe,
		Args:         []string{"-test.run=^TestSourceHelperProcess$", "--"},
		VersionArgs:  []string{"-test.run=^TestSourceHelperProcess$", "--", "--version"},
		VersionRange: ">= 1.0.0",
		Licence:      "MIT",
	}, fixturePath
}

// sourceRegistry returns the real shipped registry with one source's backends
// replaced by the fixture. Everything else about the registry is untouched.
func sourceRegistry(t *testing.T, source string, b backend.Backend) *backend.Registry {
	t.Helper()
	reg := backend.DefaultRegistry()
	if err := reg.RegisterSource(source, b); err != nil {
		t.Fatalf("RegisterSource failed: %v", err)
	}
	return reg
}

func TestSources_ResolveQueryToConcreteURL(t *testing.T) {
	cases := []struct {
		source  string
		query   string
		wantURL string
		wantErr bool
	}{
		{"hacker-news", "golang", "https://hn.algolia.com/api/v1/search?query=golang", false},
		{"hacker-news", "go lang", "https://hn.algolia.com/api/v1/search?query=go+lang", false},
		{"hacker-news", "39123456", "https://hn.algolia.com/api/v1/items/39123456", false},
		{"hacker-news", "   ", "", true},
		{"lobsters", "golang", "https://lobste.rs/search?q=golang&what=stories&order=newest", false},
		{"lobsters", "tag:rust", "https://lobste.rs/t/rust.json", false},
		{"lobsters", "tag:../etc", "", true},
		{"lobsters", "hottest", "https://lobste.rs/hottest.json", false},
		{"lobsters", "", "https://lobste.rs/hottest.json", false},
	}

	for _, tc := range cases {
		t.Run(tc.source+"/"+tc.query, func(t *testing.T) {
			src, ok := LookupSource(tc.source)
			if !ok {
				t.Fatalf("source %q has no resolver", tc.source)
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
				t.Errorf("Resolve(%q) args %v must end with the resolved URL %q so the backend actually fetches it",
					tc.query, req.Args, tc.wantURL)
			}
		})
	}
}

// Done when: 1. A hacker-news fetch and a lobsters fetch each produce a complete
// evidence record through the real registry and store.
func TestFetchQuery_EachSourceRecordsCompleteEvidence(t *testing.T) {
	payload := []byte(`{"bytes":"received as-is"}`)
	cases := []struct {
		source  string
		query   string
		wantURL string
	}{
		{"hacker-news", "golang", "https://hn.algolia.com/api/v1/search?query=golang"},
		{"lobsters", "golang", "https://lobste.rs/search?q=golang&what=stories&order=newest"},
	}

	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			fixture, _ := sourceFixture(t, payload)

			store, err := evidence.NewFileStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewFileStore failed: %v", err)
			}
			reg := sourceRegistry(t, tc.source, fixture)

			before := time.Now().UTC().Add(-time.Second)
			hash, err := FetchQuery(context.Background(), store, reg, tc.source, tc.query)
			if err != nil {
				t.Fatalf("FetchQuery failed: %v", err)
			}
			after := time.Now().UTC().Add(time.Second)

			rec, err := store.Get(hash)
			if err != nil {
				t.Fatalf("store.Get failed: %v", err)
			}

			expectedHash := fmt.Sprintf("%x", sha256.Sum256(payload))
			if rec.Hash != expectedHash {
				t.Errorf("hash %q does not cover the bytes as received (%q)", rec.Hash, expectedHash)
			}
			if hash != expectedHash {
				t.Errorf("FetchQuery returned hash %q, want %q", hash, expectedHash)
			}
			if rec.ResolvedURL != tc.wantURL {
				t.Errorf("resolved URL %q, want %q", rec.ResolvedURL, tc.wantURL)
			}
			// The recorded URL must be the URL the backend was actually invoked
			// with; a record naming a URL that was never fetched is a false claim.
			foundURLArg := false
			for _, arg := range rec.BackendArgs {
				if arg == tc.wantURL {
					foundURLArg = true
					break
				}
			}
			if !foundURLArg {
				t.Errorf("backend args %v do not include the resolved URL %q; the recorded URL was never fetched",
					rec.BackendArgs, tc.wantURL)
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
		})
	}
}

// Done when: 2. verify can re-check an entry produced by either source and
// correctly report identical, changed, or gone. The initial fetch and the checks
// are chained in a single test against the same record.
func TestFetchQuery_VerifyChainsInitialFetchForEachSource(t *testing.T) {
	cases := []struct {
		source string
		query  string
	}{
		{"hacker-news", "golang"},
		{"lobsters", "golang"},
	}

	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			original := []byte(`{"opinion":"the original bytes"}`)
			fixture, fixturePath := sourceFixture(t, original)

			store, err := evidence.NewFileStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewFileStore failed: %v", err)
			}
			reg := sourceRegistry(t, tc.source, fixture)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			hash, err := FetchQuery(ctx, store, reg, tc.source, tc.query)
			if err != nil {
				t.Fatalf("FetchQuery failed: %v", err)
			}

			identical, err := verify.Verify(ctx, store, verify.CommandRefetcher{}, hash)
			if err != nil {
				t.Fatalf("verify (identical) failed: %v", err)
			}
			if identical.State != verify.Identical {
				t.Fatalf("expected state %q, got %q", verify.Identical, identical.State)
			}
			if identical.CurrentHash != hash {
				t.Errorf("identical current hash %q, want %q", identical.CurrentHash, hash)
			}

			changedBytes := []byte(`{"opinion":"the edited bytes"}`)
			if err := os.WriteFile(fixturePath, changedBytes, 0o644); err != nil {
				t.Fatalf("failed to mutate fixture: %v", err)
			}
			changed, err := verify.Verify(ctx, store, verify.CommandRefetcher{}, hash)
			if err != nil {
				t.Fatalf("verify (changed) failed: %v", err)
			}
			if changed.State != verify.Changed {
				t.Fatalf("expected state %q, got %q", verify.Changed, changed.State)
			}
			if changed.CurrentHash != fmt.Sprintf("%x", sha256.Sum256(changedBytes)) {
				t.Errorf("changed current hash %q does not match the mutated bytes", changed.CurrentHash)
			}

			if err := os.Remove(fixturePath); err != nil {
				t.Fatalf("failed to delete fixture: %v", err)
			}
			gone, err := verify.Verify(ctx, store, verify.CommandRefetcher{}, hash)
			if err != nil {
				t.Fatalf("verify (gone) failed: %v", err)
			}
			if gone.State != verify.Gone {
				t.Fatalf("expected state %q, got %q", verify.Gone, gone.State)
			}
			if _, err := store.GetRaw(hash); err != nil {
				t.Errorf("original bytes unretrievable after a gone check: %v", err)
			}
		})
	}
}

// Done when: 4. Health and version checks are consulted before a fetch runs. A
// backend reporting an unhealthy or wrong-version state is refused rather than
// used, proven by the fixture never being executed for a payload fetch.
func TestFetchQuery_RefusesUnhealthyOrWrongVersionBackendBeforeFetch(t *testing.T) {
	cases := []struct {
		name       string
		wantStatus string
		setup      func(t *testing.T, b *backend.Backend)
	}{
		{
			name:       "wrong-version",
			wantStatus: string(backend.StatusWrongVersion),
			setup: func(t *testing.T, b *backend.Backend) {
				t.Setenv(sourceVersionEnv, "0.1.0")
			},
		},
		{
			name:       "unreachable",
			wantStatus: string(backend.StatusUnreachable),
			setup: func(t *testing.T, b *backend.Backend) {
				b.Command = filepath.Join(t.TempDir(), "not-an-installed-backend")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture, _ := sourceFixture(t, []byte("this payload must never be fetched"))
			marker := filepath.Join(t.TempDir(), "payload-fetch-marker")
			t.Setenv(sourceMarkerEnv, marker)
			tc.setup(t, &fixture)

			store, err := evidence.NewFileStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewFileStore failed: %v", err)
			}
			reg := sourceRegistry(t, "hacker-news", fixture)

			_, err = FetchQuery(context.Background(), store, reg, "hacker-news", "golang")
			if err == nil {
				t.Fatal("expected fetch to refuse a backend that failed its health check, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantStatus) {
				t.Errorf("expected error to name status %q, got: %v", tc.wantStatus, err)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Errorf("backend ran a payload fetch despite failing its health check (status %q)", tc.wantStatus)
			}
		})
	}
}

// An unknown source is refused at resolution, before any backend can run.
func TestFetchQuery_UnknownSourceRefusedBeforeFetch(t *testing.T) {
	store := evidence.NewMemoryStore()
	reg := backend.DefaultRegistry()

	_, err := FetchQuery(context.Background(), store, reg, "not-a-source", "golang")
	if err == nil {
		t.Fatal("expected unknown source to be refused, got nil")
	}
	if !strings.Contains(err.Error(), "unknown source") {
		t.Errorf("expected an unknown-source error, got: %v", err)
	}
}
