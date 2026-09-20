package fetch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/evidence"
)

func TestFetch_HostileArgsAreInert(t *testing.T) {
	hostileArgs := []string{
		"; rm -rf /",
		"$(whoami)",
		"`whoami`",
		"\n",
		"-leading-dash",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	store := evidence.NewMemoryStore()

	// We use 'echo' as our backend to see exactly what arguments it received.
	// Since exec.Command passes arguments as an array, echo will literally print
	// these exact strings separated by spaces, without interpreting them.
	hash, err := Fetch(ctx, store, "echo", hostileArgs, "https://example.com", "1.0", false)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("Failed to get record: %v", err)
	}

	output := string(rec.Payload)

	for _, hostile := range hostileArgs {
		if !strings.Contains(output, hostile) {
			t.Errorf("Expected output to contain inert hostile argument %q, got: %q", hostile, output)
		}
	}
}

type discardStore struct{}

func (d *discardStore) Save(rec *evidence.Record) error {
	return nil
}

func (d *discardStore) Get(hash string) (*evidence.Record, error) {
	return nil, fmt.Errorf("record not found")
}

func TestFetch_RequiresRecord(t *testing.T) {
	// A test that proves it's impossible to fetch the payload without a working record.
	// We do this by proving that Fetch only returns an ID, so the payload must be
	// retrieved from the store. If the store discards the record, the payload is
	// unretrievable.
	store := &discardStore{}
	hash, err := Fetch(context.Background(), store, "echo", []string{"hi"}, "https://example.com", "1.0", false)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	// We try to get the payload... and cannot!
	_, err = store.Get(hash)
	if err == nil {
		t.Error("Expected error getting discarded payload, got nil")
	}

	// We can also test that passing a nil store fails immediately
	_, err = Fetch(context.Background(), nil, "echo", []string{"hi"}, "https://example.com", "1.0", false)
	if err == nil {
		t.Error("Expected error when fetching without a store, got nil")
	}
}

func TestFetch_AllRequiredFieldsPresent(t *testing.T) {
	ctx := context.Background()
	store := evidence.NewMemoryStore()

	cases := []struct {
		name       string
		isFallback bool
		url        string
		backend    string
		version    string
		payload    string
	}{
		{
			name:       "primary fetch",
			isFallback: false,
			url:        "https://example.com/primary-feed",
			backend:    "echo",
			version:    "1.2.3",
			payload:    "primary feed response content",
		},
		{
			name:       "fallback fetch",
			isFallback: true,
			url:        "https://example.com/fallback-feed",
			backend:    "echo",
			version:    "2.0.0",
			payload:    "fallback feed response content",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beforeFetch := time.Now().UTC().Add(-time.Second)
			hash, err := Fetch(ctx, store, tc.backend, []string{tc.payload}, tc.url, tc.version, tc.isFallback)
			if err != nil {
				t.Fatalf("Fetch failed: %v", err)
			}
			afterFetch := time.Now().UTC().Add(time.Second)

			rec, err := store.Get(hash)
			if err != nil {
				t.Fatalf("store.Get failed: %v", err)
			}

			// 1. Resolved URL
			if rec.ResolvedURL != tc.url {
				t.Errorf("ResolvedURL mismatch: got %q, want %q", rec.ResolvedURL, tc.url)
			}

			// 2. UTC Timestamp
			if rec.Timestamp.IsZero() {
				t.Errorf("Timestamp is zero")
			}
			if rec.Timestamp.Location() != time.UTC {
				t.Errorf("Timestamp location must be UTC, got %v", rec.Timestamp.Location())
			}
			if rec.Timestamp.Before(beforeFetch) || rec.Timestamp.After(afterFetch) {
				t.Errorf("Timestamp %v not within expected window [%v, %v]", rec.Timestamp, beforeFetch, afterFetch)
			}

			// 3. Content hash of bytes as received
			expectedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(tc.payload+"\n")))
			if rec.Hash != expectedHash {
				t.Errorf("Hash mismatch: got %q, want %q", rec.Hash, expectedHash)
			}
			if hash != expectedHash {
				t.Errorf("Fetch return hash mismatch: got %q, want %q", hash, expectedHash)
			}

			// 4. Backend name and version
			if rec.BackendName != tc.backend {
				t.Errorf("BackendName mismatch: got %q, want %q", rec.BackendName, tc.backend)
			}
			version := rec.BackendVersion
			if version == "" {
				version = rec.Version
			}
			if version != tc.version {
				t.Errorf("BackendVersion mismatch: got %q, want %q", version, tc.version)
			}

			// 5. Path taken (primary vs fallback)
			if rec.IsFallback != tc.isFallback {
				t.Errorf("IsFallback mismatch: got %v, want %v", rec.IsFallback, tc.isFallback)
			}

			// Raw bytes retrievable
			if string(rec.Payload) != tc.payload+"\n" {
				t.Errorf("Payload mismatch: got %q, want %q", string(rec.Payload), tc.payload+"\n")
			}
		})
	}
}

func TestFetch_HashCoversRawBytesNotNormalised(t *testing.T) {
	ctx := context.Background()
	store := evidence.NewMemoryStore()

	// Normaliser that strips whitespace, comments and normalises line endings
	normaliser := func(raw []byte) ([]byte, error) {
		trimmed := strings.TrimSpace(string(raw))
		return []byte(trimmed), nil
	}

	rawText := "  raw incoming bytes with trailing whitespace and newline \n\n  "
	hash, err := Fetch(ctx, store, "echo", []string{rawText}, "https://example.com/item", "1.0", false,
		WithNormaliser("trim", normaliser))
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}

	rawBytes := rec.RawBytes()
	normBytes := rec.NormalisedPayload

	if bytes.Equal(rawBytes, normBytes) {
		t.Fatalf("Test setup error: raw bytes and normalised bytes must be different to prove hash differentiation")
	}

	rawHash := fmt.Sprintf("%x", sha256.Sum256(rawBytes))
	normHash := fmt.Sprintf("%x", sha256.Sum256(normBytes))

	if rawHash == normHash {
		t.Fatalf("Test setup error: raw hash and normalised hash must differ")
	}

	// Constraint: Hash MUST cover bytes as received before normalisation.
	// If someone changed Fetch to hash normalised output, rec.Hash == normHash, which fails here.
	if rec.Hash != rawHash {
		t.Errorf("rec.Hash %q does not match raw bytes hash %q", rec.Hash, rawHash)
	}
	if rec.Hash == normHash {
		t.Errorf("rec.Hash %q matches normalised hash; must hash bytes as received before normalisation", rec.Hash)
	}
	if hash != rawHash {
		t.Errorf("Fetch returned hash %q, expected raw bytes hash %q", hash, rawHash)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	fmt.Println(`{"opinions": ["public comment one", "public comment two"]}`)
	os.Exit(0)
}

func TestFetch_CookieBearingFetchLeavesNoCredentialInStore(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")

	storeDir := t.TempDir()
	store, err := evidence.NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	// Secret credentials that MUST NOT enter the store anywhere
	fakeCookieVal := "test-cookie-secret-xyz-987654321"
	fakeAuthToken := "test-bearer-secret-token-abcdef-123456"
	fakePassword := "test-secret-password-xyz-45678"
	fakeQueryToken := "test-query-token-value-889900"
	fakeAPIKey := "test-api-key-value-445566"

	credentials := []string{
		fakeCookieVal,
		fakeAuthToken,
		fakePassword,
		fakeQueryToken,
		fakeAPIKey,
	}

	args := []string{
		"-test.run=^TestHelperProcess$",
		"--",
		"--cookie", "session_id=" + fakeCookieVal,
		"-H", "Cookie: auth_token=" + fakeCookieVal,
		"-H", "Authorization: Bearer " + fakeAuthToken,
		"--token=" + fakeQueryToken,
	}
	urlWithCreds := fmt.Sprintf("https://testuser:%s@example.com/api?token=%s&api_key=%s",
		fakePassword, fakeQueryToken, fakeAPIKey)

	ctx := context.Background()
	hash, err := Fetch(ctx, store, os.Args[0], args, urlWithCreds, "1.0", false)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}

	// Check record fields
	for _, cred := range credentials {
		if strings.Contains(rec.ResolvedURL, cred) {
			t.Errorf("ResolvedURL contains credential %q: %s", cred, rec.ResolvedURL)
		}
		for _, arg := range rec.BackendArgs {
			if strings.Contains(arg, cred) {
				t.Errorf("BackendArgs contains credential %q: %s", cred, arg)
			}
		}
	}

	// SEARCH THE WHOLE STORE:
	// Walk every file in storeDir and check that none contains any credential value.
	filesScanned := 0
	err = filepath.Walk(storeDir, func(path string, info fs.FileInfo, walkErr error) error {
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

		for _, cred := range credentials {
			if bytes.Contains(data, []byte(cred)) {
				t.Errorf("CREDENTIAL LEAK DETECTED: file %s contains credential %q", path, cred)
			}
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

func TestFetch_RawBytesRetrievableAfterNormalisation(t *testing.T) {
	storeDir := t.TempDir()
	store, err := evidence.NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	rawText := "Original raw street opinion with \r\n line endings and   spaces\t\n"
	normaliser := func(raw []byte) ([]byte, error) {
		// Normalise CRLF to LF, collapse spaces
		s := strings.ReplaceAll(string(raw), "\r\n", "\n")
		s = strings.Join(strings.Fields(s), " ")
		return []byte(s), nil
	}

	ctx := context.Background()
	hash, err := Fetch(ctx, store, "echo", []string{rawText}, "https://example.com/opinion", "1.0", false,
		WithNormaliser("clean-text", normaliser))
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}

	// 1. Verify normalisation is present and recorded separately
	if rec.Normalisation == nil {
		t.Fatal("rec.Normalisation is nil; normalisation must be recorded")
	}
	if rec.Normalisation.Name != "clean-text" {
		t.Errorf("Normalisation name mismatch: got %q, want %q", rec.Normalisation.Name, "clean-text")
	}
	expectedNorm := "Original raw street opinion with line endings and spaces"
	if string(rec.NormalisedPayload) != expectedNorm {
		t.Errorf("NormalisedPayload mismatch: got %q, want %q", string(rec.NormalisedPayload), expectedNorm)
	}

	// 2. Verify raw bytes are retrievable from the record after normalisation
	rawFromRecord := rec.RawBytes()
	if !strings.Contains(string(rawFromRecord), "Original raw street opinion with \r\n") {
		t.Errorf("Raw bytes not preserved in record: got %q", string(rawFromRecord))
	}
	if !bytes.Equal(rawFromRecord, rec.Payload) {
		t.Errorf("rec.Payload != rec.RawBytes()")
	}

	// 3. Verify raw bytes are retrievable directly from the store content-addressed
	rawFromStore, err := store.GetRaw(hash)
	if err != nil {
		t.Fatalf("store.GetRaw failed: %v", err)
	}
	if !bytes.Equal(rawFromStore, rawFromRecord) {
		t.Errorf("store.GetRaw bytes do not match raw bytes from record")
	}

	// 4. Verify raw bytes were not destroyed or overwritten by normalisation
	if bytes.Equal(rawFromRecord, rec.NormalisedPayload) {
		t.Error("raw bytes and normalised payload should be distinct")
	}
}
