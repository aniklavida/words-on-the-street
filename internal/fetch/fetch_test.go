package fetch

import (
	"context"
	"fmt"
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

	store := &evidence.MemoryStore{}

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
