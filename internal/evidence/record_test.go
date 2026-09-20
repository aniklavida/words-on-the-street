package evidence

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRecord_Validation(t *testing.T) {
	raw := []byte("hello evidence world")
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))

	validRec := &Record{
		ResolvedURL:    "https://example.com/posts/1",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "curl",
		BackendVersion: "8.4.0",
		IsFallback:     false,
		Payload:        raw,
	}

	if err := validRec.Validate(); err != nil {
		t.Fatalf("expected valid record to pass validation, got: %v", err)
	}

	// Missing URL
	noURL := *validRec
	noURL.ResolvedURL = ""
	if err := noURL.Validate(); err == nil {
		t.Error("expected error for missing ResolvedURL, got nil")
	}

	// Missing Timestamp
	noTime := *validRec
	noTime.Timestamp = time.Time{}
	if err := noTime.Validate(); err == nil {
		t.Error("expected error for zero Timestamp, got nil")
	}

	// Non-UTC Timestamp
	loc, _ := time.LoadLocation("EST")
	if loc == nil {
		loc = time.FixedZone("EST", -5*3600)
	}
	estTime := *validRec
	estTime.Timestamp = time.Now().In(loc)
	if err := estTime.Validate(); err == nil {
		t.Error("expected error for non-UTC Timestamp, got nil")
	}

	// Missing Hash
	noHash := *validRec
	noHash.Hash = ""
	if err := noHash.Validate(); err == nil {
		t.Error("expected error for missing Hash, got nil")
	}

	// Missing BackendName
	noBackend := *validRec
	noBackend.BackendName = ""
	if err := noBackend.Validate(); err == nil {
		t.Error("expected error for missing BackendName, got nil")
	}

	// Missing BackendVersion
	noVersion := *validRec
	noVersion.BackendVersion = ""
	noVersion.Version = ""
	if err := noVersion.Validate(); err == nil {
		t.Error("expected error for missing BackendVersion, got nil")
	}
}

func TestRecord_PayloadHashMismatch(t *testing.T) {
	raw := []byte("original raw bytes")
	wrongHash := fmt.Sprintf("%x", sha256.Sum256([]byte("different bytes")))

	rec := &Record{
		ResolvedURL:    "https://example.com",
		Timestamp:      time.Now().UTC(),
		Hash:           wrongHash,
		BackendName:    "curl",
		BackendVersion: "1.0",
		Payload:        raw,
	}

	err := rec.Validate()
	if err == nil {
		t.Fatal("expected validation error for payload hash mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "payload hash does not match") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRecord_RejectsCredentials(t *testing.T) {
	raw := []byte("clean payload")
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))

	// URL with userinfo credential
	recUserinfo := &Record{
		ResolvedURL:    "https://admin:supersecret@example.com/api",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "curl",
		BackendVersion: "1.0",
		Payload:        raw,
	}
	if err := recUserinfo.Validate(); err == nil {
		t.Error("expected validation to reject URL with userinfo credentials, got nil")
	}

	// URL with sensitive query token
	recQueryToken := &Record{
		ResolvedURL:    "https://example.com/api?token=secret12345",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "curl",
		BackendVersion: "1.0",
		Payload:        raw,
	}
	if err := recQueryToken.Validate(); err == nil {
		t.Error("expected validation to reject URL with query token, got nil")
	}

	// Backend args with unredacted cookie
	recCookieArg := &Record{
		ResolvedURL:    "https://example.com/api",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "curl",
		BackendVersion: "1.0",
		Payload:        raw,
		BackendArgs:    []string{"--cookie", "session=secretcookievalue"},
	}
	if err := recCookieArg.Validate(); err == nil {
		t.Error("expected validation to reject unredacted cookie in backend args, got nil")
	}

	// Backend args with unredacted authorization header
	recAuthHeader := &Record{
		ResolvedURL:    "https://example.com/api",
		Timestamp:      time.Now().UTC(),
		Hash:           hash,
		BackendName:    "curl",
		BackendVersion: "1.0",
		Payload:        raw,
		BackendArgs:    []string{"-H", "Authorization: Bearer mysecrettoken"},
	}
	if err := recAuthHeader.Validate(); err == nil {
		t.Error("expected validation to reject unredacted authorization header, got nil")
	}
}

func TestSanitizeURL_RedactsCredentials(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "https://user:password@example.com/data",
			expected: "https://example.com/data",
		},
		{
			input:    "https://example.com/api?token=secrettoken&query=golang",
			expected: "https://example.com/api?query=golang&token=%5BREDACTED%5D",
		},
		{
			input:    "https://example.com/api?session_id=secret123&api_key=key456",
			expected: "https://example.com/api?api_key=%5BREDACTED%5D&session_id=%5BREDACTED%5D",
		},
		{
			input:    "https://example.com/public?page=1&limit=20",
			expected: "https://example.com/public?limit=20&page=1",
		},
	}

	for _, tt := range tests {
		got := SanitizeURL(tt.input)
		if HasCredentials(got) {
			t.Errorf("SanitizeURL(%q) = %q still contains credentials", tt.input, got)
		}
		if tt.input == "https://user:password@example.com/data" && strings.Contains(got, "password") {
			t.Errorf("SanitizeURL leaked password: %q", got)
		}
		if strings.Contains(tt.input, "secret") && strings.Contains(got, "secret") {
			t.Errorf("SanitizeURL leaked secret: %q", got)
		}
	}
}

func TestSanitizeArgs_RedactsCredentials(t *testing.T) {
	args := []string{
		"-s",
		"--cookie", "session_id=supersecretcookie123",
		"-H", "Cookie: auth=supersecretauth456",
		"-H", "Authorization: Bearer mybearertoken789",
		"-H", "Content-Type: application/json",
		"--token=secrettokenabc",
		"--user", "alice:secretpass",
		"https://example.com",
	}

	sanitized := SanitizeArgs(args)

	for _, arg := range sanitized {
		if ArgHasCredentials(arg) {
			t.Errorf("SanitizeArgs left unredacted credential in argument: %q", arg)
		}
		for _, secret := range []string{
			"supersecretcookie123",
			"supersecretauth456",
			"mybearertoken789",
			"secrettokenabc",
			"alice:secretpass",
		} {
			if strings.Contains(arg, secret) {
				t.Errorf("SanitizeArgs leaked secret %q in %q", secret, arg)
			}
		}
	}

	// Verify non-sensitive args were preserved
	joined := strings.Join(sanitized, " ")
	if !strings.Contains(joined, "Content-Type: application/json") {
		t.Errorf("SanitizeArgs modified non-sensitive header: %s", joined)
	}
	if !strings.Contains(joined, "https://example.com") {
		t.Errorf("SanitizeArgs modified URL: %s", joined)
	}
}
