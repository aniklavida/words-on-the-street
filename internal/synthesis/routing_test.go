package synthesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/evidence"
)

func routingRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate test source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// Done when: 1. A workplace-culture question demonstrably routes away from the
// source where nobody criticises anyone.
//
// Proved against the routing specification and skill docs: workplace-culture
// questions evaluate LinkedIn's incentive structure (performative optimism and
// professional self-censorship, where negative feedback is not posted) and
// explicitly route away from it, choosing pseudonymous technical communities
// (hacker-news, lobsters) instead.
func TestRouting_WorkplaceCultureRoutesAwayFromUncriticalSource(t *testing.T) {
	root := routingRepoRoot(t)
	files := []string{
		filepath.Join(root, "docs", "ROUTING.md"),
		filepath.Join(root, "routing", "SKILL.md"),
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read %s: %v", path, err)
		}
		content := string(data)

		// Must contain the workplace culture worked example
		if !strings.Contains(content, "Workplace culture") && !strings.Contains(content, "workplace culture") {
			t.Errorf("%s missing workplace culture routing section", path)
		}

		// Must explicitly route away from linkedin
		if !strings.Contains(content, "ROUTE AWAY from `linkedin`") && !strings.Contains(content, "Route EXPLICITLY AWAY from `linkedin`") {
			t.Errorf("%s does not explicitly route away from linkedin for workplace culture", path)
		}

		// Must name the reason (nobody criticises anyone / self-censorship / cheerleading)
		hasReason := strings.Contains(content, "nobody criticises") ||
			strings.Contains(content, "nobody criticizes") ||
			strings.Contains(content, "self-censorship")
		if !hasReason {
			t.Errorf("%s does not state the self-censorship / nobody criticises reason for routing away from linkedin", path)
		}

		// Must route toward hacker-news and lobsters
		if !strings.Contains(content, "hacker-news") || !strings.Contains(content, "lobsters") {
			t.Errorf("%s does not route toward hacker-news and lobsters for workplace culture", path)
		}
	}
}

// Done when: 2. A library-quality question routes to issue trackers and technical
// communities.
//
// Proved against the routing specification and skill docs: library-quality
// questions route to github (issue trackers) and technical communities
// (hacker-news, lobsters) to find real failure modes, and route away from
// hype/outrage surfaces (twitter).
func TestRouting_LibraryQualityRoutesToIssueTrackersAndTechnicalCommunities(t *testing.T) {
	root := routingRepoRoot(t)
	files := []string{
		filepath.Join(root, "docs", "ROUTING.md"),
		filepath.Join(root, "routing", "SKILL.md"),
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read %s: %v", path, err)
		}
		content := string(data)

		// Must contain the library quality worked example
		if !strings.Contains(content, "Library and tool quality") && !strings.Contains(content, "library Y") {
			t.Errorf("%s missing library quality routing section", path)
		}

		// Must route toward github (issue trackers)
		if !strings.Contains(content, "github") || !strings.Contains(content, "issue") {
			t.Errorf("%s does not route toward github issue trackers for library quality", path)
		}

		// Must route toward technical communities
		if !strings.Contains(content, "hacker-news") || !strings.Contains(content, "lobsters") {
			t.Errorf("%s does not route toward hacker-news and lobsters for library quality", path)
		}

		// Must route explicitly away from twitter
		if !strings.Contains(content, "ROUTE AWAY from `twitter`") && !strings.Contains(content, "Route EXPLICITLY AWAY from `twitter`") {
			t.Errorf("%s does not explicitly route away from twitter for library quality", path)
		}
	}
}

// Done when: 3. The chosen sources and the reason appear in the answer.
//
// Proved against the synthesis engine: when an answer is built with chosen
// sources and unreached sources with reasons, all chosen sources and reasons
// appear in the rendered answer and structured JSON.
func TestRouting_ChosenSourcesAndReasonAppearInAnswer(t *testing.T) {
	store := evidence.NewMemoryStore()
	ts := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	hnHash := seedRecord(t, store, "HN discussion on engineering culture", "https://hn.algolia.com/api/v1/items/1", "curl", ts)

	hnOutcome, err := ReachedSource("hacker-news", hnHash)
	if err != nil {
		t.Fatalf("ReachedSource failed: %v", err)
	}

	exclusionReason := "professional self-censorship prevents negative feedback"
	liOutcome, err := UnreachedSource("linkedin", exclusionReason)
	if err != nil {
		t.Fatalf("UnreachedSource failed: %v", err)
	}

	ans, err := Build(store, Config{
		Question: "How is the engineering culture at Acme Corp?",
		Sources:  []string{"hacker-news", "linkedin"},
		Outcomes: []Outcome{hnOutcome, liOutcome},
		Claims: []Claim{
			mustClaim(t, "Engineers report high on-call pager burden in infra team", hnHash),
		},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	rendered := ans.String()
	// Chosen source must appear in answer coverage
	if !strings.Contains(rendered, "hacker-news: 1 record(s)") {
		t.Errorf("rendered answer missing chosen source 'hacker-news':\n%s", rendered)
	}
	// Excluded/unreached source and reason must appear in answer
	if !strings.Contains(rendered, "linkedin: unreached: "+exclusionReason) {
		t.Errorf("rendered answer missing unreached source with reason:\n%s", rendered)
	}

	// Verify structured JSON
	raw, err := ans.JSON()
	if err != nil {
		t.Fatalf("JSON failed: %v", err)
	}
	var doc struct {
		Coverage []struct {
			Source  string `json:"source"`
			Reached bool   `json:"reached"`
			Reason  string `json:"reason,omitempty"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	foundHN := false
	foundLI := false
	for _, c := range doc.Coverage {
		if c.Source == "hacker-news" && c.Reached {
			foundHN = true
		}
		if c.Source == "linkedin" && !c.Reached && c.Reason == exclusionReason {
			foundLI = true
		}
	}
	if !foundHN {
		t.Errorf("JSON coverage missing reached hacker-news: %+v", doc.Coverage)
	}
	if !foundLI {
		t.Errorf("JSON coverage missing unreached linkedin with reason: %+v", doc.Coverage)
	}
}
