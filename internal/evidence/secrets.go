package evidence

import (
	"sort"
	"strings"
	"sync"
)

// RedactionPlaceholder is the text substituted wherever a registered secret
// value is found. It is exported so a surface can assert that redaction
// happened without embedding the secret it replaced.
const RedactionPlaceholder = "[REDACTED]"

// SecretSet holds credential values that must never reach a record, a log, a
// synthesis, or anything handed to an agent. Values are held in memory only;
// the set has no serialisation, so registering a secret cannot itself leak it.
//
// Registration is the only way a value becomes scrubbable. Structural checks
// (SanitizeURL, SanitizeArgs) catch credentials described as credentials; a
// SecretSet catches the same value when it appears somewhere those checks do
// not expect, which is the shape a session cookie leaks in.
//
// A SecretSet is safe for concurrent use.
type SecretSet struct {
	mu      sync.RWMutex
	secrets map[string]struct{}
}

// NewSecretSet returns a set containing the given non-empty values.
func NewSecretSet(values ...string) *SecretSet {
	s := &SecretSet{secrets: make(map[string]struct{})}
	for _, v := range values {
		s.Add(v)
	}
	return s
}

// Add registers a value. Empty values are ignored: an empty secret would match
// every string and redact the whole record.
func (s *SecretSet) Add(value string) {
	if s == nil || value == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.secrets == nil {
		s.secrets = make(map[string]struct{})
	}
	s.secrets[value] = struct{}{}
}

// Len reports how many distinct secrets are registered.
func (s *SecretSet) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.secrets)
}

// Scrub replaces every registered secret found in text with the redaction
// placeholder. Longer secrets are replaced first so one secret embedded in
// another is redacted whole rather than split.
func (s *SecretSet) Scrub(text string) string {
	if s == nil || text == "" {
		return text
	}
	out := text
	for _, v := range s.values() {
		out = strings.ReplaceAll(out, v, RedactionPlaceholder)
	}
	return out
}

// ScrubBytes applies Scrub to a byte slice.
func (s *SecretSet) ScrubBytes(b []byte) []byte {
	if s == nil || len(b) == 0 {
		return b
	}
	return []byte(s.Scrub(string(b)))
}

// Contains reports whether text contains any registered secret.
func (s *SecretSet) Contains(text string) bool {
	if s == nil || text == "" {
		return false
	}
	for _, v := range s.values() {
		if strings.Contains(text, v) {
			return true
		}
	}
	return false
}

func (s *SecretSet) values() []string {
	s.mu.RLock()
	values := make([]string, 0, len(s.secrets))
	for v := range s.secrets {
		values = append(values, v)
	}
	s.mu.RUnlock()
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}

// defaultSecrets is the process-wide registry every boundary consults. A caller
// that holds a credential registers it once, here, and every surface that calls
// Redact or ContainsSecret afterwards shares the same protection.
var defaultSecrets = NewSecretSet()

// RegisterSecret adds a credential value to the process-wide redaction set.
func RegisterSecret(value string) { defaultSecrets.Add(value) }

// RegisteredSecretCount reports how many secrets the process has registered.
func RegisteredSecretCount() int { return defaultSecrets.Len() }

// Redact scrubs every registered secret from text.
func Redact(text string) string { return defaultSecrets.Scrub(text) }

// RedactBytes scrubs every registered secret from a byte slice.
func RedactBytes(b []byte) []byte { return defaultSecrets.ScrubBytes(b) }

// ContainsSecret reports whether text contains a registered secret.
func ContainsSecret(text string) bool { return defaultSecrets.Contains(text) }

// ResetSecrets clears the process-wide set. It exists so a test can start from
// a known-empty set; production code never calls it.
func ResetSecrets() { defaultSecrets = NewSecretSet() }
