package synthesis

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/evidence"
)

// seedRecord saves a real evidence record and returns its content hash, the
// same value store.Get addresses it by.
func seedRecord(t *testing.T, store *evidence.MemoryStore, payload, resolvedURL, backend string, ts time.Time) string {
	t.Helper()
	sum := sha256.Sum256([]byte(payload))
	hash := fmt.Sprintf("%x", sum)
	rec := &evidence.Record{
		ResolvedURL:    resolvedURL,
		Timestamp:      ts.UTC(),
		Hash:           hash,
		BackendName:    backend,
		BackendVersion: "1.0.0",
		Version:        "1.0.0",
		Payload:        []byte(payload),
		RawPayload:     []byte(payload),
	}
	if err := store.Save(rec); err != nil {
		t.Fatalf("failed to seed record: %v", err)
	}
	return hash
}

func mustClaim(t *testing.T, text string, hashes ...string) Claim {
	t.Helper()
	c, err := NewClaim(text, hashes...)
	if err != nil {
		t.Fatalf("NewClaim(%q, %v) failed: %v", text, hashes, err)
	}
	return c
}

// TestEveryClaimResolvesToStoredRecord is the mechanical check for the first
// "done when" line: every hash a claim is attributed to must exist in the store
// and be retrievable with store.Get. It also proves Build refuses a claim whose
// record is absent, which is what makes the property structural rather than a
// matter of reviewer discipline.
func TestEveryClaimResolvesToStoredRecord(t *testing.T) {
	store := evidence.NewMemoryStore()
	ts := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	hnHash := seedRecord(t, store, "hn thread about acme", "https://hn.algolia.com/api/v1/items/1", "curl", ts)
	ghHash := seedRecord(t, store, "github issue about acme", "https://api.github.com/repos/acme/acme/issues/2", "gh", ts.Add(time.Hour))

	hnOutcome, err := ReachedSource("hacker-news", hnHash)
	if err != nil {
		t.Fatalf("ReachedSource: %v", err)
	}
	ghOutcome, err := ReachedSource("github", ghHash)
	if err != nil {
		t.Fatalf("ReachedSource: %v", err)
	}

	ans, err := Build(store, Config{
		Question: "What do people say about Acme?",
		Sources:  []string{"hacker-news", "github"},
		Outcomes: []Outcome{hnOutcome, ghOutcome},
		Claims: []Claim{
			mustClaim(t, "A thread discusses Acme", hnHash),
			mustClaim(t, "An issue reports a failure", ghHash, hnHash),
		},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	for _, c := range ans.Claims() {
		if len(c.Hashes()) == 0 {
			t.Fatalf("claim %q has no hashes", c.Text())
		}
		for _, h := range c.Hashes() {
			if _, err := store.Get(h); err != nil {
				t.Errorf("claim %q references hash %s that store.Get cannot resolve: %v", c.Text(), h, err)
			}
		}
	}

	// A claim attributed to a hash that is not in the store must not be
	// generated. Build has to refuse it.
	absent := strings.Repeat("f", 64)
	orphan := mustClaim(t, "A claim with no stored record", absent)
	if _, err := Build(store, Config{
		Question: "What do people say about Acme?",
		Sources:  []string{"hacker-news"},
		Outcomes: []Outcome{hnOutcome},
		Claims:   []Claim{orphan},
	}); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("Build accepted a claim with an absent record: got err %v, want ErrUnknownRecord", err)
	}

	// A zero-value Claim cannot slip past either, even without NewClaim.
	if _, err := Build(store, Config{
		Question: "What do people say about Acme?",
		Claims:   []Claim{{}},
	}); !errors.Is(err, ErrUnattributedClaim) {
		t.Fatalf("Build accepted an empty claim: got err %v, want ErrUnattributedClaim", err)
	}

	// The rendered attribution list must equal the claim hashes.
	raw, err := ans.JSON()
	if err != nil {
		t.Fatalf("JSON failed: %v", err)
	}
	var doc struct {
		AttributionRecords []string `json:"attribution_records"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal answer: %v", err)
	}
	if len(doc.AttributionRecords) != 2 {
		t.Fatalf("attribution_records = %v, want 2 entries", doc.AttributionRecords)
	}
	for _, h := range doc.AttributionRecords {
		if _, err := store.Get(h); err != nil {
			t.Errorf("attribution_records contains unresolvable hash %s: %v", h, err)
		}
	}

	// The most recent record line is itself attributed, so even a derived
	// factual sentence names the record it came from.
	if !strings.Contains(ans.String(), "Most recent record: 2026-09-02 (record: "+ghHash+")") {
		t.Errorf("most recent record line is not attributed:\n%s", ans.String())
	}
	var latest struct {
		MostRecentRecordHash string `json:"most_recent_record_hash"`
	}
	if err := json.Unmarshal(raw, &latest); err != nil {
		t.Fatalf("unmarshal answer: %v", err)
	}
	if latest.MostRecentRecordHash != ghHash {
		t.Errorf("most_recent_record_hash = %q, want %q", latest.MostRecentRecordHash, ghHash)
	}
	if _, err := store.Get(latest.MostRecentRecordHash); err != nil {
		t.Errorf("most_recent_record_hash does not resolve: %v", err)
	}
}

// TestUnreachedSourceAppearsExplicitly is the check for the second "done when"
// line: a configured source that produced no evidence is named as unreached,
// never silently omitted. It covers both an explicit failure reason and the
// automatic case where a configured source has no outcome at all.
func TestUnreachedSourceAppearsExplicitly(t *testing.T) {
	store := evidence.NewMemoryStore()
	ts := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	hnHash := seedRecord(t, store, "hn thread", "https://hn.algolia.com/api/v1/items/1", "curl", ts)

	hnOutcome, err := ReachedSource("hacker-news", hnHash)
	if err != nil {
		t.Fatalf("ReachedSource: %v", err)
	}
	// LinkedIn was configured but no cookie is present, so it never ran.
	liOutcome, err := UnreachedSource("linkedin", "no session cookie configured")
	if err != nil {
		t.Fatalf("UnreachedSource: %v", err)
	}

	ans, err := Build(store, Config{
		// github is configured but the caller recorded no outcome for it.
		Question: "What do people say about Acme?",
		Sources:  []string{"hacker-news", "github", "linkedin"},
		Outcomes: []Outcome{hnOutcome, liOutcome},
		Claims:   []Claim{mustClaim(t, "A thread discusses Acme", hnHash)},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	out := ans.String()
	if !strings.Contains(out, "linkedin: unreached: no session cookie configured") {
		t.Errorf("rendered answer does not name linkedin as unreached with its reason:\n%s", out)
	}
	if !strings.Contains(out, "github issues: unreached: no outcome recorded") {
		t.Errorf("configured github with no outcome was silently omitted:\n%s", out)
	}
	// Both unreached sources must also be present in the structured coverage.
	unreached := map[string]string{}
	reached := map[string]bool{}
	for _, c := range ans.Coverage() {
		if c.Reached() {
			reached[c.Source()] = true
			continue
		}
		unreached[c.Source()] = c.Reason()
	}
	if !reached["hacker-news"] {
		t.Errorf("hacker-news should be reached, coverage = %+v", ans.Coverage())
	}
	if unreached["linkedin"] != "no session cookie configured" {
		t.Errorf("linkedin reason = %q, want %q", unreached["linkedin"], "no session cookie configured")
	}
	if _, ok := unreached["github"]; !ok {
		t.Errorf("github did not appear as unreached, coverage = %+v", ans.Coverage())
	}
}

// TestCaveatCannotBeSuppressed is the check for the third "done when" line:
// the representativeness sentence is in every output and there is no parameter,
// field, or config option that removes it. It asserts the sentence survives the
// minimal possible answer and both renderers, and that the only builder takes
// no variadic option and the config exposes no caveat switch.
func TestCaveatCannotBeSuppressed(t *testing.T) {
	store := evidence.NewMemoryStore()

	// The emptiest answer the one builder can produce still carries the caveat.
	ans, err := Build(store, Config{Question: "Anything?"})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if got := ans.Caveat(); !strings.Contains(got, RepresentativenessCaveat) {
		t.Errorf("Answer.Caveat() = %q, want it to contain %q", got, RepresentativenessCaveat)
	}
	if out := ans.String(); !strings.Contains(out, RepresentativenessCaveat) {
		t.Errorf("String() omitted the representativeness caveat:\n%s", out)
	}
	raw, err := ans.JSON()
	if err != nil {
		t.Fatalf("JSON failed: %v", err)
	}
	if !strings.Contains(string(raw), RepresentativenessCaveat) {
		t.Errorf("JSON() omitted the representativeness caveat:\n%s", raw)
	}

	// There is no optional parameter on the builder that could suppress it.
	if reflect.TypeOf(Build).IsVariadic() {
		t.Error("Build is variadic; a caller could pass an option that suppresses the caveat")
	}

	// The config exposes no field that toggles or replaces the caveat.
	ct := reflect.TypeOf(Config{})
	for i := 0; i < ct.NumField(); i++ {
		name := strings.ToLower(ct.Field(i).Name)
		for _, forbidden := range []string{"caveat", "represent", "limitation", "disclaimer"} {
			if strings.Contains(name, forbidden) {
				t.Errorf("Config exposes a %q field, which could configure the caveat away", ct.Field(i).Name)
			}
		}
	}

	// A populated answer carries it too.
	ts := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	h := seedRecord(t, store, "payload", "https://lobste.rs/hottest.json", "curl", ts)
	o, err := ReachedSource("lobsters", h)
	if err != nil {
		t.Fatalf("ReachedSource: %v", err)
	}
	ans, err = Build(store, Config{
		Question: "Anything?",
		Sources:  []string{"lobsters"},
		Outcomes: []Outcome{o},
		Claims:   []Claim{mustClaim(t, "A lobsters thread exists", h)},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if !strings.Contains(ans.String(), RepresentativenessCaveat) {
		t.Errorf("populated answer omitted the representativeness caveat:\n%s", ans.String())
	}
}

// TestNoSentimentScoreOrRatingInOutput enforces the third constraint: no
// numeric sentiment score, star rating, or similar derived metric appears
// anywhere in the answer. Only plain record counts are allowed.
func TestNoSentimentScoreOrRatingInOutput(t *testing.T) {
	store := evidence.NewMemoryStore()
	ts := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	h := seedRecord(t, store, "payload", "https://api.github.com/repos/acme/acme/issues/2", "gh", ts)
	o, err := ReachedSource("github", h)
	if err != nil {
		t.Fatalf("ReachedSource: %v", err)
	}
	ans, err := Build(store, Config{
		Question: "Is Acme good?",
		Sources:  []string{"github"},
		Outcomes: []Outcome{o},
		Claims:   []Claim{mustClaim(t, "An issue reports a failure", h)},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	raw, err := ans.JSON()
	if err != nil {
		t.Fatalf("JSON failed: %v", err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	forbidden := []string{"sentiment", "polarity", "score", "rating", "stars", "positive", "negative"}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch value := v.(type) {
		case map[string]any:
			for k, child := range value {
				key := strings.ToLower(k)
				for _, bad := range forbidden {
					if strings.Contains(key, bad) {
						t.Errorf("output key %q contains forbidden metric word %q", prefix+k, bad)
					}
				}
				walk(prefix+k+".", child)
			}
		case []any:
			for _, child := range value {
				walk(prefix, child)
			}
		}
	}
	walk("", doc)
}

// TestNewClaimRequiresARecord proves a claim cannot be constructed without a
// well-formed record hash, which is what keeps unattributed claims out of an
// answer before Build is even reached.
func TestNewClaimRequiresARecord(t *testing.T) {
	if _, err := NewClaim("No evidence here"); !errors.Is(err, ErrUnattributedClaim) {
		t.Errorf("NewClaim with no hash: got %v, want ErrUnattributedClaim", err)
	}
	if _, err := NewClaim("Bad hash", "not-a-hash"); !errors.Is(err, ErrInvalidHash) {
		t.Errorf("NewClaim with a malformed hash: got %v, want ErrInvalidHash", err)
	}
	if _, err := NewClaim("   ", strings.Repeat("a", 64)); !errors.Is(err, ErrUnattributedClaim) {
		t.Errorf("NewClaim with empty text: got %v, want ErrUnattributedClaim", err)
	}
	c, err := NewClaim("Backed claim", strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("NewClaim with a valid hash failed: %v", err)
	}
	if len(c.Hashes()) != 1 {
		t.Errorf("Hashes() = %v, want one entry", c.Hashes())
	}
}

// TestOutcomeConstructorsValidate proves a "reached" source cannot be recorded
// without naming a record, and an unreached source always carries a reason.
func TestOutcomeConstructorsValidate(t *testing.T) {
	if _, err := ReachedSource("hacker-news"); err == nil {
		t.Error("ReachedSource with no hash should fail")
	}
	if _, err := UnreachedSource("", "reason"); err == nil {
		t.Error("UnreachedSource with no source should fail")
	}
	o, err := UnreachedSource("linkedin", "  ")
	if err != nil {
		t.Fatalf("UnreachedSource: %v", err)
	}
	if o.Reason() != "unreachable" {
		t.Errorf("empty reason defaulted to %q, want %q", o.Reason(), "unreachable")
	}
}

// TestClaimAndReasonAreRedacted proves a registered credential cannot ride into
// a synthesized answer through claim text or an unreached reason.
func TestClaimAndReasonAreRedacted(t *testing.T) {
	evidence.ResetSecrets()
	t.Cleanup(evidence.ResetSecrets)

	secret := "session-cookie-value-123"
	evidence.RegisterSecret(secret)

	hash := strings.Repeat("b", 64)
	c, err := NewClaim("the page contained "+secret, hash)
	if err != nil {
		t.Fatalf("NewClaim: %v", err)
	}
	if strings.Contains(c.Text(), secret) {
		t.Errorf("claim text retained the secret: %q", c.Text())
	}
	o, err := UnreachedSource("twitter", "cookie "+secret+" rejected")
	if err != nil {
		t.Fatalf("UnreachedSource: %v", err)
	}
	if strings.Contains(o.Reason(), secret) {
		t.Errorf("unreached reason retained the secret: %q", o.Reason())
	}
}
