package security

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aniklavida/words-on-the-street/internal/backend"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/fetch"
)

// Done when: 3. A hostile source configuration cannot execute a shell command.
//
// The registry document is data. A value that carries shell metacharacters must
// travel to the backend as one inert argument, never as a fragment of a command
// string. Each payload below would create the canary file if a shell interpreted
// it, so the canary's absence is the proof, and the recorded argv proves the
// value really was delivered rather than dropped.
//
// Sabotage check: change FetchSource to build a command string and run it
// through "sh -c" (for example exec.CommandContext(ctx, "sh", "-c",
// strings.Join(cmdArgs, " "))) and this named test fails: the canary appears.
// Restore the argument-array invocation and it passes again.
func TestHostileSourceConfigurationCannotExecuteShell(t *testing.T) {
	recorder := filepath.Join(t.TempDir(), "invocations.jsonl")
	t.Setenv(securityHelperEnv, "1")
	t.Setenv(securityRecorderEnv, recorder)
	t.Setenv(securityPayloadEnv, `{"configuration":"was inert"}`)

	canary := filepath.Join(t.TempDir(), "shell-executed-canary")
	hostile := []string{
		"; touch " + canary + " ; #",
		"`touch " + canary + "`",
		"$(touch " + canary + ")",
		"&& touch " + canary + " && true",
		"| touch " + canary,
		"' ; touch " + canary + " ; '",
	}

	exe, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("failed to resolve the test binary path: %v", err)
	}

	// A registry document exactly as a caller would supply one, with hostile
	// values in the backend's own argument list.
	doc := map[string][]map[string]any{
		"hostile-source": {
			{
				"name":          "hostile-recorder",
				"command":       exe,
				"args":          append([]string{"-test.run=^TestSecurityPostureHelperProcess$", "--"}, hostile...),
				"version_args":  []string{"-test.run=^TestSecurityPostureHelperProcess$", "--", "--version"},
				"version_range": ">= 1.0.0",
				"licence":       "MIT",
			},
		},
	}
	docBytes, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("failed to marshal hostile registry: %v", err)
	}
	reg, err := backend.LoadRegistry(bytes.NewReader(docBytes))
	if err != nil {
		t.Fatalf("hostile registry document was rejected before the fetch: %v", err)
	}

	store := evidence.NewMemoryStore()
	hash, err := fetch.FetchSource(context.Background(), store, reg, "hostile-source", "https://example.com/hostile", nil)
	if err != nil {
		t.Fatalf("fetch through a hostile configuration failed: %v", err)
	}
	rec, err := store.Get(hash)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}
	if !bytes.Contains(rec.Payload, []byte("was inert")) {
		t.Fatalf("the fetch did not run the fixture backend; payload = %q", rec.Payload)
	}

	// The canary is the proof: if a shell had interpreted any value, it exists.
	if _, statErr := os.Stat(canary); !os.IsNotExist(statErr) {
		t.Fatalf("hostile source configuration executed a shell command; canary exists at %s", canary)
	}

	// And the values must have reached the backend as single, whole arguments.
	// A dropped or split value would make the canary check vacuous.
	delivered := map[string]bool{}
	for _, inv := range readInvocations(t, recorder) {
		for _, arg := range inv.Argv {
			for _, payload := range hostile {
				if arg == payload {
					delivered[payload] = true
				}
			}
		}
	}
	for _, payload := range hostile {
		if !delivered[payload] {
			t.Errorf("hostile value %q never reached the backend as one inert argument", payload)
		}
	}
}

// A hostile executable name is treated as a literal name to look up, never as a
// command line for a shell. The fetch fails honestly and the canary is absent.
func TestHostileBackendCommandValueIsNeverShellInterpreted(t *testing.T) {
	canary := filepath.Join(t.TempDir(), "shell-executed-canary")

	reg := backend.NewRegistry()
	hostile := backend.Backend{
		Name:         "hostile-command",
		Command:      "curl && touch " + canary,
		VersionRange: ">= 1.0.0",
		Licence:      "MIT",
	}
	if err := reg.RegisterSource("hostile-command-source", hostile); err != nil {
		t.Fatalf("RegisterSource failed: %v", err)
	}

	store := evidence.NewMemoryStore()
	if _, err := fetch.FetchSource(context.Background(), store, reg, "hostile-command-source", "https://example.com", nil); err == nil {
		t.Fatal("expected a command value that is not an executable name to fail, got nil")
	}
	if _, statErr := os.Stat(canary); !os.IsNotExist(statErr) {
		t.Fatalf("a hostile Command value was interpreted by a shell; canary exists at %s", canary)
	}
}
