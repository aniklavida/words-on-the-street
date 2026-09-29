package security

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// publicDoc returns the path to a documentation file, failing if it is missing.
func publicDoc(t *testing.T, root, rel string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("public document %s is missing or unreadable: %v", rel, err)
	}
	return path
}

// Done when: 1. The README carries all three limit sentences together.
//
// The product's honesty rests on three sentences that must ship as a set.
// Publishing only the part that flatters the product turns a narrow true claim
// into a wide false one. This reads the README and fails if any of the three
// concepts goes missing.
//
// Sabotage check: delete the "What is NOT proved" sentence from README.md and
// this named test fails. Restore it and it passes again.
func TestHonesty_ReadmeCarriesAllThreeLimitSentencesTogether(t *testing.T) {
	root := repoRoot(t)
	readme := publicDoc(t, root, "README.md")
	data, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("failed to read README.md: %v", err)
	}
	lower := strings.ToLower(string(data))

	limits := []struct{ name, needle string }{
		{"what is proved", "what is proved"},
		{"what is not proved", "what is not proved"},
		{"what is not representative", "not representative"},
	}
	for _, limit := range limits {
		if !strings.Contains(lower, limit.needle) {
			t.Errorf("README.md is missing the %q limit sentence", limit.name)
		}
	}

	// The README must state the three travel together, so the rule is visible
	// to a reader and not only enforced by this test.
	if !strings.Contains(lower, "all three ship together") {
		t.Error("README.md does not state that the three limits ship together")
	}

	// The same three are restated in the specification, so the canonical
	// wording and the user-facing wording cannot drift apart.
	spec := publicDoc(t, root, "docs/SPEC.md")
	specData, err := os.ReadFile(spec)
	if err != nil {
		t.Fatalf("failed to read docs/SPEC.md: %v", err)
	}
	specLower := strings.ToLower(string(specData))
	for _, limit := range limits {
		if !strings.Contains(specLower, limit.needle) {
			t.Errorf("docs/SPEC.md is missing the %q limit sentence", limit.name)
		}
	}
}

// Done when: 2. Release notes state plainly what is unverified, including
// endpoint instability on any source read through a user session.
//
// The CHANGELOG is the release-notes surface. It has to say which parts are not
// proven rather than listing only what was added, and it has to name the
// session-cookie sources whose endpoints a fixture cannot pin down.
//
// Sabotage check: remove the "Unverified" section from CHANGELOG.md and this
// named test fails. Restore it and it passes again.
func TestHonesty_ReleaseNotesStateUnverifiedEndpointInstability(t *testing.T) {
	root := repoRoot(t)
	changelog := publicDoc(t, root, "CHANGELOG.md")
	data, err := os.ReadFile(changelog)
	if err != nil {
		t.Fatalf("failed to read CHANGELOG.md: %v", err)
	}
	lower := strings.ToLower(string(data))

	for _, want := range []struct{ needle, why string }{
		{"unverified", "the release notes must say what is not verified"},
		{"experimental", "the session sources must be marked experimental"},
		{"endpoint instability", "the specific endpoint risk must be named"},
		{"twitter", "the session sources must be named"},
		{"linkedin", "the session sources must be named"},
		{"session", "the notes must say the risk is on session-read sources"},
	} {
		if !strings.Contains(lower, want.needle) {
			t.Errorf("CHANGELOG.md does not contain %q (%s)", want.needle, want.why)
		}
	}
}

// referenceProjectPattern names well-known projects in this tool's space. The
// checklist forbids naming a reference project in a public document; the
// project's own honesty rule is that it describes its behaviour, not whose
// footsteps it follows. Attribution that the law requires would be an explicit
// exception, and none of these names appears in this repository at all.
var referenceProjectPattern = regexp.MustCompile(`(?i)\b(perplexity|gpt-researcher|open-?deep-?research|firecrawl|tavily|storm|manus|openmanus|deerflow|morphic|exa)\b`)

// Done when: 3. No public document names a reference project except where
// attribution is legally required.
//
// This scans every Markdown document a reader would call public and fails if
// one of the named comparable projects appears. Attribution that the law
// requires is not present here, so the exception does not apply.
//
// Sabotage check: add the sentence "Unlike Perplexity, ..." to README.md and
// this named test fails. Remove it and it passes again.
func TestHonesty_NoPublicDocumentNamesAReferenceProject(t *testing.T) {
	root := repoRoot(t)

	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++
		if match := referenceProjectPattern.FindString(string(data)); match != "" {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s names reference project %q", rel, match)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to scan public documents: %v", err)
	}
	if scanned == 0 {
		t.Fatal("no public documents were scanned")
	}
}

// Done when: 4. No install path instructs an agent to fetch and execute a
// remote document.
//
// This is the whole-repository version of the install-path check: any public
// document that pipes a download into a shell is exactly the remote
// command-execution pattern SECURITY.md refuses, wherever it appears.
//
// Sabotage check: add a line to docs/INSTALL.md that pipes a curl download of
// an install script straight into sh, and this named test fails. Remove it and
// it passes again. (The literal pattern is not written here because the
// repository's own validate workflow rejects it in any tracked file.)
func TestHonesty_NoPublicDocumentInstructsFetchAndExecute(t *testing.T) {
	root := repoRoot(t)

	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++
		if match := pipeToShell.FindString(string(data)); match != "" {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s documents a fetch-and-execute install path: %q", rel, match)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to scan public documents: %v", err)
	}
	if scanned == 0 {
		t.Fatal("no public documents were scanned")
	}
}
