package verify

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

	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/fetch"
)

// TestVerifyHelperProcess is re-executed by the tests as a portable fixture
// backend. It serves the bytes currently held in the file named by
// VERIFY_FIXTURE_FILE, so a test can mutate the source between runs. When that
// file no longer exists the fixture exits with ExitGone, which is how a deleted
// source is signalled without a shell script and without platform-specific code.
func TestVerifyHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_VERIFY_HELPER") != "1" {
		return
	}
	for _, arg := range os.Args {
		if arg == "--version" {
			fmt.Println("verify-fixture version 1.0.0")
			os.Exit(0)
		}
	}

	path := os.Getenv("VERIFY_FIXTURE_FILE")
	if path == "" {
		fmt.Fprintln(os.Stderr, "VERIFY_FIXTURE_FILE is not set")
		os.Exit(2)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "fixture source is gone")
			os.Exit(ExitGone)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Stdout.Write(data)
	os.Exit(0)
}

func verifyHelperArgs() []string {
	return []string{"-test.run=^TestVerifyHelperProcess$", "--"}
}

func sha256Hex(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// newVerifyFixture performs the initial fetch of a mutable fixture source and
// returns the file store, the fixture file path, and the recorded hash.
func newVerifyFixture(t *testing.T) (*evidence.FileStore, string, string) {
	t.Helper()
	t.Setenv("GO_WANT_VERIFY_HELPER", "1")

	fixturePath := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(fixturePath, []byte("original street opinion\nsecond line\n"), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	t.Setenv("VERIFY_FIXTURE_FILE", fixturePath)

	store, err := evidence.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hash, err := fetch.Fetch(ctx, store, os.Args[0], verifyHelperArgs(), "https://example.com/street/opinion", "1.0.0", false)
	if err != nil {
		t.Fatalf("initial fetch failed: %v", err)
	}
	return store, fixturePath, hash
}

func mustVerify(t *testing.T, store evidence.Store, originalHash string) *Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := Verify(ctx, store, CommandRefetcher{}, originalHash)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	return res
}

// Done when: 1. Identical, changed and gone are distinguished against a fixture
// mutated between runs.
func TestVerify_DistinguishesIdenticalChangedAndGone(t *testing.T) {
	store, fixturePath, originalHash := newVerifyFixture(t)

	identical := mustVerify(t, store, originalHash)
	if identical.State != Identical {
		t.Fatalf("expected state %q, got %q", Identical, identical.State)
	}
	if identical.CurrentHash != originalHash {
		t.Errorf("identical check reported current hash %q, want %q", identical.CurrentHash, originalHash)
	}

	if err := os.WriteFile(fixturePath, []byte("edited street opinion\nsecond line\n"), 0o644); err != nil {
		t.Fatalf("failed to mutate fixture: %v", err)
	}
	changed := mustVerify(t, store, originalHash)
	if changed.State != Changed {
		t.Fatalf("expected state %q after mutation, got %q", Changed, changed.State)
	}

	if err := os.Remove(fixturePath); err != nil {
		t.Fatalf("failed to delete fixture: %v", err)
	}
	gone := mustVerify(t, store, originalHash)
	if gone.State != Gone {
		t.Fatalf("expected state %q after deletion, got %q", Gone, gone.State)
	}
}

// Done when: 2. A changed page reports what changed, not only that it did. The
// report carries both hashes and a line diff.
func TestVerify_ChangedReportsWhatChanged(t *testing.T) {
	store, fixturePath, originalHash := newVerifyFixture(t)

	newContent := []byte("edited street opinion\na brand new line\n")
	if err := os.WriteFile(fixturePath, newContent, 0o644); err != nil {
		t.Fatalf("failed to mutate fixture: %v", err)
	}

	result := mustVerify(t, store, originalHash)
	if result.State != Changed {
		t.Fatalf("expected state %q, got %q", Changed, result.State)
	}
	if result.OriginalHash == result.CurrentHash {
		t.Errorf("old and new hashes are both %q; a change must be visible", result.OriginalHash)
	}
	if result.CurrentHash != sha256Hex(newContent) {
		t.Errorf("current hash %q does not match the mutated bytes hash %q", result.CurrentHash, sha256Hex(newContent))
	}
	if result.Diff == "" {
		t.Fatal("changed report has no diff; a bare boolean is not enough")
	}
	for _, want := range []string{"-original street opinion", "+edited street opinion", "+a brand new line"} {
		if !strings.Contains(result.Diff, want) {
			t.Errorf("diff does not show %q:\n%s", want, result.Diff)
		}
	}
}

// Done when: 3. A deleted source is reported as gone, and the stored bytes are
// still retrievable. Deletion at the source never touches the record.
func TestVerify_GoneStillRetrievableFromStore(t *testing.T) {
	store, fixturePath, originalHash := newVerifyFixture(t)

	originalBytes, err := store.GetRaw(originalHash)
	if err != nil {
		t.Fatalf("failed to read original bytes before deletion: %v", err)
	}

	if err := os.Remove(fixturePath); err != nil {
		t.Fatalf("failed to delete fixture: %v", err)
	}

	result := mustVerify(t, store, originalHash)
	if result.State != Gone {
		t.Fatalf("expected state %q, got %q", Gone, result.State)
	}

	storedBytes, err := store.GetRaw(originalHash)
	if err != nil {
		t.Fatalf("original bytes unretrievable after a gone verification: %v", err)
	}
	if !bytes.Equal(storedBytes, originalBytes) {
		t.Errorf("stored bytes changed after a gone verification")
	}

	record, err := store.Get(originalHash)
	if err != nil {
		t.Fatalf("original record unretrievable after a gone verification: %v", err)
	}
	if !bytes.Equal(record.RawBytes(), originalBytes) {
		t.Errorf("record raw bytes differ from the original bytes after a gone verification")
	}
}

// Done when: 4. Verification never overwrites the original record. A changed
// verification writes a new observation and leaves the original byte-identical.
func TestVerify_NeverOverwritesOriginalRecord(t *testing.T) {
	store, fixturePath, originalHash := newVerifyFixture(t)

	recordPath := filepath.Join(store.Dir(), "records", originalHash+".json")
	beforeBytes, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("failed to read original record file: %v", err)
	}
	beforeHash := sha256Hex(beforeBytes)

	originalRecord, err := store.Get(originalHash)
	if err != nil {
		t.Fatalf("failed to load original record: %v", err)
	}
	beforeRecordHash := originalRecord.RecordHash

	if err := os.WriteFile(fixturePath, []byte("changed content that will be recorded separately\n"), 0o644); err != nil {
		t.Fatalf("failed to mutate fixture: %v", err)
	}

	result := mustVerify(t, store, originalHash)
	if result.State != Changed {
		t.Fatalf("expected state %q, got %q", Changed, result.State)
	}

	afterBytes, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("failed to read original record file after verification: %v", err)
	}
	if sha256Hex(afterBytes) != beforeHash || !bytes.Equal(afterBytes, beforeBytes) {
		t.Errorf("original record file was modified by a verification that found a change")
	}

	afterRecord, err := store.Get(originalHash)
	if err != nil {
		t.Fatalf("failed to reload original record after verification: %v", err)
	}
	if afterRecord.RecordHash != beforeRecordHash {
		t.Errorf("original record hash changed: before %q, after %q", beforeRecordHash, afterRecord.RecordHash)
	}

	if result.ObservationRecordHash == "" {
		t.Error("verification did not write a new observation record")
	}
	if result.ObservationRecordHash == beforeRecordHash {
		t.Error("observation reused the original record hash instead of writing a new record")
	}
}
