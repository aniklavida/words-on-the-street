package evidence

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStore_AppendOnlyRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	payload := []byte("first immutable payload")
	hash := fmt.Sprintf("%x", sha256.Sum256(payload))

	rec := &Record{
		ResolvedURL:    "https://example.com/one",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "curl",
		BackendVersion: "1.0",
		Payload:        payload,
	}

	if err := store.Save(rec); err != nil {
		t.Fatalf("First Save failed: %v", err)
	}

	// Attempting to rewrite/overwrite the same hash must be refused
	recRewrite := &Record{
		ResolvedURL:    "https://example.com/one-different",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "curl",
		BackendVersion: "2.0",
		Payload:        payload,
	}

	err = store.Save(recRewrite)
	if err == nil {
		t.Fatal("expected append-only violation error on overwrite attempt, got nil")
	}
	if !strings.Contains(err.Error(), "append-only violation") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestStore_ModifiedStoreIsDetectable(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	payload := []byte("untampered content bytes")
	hash := fmt.Sprintf("%x", sha256.Sum256(payload))

	rec := &Record{
		ResolvedURL:    "https://example.com/test",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "curl",
		BackendVersion: "1.0",
		Payload:        payload,
	}

	if err := store.Save(rec); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify store is initially intact
	if err := store.VerifyIntegrity(); err != nil {
		t.Fatalf("expected pristine store to pass integrity check: %v", err)
	}

	// Test 1: Tampering with raw blob on disk is detected
	blobPath := filepath.Join(dir, "blobs", hash)
	if err := os.Chmod(blobPath, 0666); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	if err := os.WriteFile(blobPath, []byte("tampered content bytes!"), 0666); err != nil {
		t.Fatalf("tampering blob failed: %v", err)
	}

	_, err = store.Get(hash)
	if err == nil {
		t.Fatal("expected Get to detect tampered blob, got nil")
	}
	if !strings.Contains(err.Error(), "store tampering detected") && !strings.Contains(err.Error(), "payload hash mismatch") {
		t.Errorf("unexpected error for tampered blob: %v", err)
	}

	if err := store.VerifyIntegrity(); err == nil {
		t.Fatal("expected VerifyIntegrity to detect tampered blob, got nil")
	}

	// Restore blob to test record file tampering
	if err := os.WriteFile(blobPath, payload, 0444); err != nil {
		t.Fatalf("restore blob failed: %v", err)
	}

	// Test 2: Tampering with record metadata on disk is detected
	recPath := filepath.Join(dir, "records", hash+".json")
	if err := os.Chmod(recPath, 0666); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	// Tamper: change resolved URL in record JSON
	recBytes, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("read recPath failed: %v", err)
	}
	tamperedRecBytes := bytes.Replace(recBytes, []byte("https://example.com/test"), []byte("https://attacker.com/evil"), 1)
	if err := os.WriteFile(recPath, tamperedRecBytes, 0666); err != nil {
		t.Fatalf("write tampered record failed: %v", err)
	}

	_, err = store.Get(hash)
	if err == nil {
		t.Fatal("expected Get to detect tampered record, got nil")
	}
	if !strings.Contains(err.Error(), "store tampering detected") && !strings.Contains(err.Error(), "record hash mismatch") {
		t.Errorf("unexpected error for tampered record: %v", err)
	}

	if err := store.VerifyIntegrity(); err == nil {
		t.Fatal("expected VerifyIntegrity to detect tampered record file, got nil")
	}
}

func TestStore_ContentAddressing(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	rawPayload := []byte("exact content addressing bytes\r\n\t")
	hash := fmt.Sprintf("%x", sha256.Sum256(rawPayload))

	rec := &Record{
		ResolvedURL:    "https://example.com/content",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "curl",
		BackendVersion: "1.0",
		Payload:        rawPayload,
	}

	if err := store.Save(rec); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file was written to blobs/<hash>
	blobPath := filepath.Join(dir, "blobs", hash)
	blobBytes, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatalf("failed to read blob at expected content address %s: %v", blobPath, err)
	}
	if !bytes.Equal(blobBytes, rawPayload) {
		t.Errorf("content-addressed blob does not match original raw payload")
	}

	// Retrieve via GetRaw
	retrievedRaw, err := store.GetRaw(hash)
	if err != nil {
		t.Fatalf("GetRaw failed: %v", err)
	}
	if !bytes.Equal(retrievedRaw, rawPayload) {
		t.Errorf("GetRaw bytes do not match original raw payload")
	}

	// Retrieve via Get
	retrievedRec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !bytes.Equal(retrievedRec.Payload, rawPayload) {
		t.Errorf("retrievedRec.Payload does not match original raw payload")
	}
	if !bytes.Equal(retrievedRec.RawBytes(), rawPayload) {
		t.Errorf("retrievedRec.RawBytes() does not match original raw payload")
	}
}

func TestStore_MemoryStore(t *testing.T) {
	store := NewMemoryStore()

	payload := []byte("in-memory payload")
	hash := fmt.Sprintf("%x", sha256.Sum256(payload))

	rec := &Record{
		ResolvedURL:    "https://example.com/mem",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "echo",
		BackendVersion: "1.0",
		Payload:        payload,
	}

	if err := store.Save(rec); err != nil {
		t.Fatalf("MemoryStore Save failed: %v", err)
	}

	// Overwrite with different metadata must fail
	recRewrite := *rec
	recRewrite.ResolvedURL = "https://example.com/mem-different"
	if err := store.Save(&recRewrite); err == nil {
		t.Fatal("expected overwrite error in MemoryStore, got nil")
	}

	// Retrieval
	got, err := store.Get(hash)
	if err != nil {
		t.Fatalf("MemoryStore Get failed: %v", err)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Errorf("payload mismatch in MemoryStore")
	}

	// Integrity
	if err := store.VerifyIntegrity(); err != nil {
		t.Errorf("MemoryStore integrity failed: %v", err)
	}
}

func TestStore_RoutingOverrideDistinguishable(t *testing.T) {
	dir := t.TempDir()
	fileStore, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	memStore := NewMemoryStore()

	stores := []struct {
		name  string
		store Store
	}{
		{"FileStore", fileStore},
		{"MemoryStore", memStore},
	}

	for _, s := range stores {
		t.Run(s.name, func(t *testing.T) {
			rawRec := []byte("content from recommended routing")
			hashRec := fmt.Sprintf("%x", sha256.Sum256(rawRec))

			recRecommended := &Record{
				ResolvedURL:     "https://example.com/item1",
				Timestamp:       time.Now().UTC(),
				Hash:            hashRec,
				BackendName:     "curl",
				BackendVersion:  "1.0",
				RoutingOverride: false,
				Payload:         rawRec,
			}

			if err := s.store.Save(recRecommended); err != nil {
				t.Fatalf("Save recommended failed: %v", err)
			}

			rawOverridden := []byte("content from user override routing")
			hashOverridden := fmt.Sprintf("%x", sha256.Sum256(rawOverridden))

			recOverridden := &Record{
				ResolvedURL:           "https://example.com/item2",
				Timestamp:             time.Now().UTC(),
				Hash:                  hashOverridden,
				BackendName:           "curl",
				BackendVersion:        "1.0",
				RoutingOverride:       true,
				RoutingOverrideReason: "Forced by user preference",
				Payload:               rawOverridden,
			}

			if err := s.store.Save(recOverridden); err != nil {
				t.Fatalf("Save overridden failed: %v", err)
			}

			gotRec, err := s.store.Get(hashRec)
			if err != nil {
				t.Fatalf("Get recommended failed: %v", err)
			}
			if gotRec.RoutingOverride {
				t.Errorf("expected RoutingOverride false for recommended fetch, got true")
			}
			if gotRec.RoutingOverrideReason != "" {
				t.Errorf("expected empty RoutingOverrideReason for recommended fetch, got %q", gotRec.RoutingOverrideReason)
			}

			gotOverridden, err := s.store.Get(hashOverridden)
			if err != nil {
				t.Fatalf("Get overridden failed: %v", err)
			}
			if !gotOverridden.RoutingOverride {
				t.Errorf("expected RoutingOverride true for overridden fetch, got false")
			}
			if gotOverridden.RoutingOverrideReason != "Forced by user preference" {
				t.Errorf("expected RoutingOverrideReason %q, got %q", "Forced by user preference", gotOverridden.RoutingOverrideReason)
			}

			// Modifying routing override status for same payload hash must violate append-only
			recTampered := *recRecommended
			recTampered.RoutingOverride = true
			recTampered.RoutingOverrideReason = "Attempted retroactive override modification"
			if err := s.store.Save(&recTampered); err == nil {
				t.Fatalf("expected append-only violation when modifying routing override for existing hash, got nil")
			}
		})
	}
}
