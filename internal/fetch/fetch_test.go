package fetch

import (
	"context"
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
	rec, err := Fetch(ctx, store, "echo", hostileArgs, "https://example.com", "1.0", false)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	output := string(rec.Payload)

	for _, hostile := range hostileArgs {
		if !strings.Contains(output, hostile) {
			t.Errorf("Expected output to contain inert hostile argument %q, got: %q", hostile, output)
		}
	}
}

func TestFetch_RequiresRecord(t *testing.T) {
	// A test that proves it's impossible to fetch without returning a record.
	// We do this by ensuring the only public API is Fetch, which returns the
	// record type. Since Go doesn't let us dynamically add functions or return types,
	// the type signature itself is the proof.
	
	// We can also test that passing a nil store fails, showing we can't bypass storage.
	_, err := Fetch(context.Background(), nil, "echo", []string{"hi"}, "https://example.com", "1.0", false)
	if err == nil {
		t.Error("Expected error when fetching without a store, got nil")
	}
}
