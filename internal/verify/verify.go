// Package verify re-fetches a previously recorded entry and reports whether the
// source is byte-for-byte identical, has changed, or is gone.
//
// Verification never rewrites the evidence it checks. The re-fetch is recorded
// as a new entry in the append-only store, linked to the original through the
// ledger chain, so the original record and its bytes survive a check unchanged.
package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/evidence"
)

// ExitGone is the process exit code a backend uses to report that the source it
// was asked for no longer exists. It is distinct from an execution failure so a
// deleted post is never confused with a broken tool.
const ExitGone = 44

// ErrSourceGone reports that the source no longer serves the recorded entry.
var ErrSourceGone = errors.New("source is gone")

// State is the outcome of comparing a recorded entry with its current source.
type State string

const (
	// Identical means the re-fetched bytes hash to the same value as the record.
	Identical State = "identical"
	// Changed means the re-fetched bytes differ from the recorded bytes.
	Changed State = "changed"
	// Gone means the source no longer serves the recorded entry.
	Gone State = "gone"
)

// Result is the structured report of a verification.
type Result struct {
	State                 State     `json:"state"`
	OriginalHash          string    `json:"original_hash"`
	CurrentHash           string    `json:"current_hash,omitempty"`
	OriginalRecordHash    string    `json:"original_record_hash"`
	ObservationRecordHash string    `json:"observation_record_hash"`
	Diff                  string    `json:"diff,omitempty"`
	CheckedAt             time.Time `json:"checked_at"`
}

// Refetcher retrieves the current bytes for a previously recorded entry.
type Refetcher interface {
	Refetch(ctx context.Context, rec *evidence.Record) ([]byte, error)
}

// CommandRefetcher re-invokes the backend named in the record, with the recorded
// argument array, exactly as the original fetch was made. A backend that exits
// with ExitGone is reported as ErrSourceGone rather than as a tool failure.
type CommandRefetcher struct{}

// Refetch re-runs the recorded backend and returns the bytes it serves now.
func (CommandRefetcher) Refetch(ctx context.Context, rec *evidence.Record) ([]byte, error) {
	if rec == nil {
		return nil, fmt.Errorf("record is required")
	}
	cmd := exec.CommandContext(ctx, rec.BackendName, rec.BackendArgs...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == ExitGone {
			return nil, ErrSourceGone
		}
		return nil, fmt.Errorf("re-fetch with backend %q failed: %w", rec.BackendName, err)
	}
	return out, nil
}

// Verify re-fetches the entry named by originalHash and reports whether the
// source is identical, changed, or gone. The original record is only read; the
// observation is written as a new record so verifying can never overwrite what
// it verifies.
func Verify(ctx context.Context, store evidence.Store, refetcher Refetcher, originalHash string) (*Result, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if refetcher == nil {
		return nil, fmt.Errorf("refetcher is required")
	}
	if originalHash == "" {
		return nil, fmt.Errorf("original record hash is required")
	}

	original, err := store.Get(originalHash)
	if err != nil {
		return nil, fmt.Errorf("failed to load original record %s: %w", originalHash, err)
	}

	checkedAt := time.Now().UTC()
	current, fetchErr := refetcher.Refetch(ctx, original)
	if fetchErr != nil && !errors.Is(fetchErr, ErrSourceGone) {
		return nil, fetchErr
	}

	result := &Result{
		OriginalHash:       original.Hash,
		OriginalRecordHash: original.RecordHash,
		CheckedAt:          checkedAt,
	}

	if errors.Is(fetchErr, ErrSourceGone) {
		result.State = Gone
		observationHash, err := recordObservation(store, original, goneMarker(original, checkedAt), checkedAt, string(Gone))
		if err != nil {
			return nil, err
		}
		result.ObservationRecordHash = observationHash
		return result, nil
	}

	result.CurrentHash = hashBytes(current)
	status := "verified-identical"
	if bytes.Equal(original.RawBytes(), current) {
		result.State = Identical
	} else {
		result.State = Changed
		result.Diff = lineDiff(original.RawBytes(), current)
		status = "verified-changed"
	}

	observationHash, err := recordObservation(store, original, current, checkedAt, status)
	if err != nil {
		return nil, err
	}
	result.ObservationRecordHash = observationHash
	return result, nil
}

func recordObservation(store evidence.Store, original *evidence.Record, payload []byte, checkedAt time.Time, status string) (string, error) {
	rec := &evidence.Record{
		ResolvedURL:    original.ResolvedURL,
		Timestamp:      checkedAt,
		Hash:           hashBytes(payload),
		BackendName:    original.BackendName,
		BackendVersion: original.BackendVersion,
		Version:        original.Version,
		IsFallback:     original.IsFallback,
		Payload:        payload,
		RawPayload:     payload,
		BackendArgs:    original.BackendArgs,
		BackendStatus:  status,
	}
	if err := store.Save(rec); err != nil {
		return "", fmt.Errorf("failed to record verification observation: %w", err)
	}
	return rec.RecordHash, nil
}

func goneMarker(original *evidence.Record, checkedAt time.Time) []byte {
	return []byte(fmt.Sprintf("source is gone: %s checked at %s", original.ResolvedURL, checkedAt.Format(time.RFC3339)))
}

func hashBytes(b []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func lineDiff(oldBytes, newBytes []byte) string {
	oldLines := splitLines(oldBytes)
	newLines := splitLines(newBytes)

	n, m := len(oldLines), len(newLines)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case oldLines[i] == newLines[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var b strings.Builder
	b.WriteString("--- original\n+++ current\n")
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case oldLines[i] == newLines[j]:
			fmt.Fprintf(&b, " %s\n", oldLines[i])
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			fmt.Fprintf(&b, "-%s\n", oldLines[i])
			i++
		default:
			fmt.Fprintf(&b, "+%s\n", newLines[j])
			j++
		}
	}
	for ; i < n; i++ {
		fmt.Fprintf(&b, "-%s\n", oldLines[i])
	}
	for ; j < m; j++ {
		fmt.Fprintf(&b, "+%s\n", newLines[j])
	}
	return b.String()
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	return strings.Split(string(data), "\n")
}
