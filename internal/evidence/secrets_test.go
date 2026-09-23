package evidence

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The value below is an obviously-fake placeholder, never a real credential.
const testRegisteredSecret = "fake-registered-secret-value-not-real-0123456789"

func TestSecretSet_ScrubsRegisteredSecretsAndContainsThem(t *testing.T) {
	s := NewSecretSet("alpha-secret-value", "beta-secret-value")

	scrubbed := s.Scrub("prefix alpha-secret-value middle beta-secret-value suffix")
	for _, secret := range []string{"alpha-secret-value", "beta-secret-value"} {
		if strings.Contains(scrubbed, secret) {
			t.Errorf("Scrub left %q in %q", secret, scrubbed)
		}
	}
	if !strings.Contains(scrubbed, RedactionPlaceholder) {
		t.Errorf("Scrub did not insert the redaction placeholder: %q", scrubbed)
	}
	if !s.Contains("text with alpha-secret-value inside") {
		t.Error("Contains did not find a registered secret")
	}
	if s.Contains("this text holds nothing sensitive") {
		t.Error("Contains reported a secret in benign text")
	}
	if got := s.Len(); got != 2 {
		t.Errorf("Len = %d, want 2", got)
	}
}

// A session cookie is not always spelled with a flag the structural rules know.
// The value itself must be scrubbed wherever it appears.
func TestSanitizeArgs_ScrubsRegisteredCookieValue(t *testing.T) {
	ResetSecrets()
	t.Cleanup(ResetSecrets)
	RegisterSecret(testRegisteredSecret)

	args := []string{
		"-sSL",
		"-H", "X-Trace: " + testRegisteredSecret,
		testRegisteredSecret,
		"https://x.com/search?q=golang",
	}

	sanitized := SanitizeArgs(args)
	for _, arg := range sanitized {
		if strings.Contains(arg, testRegisteredSecret) {
			t.Errorf("SanitizeArgs leaked a registered secret: %q", arg)
		}
	}
	if sanitized[len(sanitized)-1] != "https://x.com/search?q=golang" {
		t.Errorf("SanitizeArgs damaged an unrelated argument: %q", sanitized[len(sanitized)-1])
	}
}

func TestRedact_LeavesTextWithoutSecretsUnchanged(t *testing.T) {
	ResetSecrets()
	t.Cleanup(ResetSecrets)
	RegisterSecret(testRegisteredSecret)

	const plain = "an ordinary log line with no credential"
	if got := Redact(plain); got != plain {
		t.Errorf("Redact changed benign text: %q", got)
	}
}

// Validation refuses a registered secret in every textual field of a record.
// This is the boundary that stops a cookie reaching the store regardless of how
// the argument was spelled.
func TestRecord_RejectsRegisteredSecretInEveryField(t *testing.T) {
	ResetSecrets()
	t.Cleanup(ResetSecrets)
	RegisterSecret(testRegisteredSecret)

	raw := []byte("clean payload")
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	base := Record{
		ResolvedURL:    "https://x.com/search?q=golang",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "curl",
		BackendVersion: "8.4.0",
		Payload:        raw,
	}

	cases := []struct {
		name   string
		mutate func(*Record)
	}{
		{"resolved URL", func(r *Record) {
			r.ResolvedURL = "https://x.com/search?q=" + testRegisteredSecret
		}},
		{"backend argument", func(r *Record) {
			r.BackendArgs = []string{"-H", "X-Trace: " + testRegisteredSecret}
		}},
		{"backend name", func(r *Record) { r.BackendName = testRegisteredSecret }},
		{"missing backend", func(r *Record) { r.MissingBackends = []string{testRegisteredSecret} }},
		{"backend attempt error", func(r *Record) {
			r.BackendAttempts = []BackendAttempt{{BackendName: "curl", Status: "failed", Error: testRegisteredSecret}}
		}},
		{"payload", func(r *Record) {
			r.Payload = []byte("body " + testRegisteredSecret)
			r.Hash = fmt.Sprintf("%x", sha256.Sum256(r.Payload))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := base
			tc.mutate(&rec)
			err := rec.Validate()
			if err == nil {
				t.Fatalf("Validate accepted a record with the registered secret in %s", tc.name)
			}
			if !errors.Is(err, ErrCredentialLeak) {
				t.Errorf("expected ErrCredentialLeak for %s, got: %v", tc.name, err)
			}
		})
	}
}
