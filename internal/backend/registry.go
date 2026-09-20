package backend

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

var (
	ErrMissingLicence = errors.New("backend entry missing licence")
	ErrMissingName    = errors.New("backend entry missing name")
	ErrMissingCommand = errors.New("backend entry missing command")
	ErrMissingRange   = errors.New("backend entry missing version range")
)

// Backend defines an external tool invoked as a separate process.
// Backends are never linked or embedded, preserving the boundary that keeps
// copyleft tools from constraining the repository licence.
type Backend struct {
	Name                 string                              `json:"name"`
	Command              string                              `json:"command"`
	Args                 []string                            `json:"args,omitempty"`
	VersionArgs          []string                            `json:"version_args,omitempty"`
	VersionRange         string                              `json:"version_range"`
	Licence              string                              `json:"licence"`
	CustomVersionExtract func(output string) (string, error) `json:"-"`
}

// Validate checks that the backend specification is complete.
// Every backend must specify a name, an external command, a version range, and a licence.
func (b *Backend) Validate() error {
	if strings.TrimSpace(b.Name) == "" {
		return ErrMissingName
	}
	if strings.TrimSpace(b.Command) == "" {
		return ErrMissingCommand
	}
	if strings.TrimSpace(b.Licence) == "" {
		return fmt.Errorf("%w: backend %q", ErrMissingLicence, b.Name)
	}
	if strings.TrimSpace(b.VersionRange) == "" {
		return ErrMissingRange
	}
	if _, err := ParseVersionRange(b.VersionRange); err != nil {
		return fmt.Errorf("invalid version range %q for backend %q: %w", b.VersionRange, b.Name, err)
	}
	return nil
}

// DetectVersion invokes the backend command as a separate process to detect its version.
func (b *Backend) DetectVersion(ctx context.Context) (string, error) {
	vArgs := b.VersionArgs
	if len(vArgs) == 0 {
		vArgs = []string{"--version"}
	}

	cmd := exec.CommandContext(ctx, b.Command, vArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run %s %v: %w (output: %s)", b.Command, vArgs, err, string(out))
	}

	if b.CustomVersionExtract != nil {
		return b.CustomVersionExtract(string(out))
	}
	return ExtractVersion(string(out))
}

// Registry stores the registered backends for each source as pure DATA.
// Adding a backend is reviewable in a diff without touching code branches or control flow.
type Registry struct {
	mu      sync.RWMutex
	sources map[string][]Backend
}

// NewRegistry constructs an empty backend registry.
func NewRegistry() *Registry {
	return &Registry{
		sources: make(map[string][]Backend),
	}
}

// Register appends a backend to the ordered failover list for a source.
func (r *Registry) Register(source string, b Backend) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := b.Validate(); err != nil {
		return err
	}

	r.sources[source] = append(r.sources[source], b)
	return nil
}

// RegisterSource sets the ordered failover list of backends for a source.
func (r *Registry) RegisterSource(source string, backends ...Backend) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, b := range backends {
		if err := b.Validate(); err != nil {
			return err
		}
	}
	r.sources[source] = append([]Backend(nil), backends...)
	return nil
}

// BackendsForSource returns the ordered failover list of backends registered for a source.
func (r *Registry) BackendsForSource(source string) ([]Backend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	backends, ok := r.sources[source]
	if !ok || len(backends) == 0 {
		return nil, false
	}
	cp := make([]Backend, len(backends))
	copy(cp, backends)
	return cp, true
}

// Sources returns the list of all registered source names.
func (r *Registry) Sources() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]string, 0, len(r.sources))
	for s := range r.sources {
		res = append(res, s)
	}
	return res
}

// Validate verifies that every source has at least one backend, and that every backend carries a licence.
func (r *Registry) Validate() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for src, backends := range r.sources {
		if len(backends) == 0 {
			return fmt.Errorf("source %q has no backends registered", src)
		}
		for i, b := range backends {
			if err := b.Validate(); err != nil {
				return fmt.Errorf("source %q backend [%d] (%q) invalid: %w", src, i, b.Name, err)
			}
		}
	}
	return nil
}

// DefaultSources defines the shipped default sources and ordered backends as pure DATA.
// Order within each source slice is the failover order.
//
// Which sources ship by default is not settled yet. The set below is provisional
// and exists so the registry has something real to exercise; changing it is a diff
// against this map, which is the point of holding it as data rather than as code.
var DefaultSources = map[string][]Backend{
	"hacker-news": {
		{
			Name:         "curl",
			Command:      "curl",
			Args:         []string{"-sSL"},
			VersionArgs:  []string{"--version"},
			VersionRange: ">= 7.68.0",
			Licence:      "curl",
		},
	},
	"lobsters": {
		{
			Name:         "curl",
			Command:      "curl",
			Args:         []string{"-sSL"},
			VersionArgs:  []string{"--version"},
			VersionRange: ">= 7.68.0",
			Licence:      "curl",
		},
	},
	"github": {
		{
			Name:         "gh",
			Command:      "gh",
			Args:         []string{"api"},
			VersionArgs:  []string{"--version"},
			VersionRange: ">= 2.0.0",
			Licence:      "MIT",
		},
		{
			Name:         "curl",
			Command:      "curl",
			Args:         []string{"-sSL"},
			VersionArgs:  []string{"--version"},
			VersionRange: ">= 7.68.0",
			Licence:      "curl",
		},
	},
	"web": {
		{
			Name:         "curl",
			Command:      "curl",
			Args:         []string{"-sSL"},
			VersionArgs:  []string{"--version"},
			VersionRange: ">= 7.68.0",
			Licence:      "curl",
		},
	},
}

// DefaultRegistry creates and populates a Registry with the shipped default sources and backends.
func DefaultRegistry() *Registry {
	reg := NewRegistry()
	for src, backends := range DefaultSources {
		_ = reg.RegisterSource(src, backends...)
	}
	return reg
}
