package security

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pipeToShell matches a download piped into a shell, the fetch-and-execute
// install pattern SECURITY.md forbids. It is applied to the release workflow
// and to the install documentation, never to the product's own source.
var pipeToShell = regexp.MustCompile(`(?i)(curl|wget|iwr|invoke-webrequest)[^|\n]*\|\s*(ba)?sh`)

// Done when: 4. Release binaries publish checksums.
//
// The release process is a workflow. This test reads it and fails if the
// checksum generation step or its artifact disappears, so the claim cannot
// quietly become false while the prose stays the same.
//
// Sabotage check: delete the "Generate checksums" step from release.yml and
// this named test fails. Restore it and it passes again.
func TestReleaseBinariesPublishChecksums(t *testing.T) {
	root := repoRoot(t)
	workflowPath := filepath.Join(root, ".github", "workflows", "release.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("release workflow is missing or unreadable: %v", err)
	}
	workflow := string(data)

	for _, want := range []struct{ needle, why string }{
		{"checksums.txt", "the published checksum manifest"},
		{"sha256sum", "the checksum generation command"},
		{"go build", "the binary build step"},
		{"tags:", "the tag trigger"},
	} {
		if !strings.Contains(workflow, want.needle) {
			t.Errorf("release workflow does not contain %q (%s)", want.needle, want.why)
		}
	}

	if match := pipeToShell.FindString(workflow); match != "" {
		t.Errorf("release workflow contains a fetch-and-execute install pattern: %q", match)
	}
}

// Done when: 4 (the documented half). The documented install path uses the
// published checksums and never tells a reader to fetch and execute a remote
// document. The install document must carry a real verification command, not
// merely the word "checksum", and the README must point at it.
//
// Sabotage check: remove the checksum verification command from
// docs/INSTALL.md (or the link to it from README.md) and this named test fails.
// Restore it and it passes again.
func TestDocumentedInstallPathVerifiesChecksums(t *testing.T) {
	root := repoRoot(t)
	docs := map[string]string{
		"README.md":       filepath.Join(root, "README.md"),
		"docs/INSTALL.md": filepath.Join(root, "docs", "INSTALL.md"),
	}
	for name, path := range docs {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s is missing or unreadable: %v", name, err)
		}
		content := string(data)

		if !strings.Contains(content, "checksums.txt") {
			t.Errorf("%s does not document the published checksums file", name)
		}
		if match := pipeToShell.FindString(content); match != "" {
			t.Errorf("%s documents a fetch-and-execute install path: %q", name, match)
		}
	}

	install, err := os.ReadFile(filepath.Join(root, "docs", "INSTALL.md"))
	if err != nil {
		t.Fatalf("docs/INSTALL.md is missing or unreadable: %v", err)
	}
	if !containsAny(string(install), "sha256sum", "shasum", "Get-FileHash") {
		t.Error("docs/INSTALL.md documents no actual checksum verification command")
	}

	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("README.md is missing or unreadable: %v", err)
	}
	if !strings.Contains(string(readme), "docs/INSTALL.md") {
		t.Error("README.md does not link to the documented checksum-verified install path")
	}
}

// containsAny reports whether text contains at least one of the needles.
func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
