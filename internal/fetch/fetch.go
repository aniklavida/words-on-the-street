package fetch

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/backend"
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
// A missing backend produces a recorded state in the evidence record, never a silent skip or nil.
func Fetch(ctx context.Context, store evidence.Store, backendCmd string, args []string, url string, version string, isFallback bool, opts ...Option) (string, error) {
	if store == nil {
		return "", fmt.Errorf("store is required")
	}

	var options fetchOptions
	for _, opt := range opts {
		opt(&options)
	}

	cleanURL := evidence.SanitizeURL(url)
	cleanArgs := evidence.SanitizeArgs(args)

	// Check if backend executable exists
	if _, lookErr := exec.LookPath(backendCmd); lookErr != nil {
		payload := []byte(fmt.Sprintf("backend %q is unreachable: %v", backendCmd, lookErr))
		rawHash := fmt.Sprintf("%x", sha256.Sum256(payload))
		rec := &evidence.Record{
			ResolvedURL:     cleanURL,
			Timestamp:       time.Now().UTC(),
			Hash:            rawHash,
			BackendName:     backendCmd,
			BackendVersion:  "missing",
			Version:         "missing",
			BackendStatus:   string(backend.StatusUnreachable),
			IsFallback:      isFallback,
			Payload:         payload,
			RawPayload:      payload,
			BackendArgs:     cleanArgs,
			MissingBackends: []string{backendCmd},
			BackendAttempts: []evidence.BackendAttempt{
				{
					BackendName: backendCmd,
					Status:      string(backend.StatusUnreachable),
					Error:       lookErr.Error(),
				},
			},
		}
		if err := store.Save(rec); err != nil {
			return "", fmt.Errorf("backend fetch failed (%v) and store save failed: %w", lookErr, err)
		}
		return rawHash, fmt.Errorf("backend fetch failed: backend %q is missing: %w", backendCmd, lookErr)
	}

	cmd := exec.CommandContext(ctx, backendCmd, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("backend fetch failed: %w", err)
	}

	// Content hash of the bytes AS RECEIVED, before any normalisation
	rawBytes := out
	rawHash := fmt.Sprintf("%x", sha256.Sum256(rawBytes))

	rec := &evidence.Record{
		ResolvedURL:    cleanURL,
		Timestamp:      time.Now().UTC(),
		Hash:           rawHash,
		BackendName:    backendCmd,
		BackendVersion: version,
		Version:        version,
		BackendStatus:  string(backend.StatusReachable),
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

// FetchSource fetches a URL for a registered source by trying its backends in failover order.
// A missing backend is never silently skipped: its missing state reaches the evidence record.
// An unexpected backend version is reported and never silently accepted.
func FetchSource(ctx context.Context, store evidence.Store, reg *backend.Registry, source string, url string, args []string, opts ...Option) (string, error) {
	if store == nil {
		return "", fmt.Errorf("store is required")
	}
	if reg == nil {
		return "", fmt.Errorf("registry is required")
	}

	backends, ok := reg.BackendsForSource(source)
	if !ok || len(backends) == 0 {
		return "", fmt.Errorf("unknown source or no backends registered for %q", source)
	}

	var options fetchOptions
	for _, opt := range opts {
		opt(&options)
	}

	cleanURL := evidence.SanitizeURL(url)
	var attempts []evidence.BackendAttempt
	var missingBackends []string

	for i, b := range backends {
		isFallback := (i > 0)

		// 1-2. Consult the backend's health check before retrieving anything.
		// CheckHealth verifies the command exists and its detected version falls
		// inside the declared range, returning a structured report. An unhealthy
		// or wrong-version backend is reported and never used to fetch bytes.
		report := b.CheckHealth(ctx)
		if report.Status != backend.StatusReachable {
			attempts = append(attempts, evidence.BackendAttempt{
				BackendName: b.Name,
				Status:      string(report.Status),
				Error:       report.Error,
			})
			missingBackends = append(missingBackends, b.Name)

			// If this was the final backend in failover order, record the failed state in store
			if i == len(backends)-1 {
				payload := []byte(fmt.Sprintf("all backends failed for source %q; backend %q is %s: %s", source, b.Name, report.Status, report.Error))
				rawHash := fmt.Sprintf("%x", sha256.Sum256(payload))
				version := report.DetectedVersion
				if version == "" {
					version = string(report.Status)
				}
				rec := &evidence.Record{
					ResolvedURL:     cleanURL,
					Timestamp:       time.Now().UTC(),
					Hash:            rawHash,
					BackendName:     b.Name,
					BackendVersion:  version,
					Version:         version,
					BackendStatus:   string(report.Status),
					IsFallback:      isFallback,
					Payload:         payload,
					RawPayload:      payload,
					BackendArgs:     evidence.SanitizeArgs(args),
					MissingBackends: missingBackends,
					BackendAttempts: attempts,
				}
				if err := store.Save(rec); err != nil {
					return "", fmt.Errorf("failed to save evidence record for %s backend: %w", report.Status, err)
				}
				return rawHash, fmt.Errorf("all backends failed for source %q: backend %q is %s: %s", source, b.Name, report.Status, report.Error)
			}
			continue
		}

		detectedVer := report.DetectedVersion

		// 3. Execute backend command with argument array
		cmdArgs := append([]string(nil), b.Args...)
		cmdArgs = append(cmdArgs, args...)

		cmd := exec.CommandContext(ctx, b.Command, cmdArgs...)
		out, execErr := cmd.Output()
		if execErr != nil {
			attempts = append(attempts, evidence.BackendAttempt{
				BackendName: b.Name,
				Status:      "execution-failed",
				Error:       execErr.Error(),
			})
			if i == len(backends)-1 {
				payload := []byte(fmt.Sprintf("all backends failed for source %q; backend %q execution failed: %v", source, b.Name, execErr))
				rawHash := fmt.Sprintf("%x", sha256.Sum256(payload))
				rec := &evidence.Record{
					ResolvedURL:     cleanURL,
					Timestamp:       time.Now().UTC(),
					Hash:            rawHash,
					BackendName:     b.Name,
					BackendVersion:  detectedVer,
					Version:         detectedVer,
					BackendStatus:   "execution-failed",
					IsFallback:      isFallback,
					Payload:         payload,
					RawPayload:      payload,
					BackendArgs:     evidence.SanitizeArgs(cmdArgs),
					MissingBackends: missingBackends,
					BackendAttempts: attempts,
				}
				_ = store.Save(rec)
				return rawHash, fmt.Errorf("all backends failed for source %q: %w", source, execErr)
			}
			continue
		}

		// Content hash of the bytes AS RECEIVED, before any normalisation
		rawBytes := out
		rawHash := fmt.Sprintf("%x", sha256.Sum256(rawBytes))

		rec := &evidence.Record{
			ResolvedURL:     cleanURL,
			Timestamp:       time.Now().UTC(),
			Hash:            rawHash,
			BackendName:     b.Name,
			BackendVersion:  detectedVer,
			Version:         detectedVer,
			BackendStatus:   string(backend.StatusReachable),
			IsFallback:      isFallback,
			Payload:         rawBytes,
			RawPayload:      rawBytes,
			BackendArgs:     evidence.SanitizeArgs(cmdArgs),
			MissingBackends: missingBackends,
			BackendAttempts: attempts,
		}

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

	return "", fmt.Errorf("no backends succeeded for source %q", source)
}
