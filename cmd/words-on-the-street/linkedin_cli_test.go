package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Done when: 3. The configuration flow shows the risk before accepting a
// credential. `config linkedin` is the CLI point of configuration: it prints the
// ban-risk disclosure in plain text, and even when a cookie is already set it
// never echoes the value.
func TestCLI_ConfigureLinkedInShowsBanRiskDisclosureWithoutEchoingCookie(t *testing.T) {
	binPath := buildCLIBinary(t)
	const fakeCookie = "li_at=FAKE-LINKEDIN-COOKIE-DO-NOT-USE-0000"

	env := append(os.Environ(), "WORDS_ON_THE_STREET_LINKEDIN_COOKIE="+fakeCookie)
	stdout, stderr, err := runCLI(t, binPath, env, "config", "linkedin")
	if err != nil {
		t.Fatalf("config linkedin failed: %v\nstderr: %s", err, stderr)
	}

	out := string(stdout) + string(stderr)
	for _, want := range []string{
		"against its terms of service",
		"without the password",
		"permanently and without warning",
		"litigated against scrapers",
		"WORDS_ON_THE_STREET_LINKEDIN_COOKIE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config output must state %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, fakeCookie) {
		t.Errorf("config output echoed the cookie value")
	}

	// With no cookie configured, the command still shows the disclosure and says
	// how to set one.
	stdout, stderr, err = runCLI(t, binPath, os.Environ(), "config", "linkedin")
	if err != nil {
		t.Fatalf("config linkedin without a cookie failed: %v\nstderr: %s", err, stderr)
	}
	out = string(stdout) + string(stderr)
	if !strings.Contains(out, "permanently and without warning") {
		t.Errorf("disclosure must be shown before a cookie is configured; got:\n%s", out)
	}
	if !strings.Contains(out, "No cookie is configured") {
		t.Errorf("expected guidance that no cookie is configured; got:\n%s", out)
	}
}

// Done when: 2 (CLI surface). A real linkedin fetch never puts the cookie on
// stdout (the agent-facing bytes) or stderr (the log), and never writes it to
// the store.
func TestCLI_LinkedInFetchLeavesNoCookieInOutputOrStore(t *testing.T) {
	binPath := buildCLIBinary(t)
	tmpDir := t.TempDir()
	const fakeCookie = "li_at=FAKE-LINKEDIN-COOKIE-DO-NOT-USE-0000"

	payload := []byte(`{"source":"linkedin","posts":[{"text":"public post"}]}`)
	fixturePath := writeCLIFixture(t, tmpDir, payload)
	regPath := writeCLIRegistry(t, tmpDir, map[string][]map[string]any{
		"linkedin": {cliHelperBackend(t, "curl")},
	})
	storeDir := filepath.Join(tmpDir, "store")

	env := append(cliEnv(regPath, storeDir, fixturePath),
		"WORDS_ON_THE_STREET_LINKEDIN_COOKIE="+fakeCookie,
		"WORDS_ON_THE_STREET_LINKEDIN_MIN_INTERVAL=0",
	)

	stdout, stderr, err := runCLI(t, binPath, env, "fetch", "linkedin", "acme")
	if err != nil {
		t.Fatalf("linkedin fetch failed: %v\nstderr: %s", err, stderr)
	}
	if !bytes.Equal(stdout, payload) {
		t.Fatalf("stdout must be exactly the fetched bytes:\ngot:  %q\nwant: %q", stdout, payload)
	}
	for _, out := range []string{string(stdout), string(stderr)} {
		if strings.Contains(out, fakeCookie) {
			t.Errorf("CLI output contains the cookie")
		}
	}

	err = filepath.Walk(storeDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(data, []byte(fakeCookie)) {
			t.Errorf("CREDENTIAL LEAK: store file %s contains the cookie", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("error scanning store directory: %v", err)
	}
}
