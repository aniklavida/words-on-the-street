package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBackendHelperProcess is re-executed by tests as a portable mock backend.
// It avoids any platform-specific shell scripts and runs identically on Linux, macOS, and Windows.
func TestBackendHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_BACKEND_HELPER") != "1" {
		return
	}

	toolName := os.Getenv("HELPER_TOOL_NAME")
	if toolName == "" {
		toolName = "fixture-tool"
	}

	for _, arg := range os.Args {
		if arg == "--version" {
			ver := os.Getenv("HELPER_VERSION")
			if ver == "" {
				ver = "1.5.0"
			}
			fmt.Printf("%s version %s\n", toolName, ver)
			os.Exit(0)
		}
	}

	// Normal fetch execution
	fmt.Printf("%s output payload\n", toolName)
	os.Exit(0)
}

// Done when: 1. The registry is data and a new backend can be added without touching control flow
// — prove it with a test that registers a fixture backend.
func TestRegistry_AddBackendWithoutControlFlowChange(t *testing.T) {
	reg := NewRegistry()

	fixtureBackend := Backend{
		Name:         "fixture-custom-tool",
		Command:      os.Args[0],
		Args:         []string{"-test.run=^TestBackendHelperProcess$", "--"},
		VersionArgs:  []string{"-test.run=^TestBackendHelperProcess$", "--", "--version"},
		VersionRange: ">= 1.0.0",
		Licence:      "MIT",
	}

	sourceName := "fixture-custom-source"
	if err := reg.Register(sourceName, fixtureBackend); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	backends, ok := reg.BackendsForSource(sourceName)
	if !ok || len(backends) != 1 {
		t.Fatalf("expected 1 backend for source %q, got: %v", sourceName, backends)
	}

	if backends[0].Name != "fixture-custom-tool" {
		t.Errorf("expected backend name %q, got %q", "fixture-custom-tool", backends[0].Name)
	}
	if backends[0].Licence != "MIT" {
		t.Errorf("expected backend licence %q, got %q", "MIT", backends[0].Licence)
	}
}

// Done when: 2. A backend whose version is outside the declared range produces an error naming the tool
// AND the detected version, asserted by a named test.
func TestVersion_OutsideDeclaredRangeReportsToolAndVersion(t *testing.T) {
	t.Setenv("GO_WANT_BACKEND_HELPER", "1")
	t.Setenv("HELPER_TOOL_NAME", "version-guarded-tool")
	t.Setenv("HELPER_VERSION", "0.9.4")

	b := Backend{
		Name:         "version-guarded-tool",
		Command:      os.Args[0],
		VersionArgs:  []string{"-test.run=^TestBackendHelperProcess$", "--", "--version"},
		VersionRange: ">= 2.0.0, < 3.0.0",
		Licence:      "Apache-2.0",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	detectedVer, err := b.DetectVersion(ctx)
	if err != nil {
		t.Fatalf("DetectVersion failed: %v", err)
	}

	if detectedVer != "0.9.4" {
		t.Fatalf("expected detected version %q, got %q", "0.9.4", detectedVer)
	}

	valErr := ValidateVersion(b.Name, detectedVer, b.VersionRange)
	if valErr == nil {
		t.Fatal("expected ValidateVersion to fail for version outside declared range, got nil")
	}

	errMsg := valErr.Error()
	if !strings.Contains(errMsg, "version-guarded-tool") {
		t.Errorf("expected error message to name the tool %q, got: %s", "version-guarded-tool", errMsg)
	}
	if !strings.Contains(errMsg, "0.9.4") {
		t.Errorf("expected error message to name the detected version %q, got: %s", "0.9.4", errMsg)
	}
}

// Constraint: An unexpected backend version is REPORTED, never silently accepted.
// A tool whose output format changed produces plausible nonsense otherwise, and plausible nonsense
// is the worst failure this project can have.
func TestBackend_UnexpectedVersionNeverSilentlyAccepted(t *testing.T) {
	t.Setenv("GO_WANT_BACKEND_HELPER", "1")
	t.Setenv("HELPER_TOOL_NAME", "strict-tool")
	t.Setenv("HELPER_VERSION", "3.5.0") // higher than allowed range

	b := Backend{
		Name:         "strict-tool",
		Command:      os.Args[0],
		VersionArgs:  []string{"-test.run=^TestBackendHelperProcess$", "--", "--version"},
		VersionRange: ">= 1.0.0, < 2.0.0",
		Licence:      "BSD-3-Clause",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	report := b.CheckHealth(ctx)
	if report.Status != StatusWrongVersion {
		t.Fatalf("expected StatusWrongVersion, got %v (error: %s)", report.Status, report.Error)
	}
	if !strings.Contains(report.Error, "strict-tool") {
		t.Errorf("expected report error to name tool %q: %s", "strict-tool", report.Error)
	}
	if !strings.Contains(report.Error, "3.5.0") {
		t.Errorf("expected report error to name detected version %q: %s", "3.5.0", report.Error)
	}

	// Verify that ValidateVersion explicitly refuses the unexpected version
	err := ValidateVersion(b.Name, report.DetectedVersion, b.VersionRange)
	if err == nil {
		t.Fatal("expected unexpected version to be refused with error, but was silently accepted")
	}
}

// Done when: 4. Every registry entry carries a licence, and a test fails if one does not.
func TestRegistry_EveryEntryCarriesLicence(t *testing.T) {
	// 1. Shipped defaults must all carry a licence
	defReg := DefaultRegistry()
	if err := defReg.Validate(); err != nil {
		t.Fatalf("DefaultRegistry validation failed: %v", err)
	}

	for _, src := range defReg.Sources() {
		backends, _ := defReg.BackendsForSource(src)
		for _, b := range backends {
			if strings.TrimSpace(b.Licence) == "" {
				t.Fatalf("backend %q for source %q has empty licence", b.Name, src)
			}
		}
	}

	// 2. Registering an entry without a licence must be refused
	reg := NewRegistry()
	unlicensedBackend := Backend{
		Name:         "unlicensed-tool",
		Command:      "echo",
		VersionRange: ">= 1.0.0",
		Licence:      "", // Missing licence!
	}

	err := reg.Register("some-source", unlicensedBackend)
	if err == nil {
		t.Fatal("expected error when registering backend with missing licence, got nil")
	}
	if !errors.Is(err, ErrMissingLicence) {
		t.Errorf("expected ErrMissingLicence, got: %v", err)
	}

	// 3. RegisterSource with an unlicensed backend must also be refused
	err = reg.RegisterSource("some-source-2", unlicensedBackend)
	if err == nil {
		t.Fatal("expected error when RegisterSource contains backend with missing licence, got nil")
	}
}

// Constraint: "Invoked as a separate process" is a claim that has to stay true:
// it is what keeps a copyleft backend from constraining this project's licence.
// No backend is ever linked or embedded, and each backend's licence is recorded in the registry.
func TestBackend_InvokedAsSeparateProcess(t *testing.T) {
	reg := DefaultRegistry()

	for _, src := range reg.Sources() {
		backends, _ := reg.BackendsForSource(src)
		for _, b := range backends {
			// External command name must be declared
			if strings.TrimSpace(b.Command) == "" {
				t.Errorf("backend %q for source %q has empty Command; backends must be external commands", b.Name, src)
			}
			// Must carry a recorded licence (e.g. copyleft GPL or permissive MIT/curl)
			if strings.TrimSpace(b.Licence) == "" {
				t.Errorf("backend %q for source %q missing licence in registry data", b.Name, src)
			}
		}
	}

	// Test separate process invocation via argument array
	t.Setenv("GO_WANT_BACKEND_HELPER", "1")
	t.Setenv("HELPER_TOOL_NAME", "separate-process-tool")
	t.Setenv("HELPER_VERSION", "2.1.0")

	b := Backend{
		Name:         "separate-process-tool",
		Command:      os.Args[0],
		VersionArgs:  []string{"-test.run=^TestBackendHelperProcess$", "--", "--version"},
		VersionRange: ">= 2.0.0",
		Licence:      "GPL-3.0", // Copyleft licence permitted because it is invoked as a separate process
	}

	if err := b.Validate(); err != nil {
		t.Fatalf("valid separate process backend failed validation: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	detected, err := b.DetectVersion(ctx)
	if err != nil {
		t.Fatalf("DetectVersion failed: %v", err)
	}
	if detected != "2.1.0" {
		t.Errorf("expected detected version %q, got %q", "2.1.0", detected)
	}

	// In-process / embedded backends without an external command must be refused
	embeddedBackend := Backend{
		Name:         "embedded-tool",
		Command:      "", // No external command; violates separate-process invariant!
		VersionRange: ">= 1.0.0",
		Licence:      "MIT",
	}
	if err := embeddedBackend.Validate(); !errors.Is(err, ErrMissingCommand) {
		t.Fatalf("expected ErrMissingCommand for embedded backend without command, got: %v", err)
	}
}

// A health check per backend that reports reachable / unreachable / wrong-version, as a value, not a log line.
func TestHealthCheck_ReportsStatusAsValue(t *testing.T) {
	t.Setenv("GO_WANT_BACKEND_HELPER", "1")

	// 1. Reachable
	t.Setenv("HELPER_TOOL_NAME", "tool-reachable")
	t.Setenv("HELPER_VERSION", "1.2.0")
	bReachable := Backend{
		Name:         "tool-reachable",
		Command:      os.Args[0],
		VersionArgs:  []string{"-test.run=^TestBackendHelperProcess$", "--", "--version"},
		VersionRange: ">= 1.0.0, < 2.0.0",
		Licence:      "MIT",
	}
	rep1 := bReachable.CheckHealth(context.Background())
	if rep1.Status != StatusReachable {
		t.Errorf("expected StatusReachable, got %v", rep1.Status)
	}
	if rep1.DetectedVersion != "1.2.0" {
		t.Errorf("expected DetectedVersion %q, got %q", "1.2.0", rep1.DetectedVersion)
	}

	// 2. Unreachable (command executable does not exist in PATH)
	missingPath := filepath.Join(t.TempDir(), "nonexistent-tool-binary")
	bUnreachable := Backend{
		Name:         "tool-missing",
		Command:      missingPath,
		VersionRange: ">= 1.0.0",
		Licence:      "MIT",
	}
	rep2 := bUnreachable.CheckHealth(context.Background())
	if rep2.Status != StatusUnreachable {
		t.Errorf("expected StatusUnreachable, got %v", rep2.Status)
	}
	if rep2.Error == "" {
		t.Error("expected non-empty Error for unreachable backend")
	}

	// 3. Wrong-version
	t.Setenv("HELPER_TOOL_NAME", "tool-wrong-version")
	t.Setenv("HELPER_VERSION", "0.8.0")
	bWrongVersion := Backend{
		Name:         "tool-wrong-version",
		Command:      os.Args[0],
		VersionArgs:  []string{"-test.run=^TestBackendHelperProcess$", "--", "--version"},
		VersionRange: ">= 1.0.0",
		Licence:      "MIT",
	}
	rep3 := bWrongVersion.CheckHealth(context.Background())
	if rep3.Status != StatusWrongVersion {
		t.Errorf("expected StatusWrongVersion, got %v", rep3.Status)
	}
	if !strings.Contains(rep3.Error, "tool-wrong-version") || !strings.Contains(rep3.Error, "0.8.0") {
		t.Errorf("expected error to name tool and detected version: %s", rep3.Error)
	}
}

// Done when: 3. The live status surface reports a source as degraded when its
// primary backend is down but a fallback exists, distinct from a source where
// every backend is down. The check runs health checks rather than reading any
// past fetch, so this exercises the same code path the CLI status command uses.
func TestCheckSourceStatus_DistinguishesDegradedFromDown(t *testing.T) {
	t.Setenv("GO_WANT_BACKEND_HELPER", "1")
	t.Setenv("HELPER_TOOL_NAME", "status-fixture-tool")
	t.Setenv("HELPER_VERSION", "1.5.0")

	missingDir := t.TempDir()
	missing := func(name string) Backend {
		return Backend{
			Name:         name,
			Command:      filepath.Join(missingDir, name+"-not-installed"),
			VersionRange: ">= 1.0.0",
			Licence:      "MIT",
		}
	}
	available := func(name string) Backend {
		return Backend{
			Name:         name,
			Command:      os.Args[0],
			VersionArgs:  []string{"-test.run=^TestBackendHelperProcess$", "--", "--version"},
			VersionRange: ">= 1.0.0",
			Licence:      "MIT",
		}
	}

	reg := NewRegistry()
	cases := []struct {
		source   string
		backends []Backend
	}{
		{"degraded-source", []Backend{missing("primary-down"), available("fallback-up")}},
		{"down-source", []Backend{missing("first-down"), missing("second-down")}},
		{"healthy-source", []Backend{available("primary-up")}},
	}
	for _, tc := range cases {
		if err := reg.RegisterSource(tc.source, tc.backends...); err != nil {
			t.Fatalf("RegisterSource(%q) failed: %v", tc.source, err)
		}
	}

	bySource := map[string]SourceStatus{}
	for _, status := range CheckAllStatus(context.Background(), reg) {
		bySource[status.Source] = status
	}

	if got := bySource["healthy-source"].State; got != SourceHealthy {
		t.Errorf("healthy-source state = %q, want %q", got, SourceHealthy)
	}

	degraded := bySource["degraded-source"]
	if degraded.State != SourceDegraded {
		t.Fatalf("degraded-source state = %q, want %q", degraded.State, SourceDegraded)
	}
	if degraded.Primary != "primary-down" {
		t.Errorf("degraded-source primary = %q, want %q", degraded.Primary, "primary-down")
	}
	if degraded.Serving != "fallback-up" {
		t.Errorf("degraded-source serving = %q, want %q", degraded.Serving, "fallback-up")
	}

	if got := bySource["down-source"].State; got != SourceDown {
		t.Errorf("down-source state = %q, want %q", got, SourceDown)
	}
	if bySource["down-source"].Serving != "" {
		t.Errorf("down-source serving = %q, want empty", bySource["down-source"].Serving)
	}
}

func TestVersionRange_ParsingAndSatisfaction(t *testing.T) {
	cases := []struct {
		rangeStr string
		version  string
		want     bool
	}{
		{">= 7.68.0", "7.68.0", true},
		{">= 7.68.0", "8.4.0", true},
		{">= 7.68.0", "7.67.9", false},
		{">= 2.0.0, < 3.0.0", "2.40.1", true},
		{">= 2.0.0, < 3.0.0", "1.9.9", false},
		{">= 2.0.0, < 3.0.0", "3.0.0", false},
		{"1.x", "1.4.2", true},
		{"1.x", "2.0.0", false},
		{"*", "9.9.9", true},
		{"", "1.0.0", true},
	}

	for _, tc := range cases {
		vr, err := ParseVersionRange(tc.rangeStr)
		if err != nil {
			t.Fatalf("ParseVersionRange(%q) failed: %v", tc.rangeStr, err)
		}
		v, err := ParseSemVersion(tc.version)
		if err != nil {
			t.Fatalf("ParseSemVersion(%q) failed: %v", tc.version, err)
		}
		got := vr.Satisfies(v)
		if got != tc.want {
			t.Errorf("VersionRange(%q).Satisfies(%q) = %v; want %v", tc.rangeStr, tc.version, got, tc.want)
		}
	}
}
