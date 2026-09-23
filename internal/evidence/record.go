package evidence

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	ErrTamperingDetected   = errors.New("store tampering detected")
	ErrAppendOnlyViolation = errors.New("append-only violation: record already exists")
	ErrCredentialLeak      = errors.New("credential detected in record")
	ErrMissingField        = errors.New("required record field missing")
	ErrPayloadMismatch     = errors.New("payload hash does not match record hash")
	ErrNotFound            = errors.New("record not found")
)

// NormalisationRecord records details of a normalisation transformation
// applied to the raw bytes. Normalisation is recorded separately so it
// never destroys what arrived.
type NormalisationRecord struct {
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	Payload   []byte    `json:"payload,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// NormaliseFunc defines a function that transforms raw bytes into normalised bytes.
type NormaliseFunc func([]byte) ([]byte, error)

// BackendAttempt records the outcome of an individual backend attempt during orchestration or failover.
type BackendAttempt struct {
	BackendName string `json:"backend_name"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

// Record is the immutable provenance record for a fetch operation.
// Every fetch records the resolved URL, UTC timestamp, content hash of the bytes
// as received before any normalisation, backend name and version, and whether
// the path taken was primary or a fallback.
type Record struct {
	ResolvedURL       string               `json:"resolved_url"`
	Timestamp         time.Time            `json:"timestamp"`
	Hash              string               `json:"hash"` // Content hash of bytes AS RECEIVED, before any normalisation
	BackendName       string               `json:"backend_name"`
	BackendVersion    string               `json:"backend_version"`
	Version           string               `json:"version"` // Backward-compatible alias for BackendVersion
	IsFallback        bool                 `json:"is_fallback"`
	Payload           []byte               `json:"payload,omitempty"`     // Raw bytes as received
	RawPayload        []byte               `json:"raw_payload,omitempty"` // Explicit alias for raw bytes
	BackendArgs       []string             `json:"backend_args,omitempty"`
	Normalisation     *NormalisationRecord `json:"normalisation,omitempty"`
	NormalisedPayload []byte               `json:"normalised_payload,omitempty"`
	BackendStatus     string               `json:"backend_status,omitempty"`
	MissingBackends   []string             `json:"missing_backends,omitempty"`
	BackendAttempts   []BackendAttempt     `json:"backend_attempts,omitempty"`
	RecordHash        string               `json:"record_hash,omitempty"`
	PrevRecordHash    string               `json:"prev_record_hash,omitempty"`
}

// RawBytes returns the raw bytes as received.
func (r *Record) RawBytes() []byte {
	if len(r.Payload) > 0 {
		return r.Payload
	}
	return r.RawPayload
}

// Validate verifies that all required fields are present, timestamp is UTC,
// raw bytes match the content hash, and no credentials leaked into the record.
func (r *Record) Validate() error {
	if r.ResolvedURL == "" {
		return fmt.Errorf("%w: resolved URL is required", ErrMissingField)
	}
	if HasCredentials(r.ResolvedURL) {
		return fmt.Errorf("%w: resolved URL contains credentials", ErrCredentialLeak)
	}
	if r.Timestamp.IsZero() {
		return fmt.Errorf("%w: timestamp is required", ErrMissingField)
	}
	if r.Timestamp.Location() != time.UTC {
		return fmt.Errorf("timestamp must be in UTC, got %v", r.Timestamp.Location())
	}
	if r.Hash == "" {
		return fmt.Errorf("%w: content hash is required", ErrMissingField)
	}
	if r.BackendName == "" {
		return fmt.Errorf("%w: backend name is required", ErrMissingField)
	}
	version := r.BackendVersion
	if version == "" {
		version = r.Version
	}
	if version == "" {
		return fmt.Errorf("%w: backend version is required", ErrMissingField)
	}

	// A registered secret is refused wherever it appears. The structural checks
	// below describe credentials by shape; this one catches a value a caller
	// told us about, which is how a user-supplied session cookie is guarded.
	if ContainsSecret(r.ResolvedURL) {
		return fmt.Errorf("%w: registered secret in resolved URL", ErrCredentialLeak)
	}
	if ContainsSecret(r.BackendName) {
		return fmt.Errorf("%w: registered secret in backend name", ErrCredentialLeak)
	}
	for _, mb := range r.MissingBackends {
		if ContainsSecret(mb) {
			return fmt.Errorf("%w: registered secret in missing backend entry", ErrCredentialLeak)
		}
	}
	for _, attempt := range r.BackendAttempts {
		if ContainsSecret(attempt.Error) {
			return fmt.Errorf("%w: registered secret in backend attempt (%s)", ErrCredentialLeak, attempt.BackendName)
		}
	}

	for i, arg := range r.BackendArgs {
		if ArgHasCredentials(arg) {
			return fmt.Errorf("%w: backend argument contains unredacted credential: %q", ErrCredentialLeak, arg)
		}
		if ContainsSecret(arg) {
			return fmt.Errorf("%w: backend argument contains a registered secret", ErrCredentialLeak)
		}
		if isCredentialFlag(arg) && i+1 < len(r.BackendArgs) {
			if r.BackendArgs[i+1] != "[REDACTED]" {
				return fmt.Errorf("%w: backend flag %q followed by unredacted credential: %q", ErrCredentialLeak, arg, r.BackendArgs[i+1])
			}
		}
		if (arg == "-H" || arg == "--header" || arg == "-header") && i+1 < len(r.BackendArgs) {
			if headerHasUnredactedCredential(r.BackendArgs[i+1]) {
				return fmt.Errorf("%w: header argument contains unredacted credential: %q", ErrCredentialLeak, r.BackendArgs[i+1])
			}
		}
	}

	raw := r.RawBytes()
	if len(raw) > 0 {
		actualHash := fmt.Sprintf("%x", sha256.Sum256(raw))
		if actualHash != r.Hash {
			return fmt.Errorf("%w: expected %s, got %s", ErrPayloadMismatch, r.Hash, actualHash)
		}
		// Raw bytes are never rewritten -- changing them would break the hash
		// that makes the record checkable. A registered secret in the payload
		// is refused instead, so the credential is not recorded and the fetch
		// is reported as failed rather than silently altered.
		if ContainsSecret(string(raw)) {
			return fmt.Errorf("%w: payload contains a registered secret", ErrCredentialLeak)
		}
	}

	if r.RecordHash != "" {
		expected := r.ComputeRecordHash()
		if r.RecordHash != expected {
			return fmt.Errorf("%w: record hash mismatch: expected %s, got %s", ErrTamperingDetected, expected, r.RecordHash)
		}
	}

	return nil
}

// ComputeRecordHash computes a deterministic cryptographic hash over the record's canonical fields.
func (r *Record) ComputeRecordHash() string {
	version := r.BackendVersion
	if version == "" {
		version = r.Version
	}
	ts := r.Timestamp.UTC().Format(time.RFC3339Nano)
	canonical := fmt.Sprintf("url=%s\nts=%s\nhash=%s\nbackend=%s\nversion=%s\nfallback=%t\nprev=%s\n",
		r.ResolvedURL, ts, r.Hash, r.BackendName, version, r.IsFallback, r.PrevRecordHash)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
}

var sensitiveKeyPattern = []string{
	"token", "cookie", "key", "secret", "auth", "pass", "session",
	"credential", "sig", "signature", "jwt", "apikey", "api_key",
}

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, pattern := range sensitiveKeyPattern {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// SanitizeURL strips userinfo and redacts credential query parameters from a URL.
func SanitizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	// Redact user credentials
	u.User = nil

	// Redact sensitive query parameters
	if u.RawQuery != "" {
		q := u.Query()
		modified := false
		for key := range q {
			if isSensitiveKey(key) {
				q.Set(key, "[REDACTED]")
				modified = true
			}
		}
		if modified {
			u.RawQuery = q.Encode()
		}
	}
	// A registered secret that the key-name heuristic did not recognise is
	// still scrubbed. Structural redaction handles credentials described as
	// credentials; this handles the value itself.
	return Redact(u.String())
}

// HasCredentials returns true if the URL carries user credentials or unredacted sensitive query parameters.
func HasCredentials(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.User != nil {
		return true
	}
	q := u.Query()
	for key, values := range q {
		if isSensitiveKey(key) {
			for _, val := range values {
				if val != "[REDACTED]" {
					return true
				}
			}
		}
	}
	return false
}

func isCredentialFlag(flag string) bool {
	lower := strings.ToLower(flag)
	flags := []string{
		"--cookie", "-b", "-cookie", "--cookies",
		"-u", "--user", "--token", "--api-key", "--apikey",
		"--key", "--password", "--passwd", "--secret", "--session",
	}
	for _, f := range flags {
		if lower == f {
			return true
		}
	}
	return false
}

func redactHeader(header string) string {
	colonIdx := strings.Index(header, ":")
	if colonIdx == -1 {
		return header
	}
	name := strings.TrimSpace(header[:colonIdx])
	lowerName := strings.ToLower(name)
	if isSensitiveKey(lowerName) || lowerName == "authorization" || lowerName == "proxy-authorization" {
		return name + ": [REDACTED]"
	}
	return header
}

// SanitizeArgs scrubs credentials from command-line arguments.
func SanitizeArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	sanitized := make([]string, len(args))
	copy(sanitized, args)

	for i := 0; i < len(sanitized); i++ {
		arg := sanitized[i]

		if isCredentialFlag(arg) && i+1 < len(sanitized) {
			sanitized[i+1] = "[REDACTED]"
			i++
			continue
		}

		if eqIdx := strings.Index(arg, "="); eqIdx != -1 {
			flag := arg[:eqIdx]
			if isCredentialFlag(flag) {
				sanitized[i] = flag + "=[REDACTED]"
				continue
			}
		}

		if (arg == "-H" || arg == "--header" || arg == "-header") && i+1 < len(sanitized) {
			sanitized[i+1] = redactHeader(sanitized[i+1])
			i++
			continue
		}
		if strings.HasPrefix(arg, "-H=") || strings.HasPrefix(arg, "--header=") {
			eq := strings.Index(arg, "=")
			sanitized[i] = arg[:eq+1] + redactHeader(arg[eq+1:])
			continue
		}

		// Inline patterns
		sanitized[i] = redactInlineString(sanitized[i])
	}

	// Final pass: scrub any registered credential value the structural rules
	// did not recognise. This is the boundary a user-supplied session cookie
	// is guarded by, independent of how the backend argument is spelled.
	for i := range sanitized {
		sanitized[i] = Redact(sanitized[i])
	}

	return sanitized
}

func redactInlineString(s string) string {
	lower := strings.ToLower(s)
	for _, prefix := range []string{"bearer ", "cookie: ", "token=", "session="} {
		if strings.Contains(lower, prefix) {
			idx := strings.Index(lower, prefix)
			return s[:idx+len(prefix)] + "[REDACTED]"
		}
	}
	return s
}

func headerHasUnredactedCredential(header string) bool {
	colonIdx := strings.Index(header, ":")
	if colonIdx == -1 {
		return false
	}
	name := strings.TrimSpace(header[:colonIdx])
	lowerName := strings.ToLower(name)
	val := strings.TrimSpace(header[colonIdx+1:])
	if isSensitiveKey(lowerName) || lowerName == "authorization" || lowerName == "proxy-authorization" {
		return val != "[REDACTED]"
	}
	return false
}

// ArgHasCredentials returns true if the argument contains unredacted credential patterns.
func ArgHasCredentials(arg string) bool {
	lower := strings.ToLower(arg)
	for _, prefix := range []string{
		"--cookie=", "-b=", "-cookie=", "--cookies=",
		"-u=", "--user=", "--token=", "--api-key=", "--apikey=",
		"--key=", "--password=", "--passwd=", "--secret=", "--session=",
	} {
		if strings.HasPrefix(lower, prefix) {
			return !strings.HasSuffix(arg, "=[REDACTED]")
		}
	}
	if strings.Contains(lower, "cookie:") || strings.Contains(lower, "authorization:") || strings.Contains(lower, "proxy-authorization:") {
		return headerHasUnredactedCredential(arg)
	}
	if strings.Contains(lower, "bearer ") {
		return !strings.HasSuffix(arg, "[REDACTED]")
	}
	for _, pattern := range []string{"token=", "session=", "api_key=", "secret="} {
		if strings.Contains(lower, pattern) {
			return !strings.HasSuffix(arg, "[REDACTED]")
		}
	}
	return false
}
