package backend

import (
	"context"
	"sort"
)

// SourceState classifies a source's live failover posture. It is computed from
// health checks run right now, never from a record of a past fetch, so it says
// what a fetch would do next rather than what one did before.
type SourceState string

const (
	// SourceHealthy means the first-listed (primary) backend is reachable.
	SourceHealthy SourceState = "healthy"
	// SourceDegraded means the primary backend is not reachable but a fallback
	// backend is, so a fetch still succeeds on a non-primary path.
	SourceDegraded SourceState = "degraded"
	// SourceDown means no backend for the source is reachable.
	SourceDown SourceState = "down"
)

// SourceStatus is the live failover posture for one source: the backend that
// would serve a fetch now, and the health of every backend in failover order.
type SourceStatus struct {
	Source  string         `json:"source"`
	State   SourceState    `json:"state"`
	Primary string         `json:"primary,omitempty"`
	Serving string         `json:"serving,omitempty"`
	Reports []HealthReport `json:"reports"`
}

// ClassifySourceStatus determines a source's state from its backends' health
// reports, which must be in failover order. The first reachable backend wins:
// if it is the primary the source is healthy, otherwise it is degraded. If none
// is reachable the source is down. This is pure and executes nothing.
func ClassifySourceStatus(source string, reports []HealthReport) SourceStatus {
	status := SourceStatus{Source: source, Reports: reports}
	if len(reports) == 0 {
		status.State = SourceDown
		return status
	}

	status.Primary = reports[0].BackendName
	for i, report := range reports {
		if report.Status != StatusReachable {
			continue
		}
		status.Serving = report.BackendName
		if i == 0 {
			status.State = SourceHealthy
		} else {
			status.State = SourceDegraded
		}
		return status
	}

	status.State = SourceDown
	return status
}

// CheckSourceStatus runs live health checks for every backend of a source and
// classifies the result as healthy, degraded, or down.
func CheckSourceStatus(ctx context.Context, reg *Registry, source string) SourceStatus {
	return ClassifySourceStatus(source, CheckSourceHealth(ctx, reg, source))
}

// CheckAllStatus runs live health checks across every registered source and
// returns the classifications in deterministic (sorted) source-name order so a
// caller can compare runs.
func CheckAllStatus(ctx context.Context, reg *Registry) []SourceStatus {
	sources := reg.Sources()
	sort.Strings(sources)

	statuses := make([]SourceStatus, 0, len(sources))
	for _, source := range sources {
		statuses = append(statuses, CheckSourceStatus(ctx, reg, source))
	}
	return statuses
}
