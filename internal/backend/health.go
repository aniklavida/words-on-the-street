package backend

import (
	"context"
	"fmt"
	"os/exec"
)

// HealthStatus represents the operational status of an external backend.
type HealthStatus string

const (
	// StatusReachable indicates the backend is installed, runnable, and satisfies the declared version range.
	StatusReachable HealthStatus = "reachable"
	// StatusUnreachable indicates the backend cannot be found in PATH, fails to start, or exits with an execution error.
	StatusUnreachable HealthStatus = "unreachable"
	// StatusWrongVersion indicates the backend runs, but its detected version falls outside the declared version range.
	StatusWrongVersion HealthStatus = "wrong-version"
)

// HealthReport holds the structured health assessment for a backend.
// It is returned as a pure value, never emitted as a log line.
type HealthReport struct {
	BackendName     string       `json:"backend_name"`
	Status          HealthStatus `json:"status"`
	DetectedVersion string       `json:"detected_version,omitempty"`
	DeclaredRange   string       `json:"declared_range,omitempty"`
	Licence         string       `json:"licence"`
	Error           string       `json:"error,omitempty"`
}

// CheckHealth performs a non-logging health check of the backend against its declared version range,
// returning the result as a structured value.
func (b *Backend) CheckHealth(ctx context.Context) HealthReport {
	report := HealthReport{
		BackendName:   b.Name,
		DeclaredRange: b.VersionRange,
		Licence:       b.Licence,
	}

	// 1. Check if the external executable exists in PATH
	if _, err := exec.LookPath(b.Command); err != nil {
		report.Status = StatusUnreachable
		report.Error = fmt.Sprintf("executable %q not found in PATH: %v", b.Command, err)
		return report
	}

	// 2. Detect the version by executing the command with its version arguments
	detectedVer, err := b.DetectVersion(ctx)
	if err != nil {
		report.Status = StatusUnreachable
		report.Error = fmt.Sprintf("version detection failed: %v", err)
		return report
	}
	report.DetectedVersion = detectedVer

	// 3. Validate against the declared version range
	if err := ValidateVersion(b.Name, detectedVer, b.VersionRange); err != nil {
		report.Status = StatusWrongVersion
		report.Error = err.Error()
		return report
	}

	report.Status = StatusReachable
	return report
}

// CheckSourceHealth runs health checks for all backends registered for a source, in failover order.
func CheckSourceHealth(ctx context.Context, reg *Registry, source string) []HealthReport {
	backends, ok := reg.BackendsForSource(source)
	if !ok {
		return nil
	}
	reports := make([]HealthReport, len(backends))
	for i, b := range backends {
		reports[i] = b.CheckHealth(ctx)
	}
	return reports
}

// CheckAllHealth runs health checks for all backends across all sources in the registry.
func CheckAllHealth(ctx context.Context, reg *Registry) map[string][]HealthReport {
	sources := reg.Sources()
	results := make(map[string][]HealthReport, len(sources))
	for _, src := range sources {
		results[src] = CheckSourceHealth(ctx, reg, src)
	}
	return results
}
