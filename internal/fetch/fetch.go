package fetch

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/evidence"
)

// Fetch performs a fetch and records the evidence in one operation.
// It returns the hash of the recorded evidence, making it impossible
// to retrieve the payload without successfully writing the evidence record first.
func Fetch(ctx context.Context, store evidence.Store, backend string, args []string, url string, version string, isFallback bool) (string, error) {
	if store == nil {
		return "", fmt.Errorf("store is required")
	}

	cmd := exec.CommandContext(ctx, backend, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("backend fetch failed: %w", err)
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(out))

	rec := &evidence.Record{
		ResolvedURL: url,
		Timestamp:   time.Now().UTC(),
		Hash:        hash,
		BackendName: backend,
		Version:     version,
		IsFallback:  isFallback,
		Payload:     out,
	}

	if err := store.Save(rec); err != nil {
		return "", fmt.Errorf("failed to save evidence: %w", err)
	}

	return hash, nil
}
