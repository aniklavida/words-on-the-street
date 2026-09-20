package fetch

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/evidence"
)

type fetchOptions struct {
	normaliser     evidence.NormaliseFunc
	normaliserName string
}

// Option configures fetch behavior.
type Option func(*fetchOptions)

// WithNormaliser specifies a normalisation transformation to record alongside raw bytes.
func WithNormaliser(name string, fn evidence.NormaliseFunc) Option {
	return func(o *fetchOptions) {
		o.normaliser = fn
		o.normaliserName = name
	}
}

// Fetch performs a fetch and records the evidence in one operation.
// It returns the hash of the recorded evidence, making it impossible
// to retrieve the payload without successfully writing the evidence record first.
func Fetch(ctx context.Context, store evidence.Store, backend string, args []string, url string, version string, isFallback bool, opts ...Option) (string, error) {
	if store == nil {
		return "", fmt.Errorf("store is required")
	}

	var options fetchOptions
	for _, opt := range opts {
		opt(&options)
	}

	cmd := exec.CommandContext(ctx, backend, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("backend fetch failed: %w", err)
	}

	// Content hash of the bytes AS RECEIVED, before any normalisation
	rawBytes := out
	rawHash := fmt.Sprintf("%x", sha256.Sum256(rawBytes))

	// Sanitize URL and backend arguments so credentials never enter the record
	cleanURL := evidence.SanitizeURL(url)
	cleanArgs := evidence.SanitizeArgs(args)

	rec := &evidence.Record{
		ResolvedURL:    cleanURL,
		Timestamp:      time.Now().UTC(),
		Hash:           rawHash,
		BackendName:    backend,
		BackendVersion: version,
		Version:        version,
		IsFallback:     isFallback,
		Payload:        rawBytes,
		RawPayload:     rawBytes,
		BackendArgs:    cleanArgs,
	}

	// Normalisation is recorded separately so it never destroys what arrived
	if options.normaliser != nil {
		normBytes, err := options.normaliser(rawBytes)
		if err != nil {
			return "", fmt.Errorf("normalisation failed: %w", err)
		}
		normHash := fmt.Sprintf("%x", sha256.Sum256(normBytes))
		rec.Normalisation = &evidence.NormalisationRecord{
			Name:      options.normaliserName,
			Hash:      normHash,
			Payload:   normBytes,
			Timestamp: time.Now().UTC(),
		}
		rec.NormalisedPayload = normBytes
	}

	if err := store.Save(rec); err != nil {
		return "", fmt.Errorf("failed to save evidence: %w", err)
	}

	return rawHash, nil
}
