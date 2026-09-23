package keychain

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestMemoryProvider_SetGetDelete(t *testing.T) {
	p := NewMemoryProvider()

	// Not found
	_, err := p.Get("service", "user")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Set
	if err := p.Set("service", "user", "secret123"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Get
	val, err := p.Get("service", "user")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "secret123" {
		t.Fatalf("got %q, want %q", val, "secret123")
	}

	// Delete
	if err := p.Delete("service", "user"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Not found after delete
	_, err = p.Get("service", "user")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestFileProvider_SetGetDelete(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "keychain.json")
	p := NewFileProvider(filePath)

	// Not found initially
	_, err := p.Get("service", "user")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Set
	if err := p.Set("service", "user", "fileSecret456"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Read with a new instance pointing to same file
	p2 := NewFileProvider(filePath)
	val, err := p2.Get("service", "user")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "fileSecret456" {
		t.Fatalf("got %q, want %q", val, "fileSecret456")
	}

	// Delete
	if err := p2.Delete("service", "user"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify gone
	p3 := NewFileProvider(filePath)
	_, err = p3.Get("service", "user")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestKeychain_CookieHelpersWithCustomProvider(t *testing.T) {
	mem := NewMemoryProvider()
	SetProvider(mem)
	defer ResetProvider()

	// LinkedIn
	if err := SetLinkedInCookie("li_cookie_value"); err != nil {
		t.Fatalf("SetLinkedInCookie failed: %v", err)
	}
	val, err := LinkedInCookie()
	if err != nil {
		t.Fatalf("LinkedInCookie failed: %v", err)
	}
	if val != "li_cookie_value" {
		t.Fatalf("got %q, want %q", val, "li_cookie_value")
	}
	if err := DeleteLinkedInCookie(); err != nil {
		t.Fatalf("DeleteLinkedInCookie failed: %v", err)
	}
	_, err = LinkedInCookie()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Twitter
	if err := SetTwitterCookie("tw_cookie_value"); err != nil {
		t.Fatalf("SetTwitterCookie failed: %v", err)
	}
	twVal, err := TwitterCookie()
	if err != nil {
		t.Fatalf("TwitterCookie failed: %v", err)
	}
	if twVal != "tw_cookie_value" {
		t.Fatalf("got %q, want %q", twVal, "tw_cookie_value")
	}
	if err := DeleteTwitterCookie(); err != nil {
		t.Fatalf("DeleteTwitterCookie failed: %v", err)
	}
	_, err = TwitterCookie()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
