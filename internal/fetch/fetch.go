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
// It is impossible to retrieve the payload without the evidence record.
func Fetch(ctx context.Context, store evidence.Store, backend string, args []string, url string, version string, isFallback bool) (*evidence.Record, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}

	cmd := exec.CommandContext(ctx, backend, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("backend fetch failed: %w", err)
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
		return nil, fmt.Errorf("failed to save evidence: %w", err)
	}

	return rec, nil
}
