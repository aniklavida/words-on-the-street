// Package synthesis builds an answer from stored evidence records so that every
// factual claim resolves to a record that can be independently re-verified with
// store.Get. It is stage four of the five-stage pipeline described in
// docs/SPEC.md and docs/ROUTING.md.
//
// Three rules are structural properties of this package, not conventions a
// reviewer has to check by eye:
//
//  1. A claim cannot exist without at least one record hash. The fields of
//     Claim are unexported and NewClaim refuses an empty hash list, so a claim
//     with no record behind it cannot be built; Build then resolves every hash
//     against the store and refuses the whole answer if any hash does not
//     resolve. There is no rendering path that emits an unattributed claim.
//  2. Every source configured for the question appears in the coverage list.
//     A source with no successful outcome is rendered as "unreached: <reason>";
//     it cannot be silently dropped, because Build walks the configured source
//     list and synthesises an unreached entry for any source with no outcome.
//  3. The representativeness sentence is appended by the rendered output itself
//     and by Answer.Caveat, both of which return a package constant. It is not
//     a field, a parameter, or a Config option, so no caller can suppress it.
//
// The output deliberately carries no sentiment score, star rating, or any other
// derived metric that dresses a biased sample up as an objective measurement.
// The only aggregate present is a plain count of records per source.
package synthesis

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/evidence"
)

// RepresentativenessCaveat is the sentence that must appear in every
// synthesized answer. People who post are not people: GitHub issues are written
// by those who hit a problem, and LinkedIn contains no criticism. The honest
// answer to "what do people say about X" is always "the people who wrote
// something say this".
const RepresentativenessCaveat = "What can be scraped is not representative."

// representativenessCaveatText is the full caveat block rendered with every
// answer. It opens with the mandatory sentence verbatim.
const representativenessCaveatText = RepresentativenessCaveat + " " +
	"People who post are not people; GitHub issues are written by those who hit " +
	"a problem, and LinkedIn contains no criticism. The honest answer to " +
	"\u201cwhat do people say about X\u201d is \u201cthe people who wrote " +
	"something say this\u201d."

// Errors returned when an answer cannot honestly be built. They name the rule
// that was violated so a caller can tell an unattributable claim from an
// unreachable source rather than seeing one generic failure.
var (
	// ErrUnattributedClaim reports a claim with no record hash behind it.
	ErrUnattributedClaim = errors.New("claim has no evidence record")
	// ErrUnknownRecord reports a claim or source hash that is not in the store.
	ErrUnknownRecord = errors.New("referenced evidence record is not in the store")
	// ErrNoQuestion reports an empty question.
	ErrNoQuestion = errors.New("question is required")
	// ErrInvalidHash reports a value that is not a 64-hex content hash.
	ErrInvalidHash = errors.New("not a content hash")
)

// recordHashPattern matches the lowercase hex SHA-256 form every record hash in
// the store uses. It is the same 64-hex shape the evidence schema records.
var recordHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Claim is one factual statement in a synthesized answer, paired with the
// stored evidence record hashes that back it. Both fields are unexported and
// there is no exported constructor other than NewClaim, so a Claim with no
// record hash cannot be built from outside this package.
type Claim struct {
	text   string
	hashes []string
}

// NewClaim pairs claim text with the record hashes that support it. It returns
// ErrUnattributedClaim when the text is empty or no hash is supplied, and
// ErrInvalidHash when a value is not a 64-hex content hash, so a claim can only
// be constructed with at least one well-formed record reference.
func NewClaim(text string, hashes ...string) (Claim, error) {
	clean := strings.TrimSpace(evidence.Redact(text))
	if clean == "" {
		return Claim{}, fmt.Errorf("%w: claim text is required", ErrUnattributedClaim)
	}
	normalized, err := normalizeHashes(hashes)
	if err != nil {
		return Claim{}, err
	}
	if len(normalized) == 0 {
		return Claim{}, fmt.Errorf("%w: claim %q was given no record hash", ErrUnattributedClaim, clean)
	}
	return Claim{text: clean, hashes: normalized}, nil
}

// Text returns the claim's prose, already scrubbed of any registered secret.
func (c Claim) Text() string { return c.text }

// Hashes returns a copy of the record hashes the claim is attributed to.
func (c Claim) Hashes() []string { return append([]string(nil), c.hashes...) }

// Outcome states what happened when one configured source was consulted for the
// question. A reached source carries the record hashes it produced; an
// unreached source carries the reason, so it can be named in the answer rather
// than omitted. Its fields are unexported and it can only be obtained from
// ReachedSource or UnreachedSource, so a "reached" outcome always names what it
// reached.
type Outcome struct {
	source       string
	reached      bool
	recordHashes []string
	reason       string
}

// Source returns the source name this outcome describes.
func (o Outcome) Source() string { return o.source }

// Reached reports whether the source produced evidence.
func (o Outcome) Reached() bool { return o.reached }

// RecordHashes returns a copy of the record hashes a reached source produced.
func (o Outcome) RecordHashes() []string { return append([]string(nil), o.recordHashes...) }

// Reason returns why an unreached source could not be reached.
func (o Outcome) Reason() string { return o.reason }

// MarshalJSON renders an outcome with the field names the answer contract uses.
func (o Outcome) MarshalJSON() ([]byte, error) {
	type dto struct {
		Source       string   `json:"source"`
		Reached      bool     `json:"reached"`
		RecordHashes []string `json:"record_hashes,omitempty"`
		Reason       string   `json:"reason,omitempty"`
	}
	return json.Marshal(dto{
		Source:       o.source,
		Reached:      o.reached,
		RecordHashes: o.recordHashes,
		Reason:       o.reason,
	})
}

// ReachedSource records a source that produced evidence. It requires at least
// one well-formed record hash, so a "reached" source always names what it
// reached.
func ReachedSource(source string, hashes ...string) (Outcome, error) {
	src := strings.TrimSpace(evidence.Redact(source))
	if src == "" {
		return Outcome{}, fmt.Errorf("source name is required")
	}
	normalized, err := normalizeHashes(hashes)
	if err != nil {
		return Outcome{}, err
	}
	if len(normalized) == 0 {
		return Outcome{}, fmt.Errorf("reached source %q has no record hash", src)
	}
	return Outcome{source: src, reached: true, recordHashes: normalized}, nil
}

// UnreachedSource records a source that was configured for the query but did
// not produce evidence, such as a backend failure or a session-based source
// with no cookie configured. The reason is preserved (scrubbed of any
// registered secret) and, when empty, defaults to "unreachable" so the source
// is still named.
func UnreachedSource(source, reason string) (Outcome, error) {
	src := strings.TrimSpace(evidence.Redact(source))
	if src == "" {
		return Outcome{}, fmt.Errorf("source name is required")
	}
	r := strings.TrimSpace(evidence.Redact(reason))
	if r == "" {
		r = "unreachable"
	}
	return Outcome{source: src, reached: false, reason: r}, nil
}

// Config is the complete input to Build. It deliberately has no field that can
// disable the representativeness caveat: the caveat is not configurable.
type Config struct {
	// Question is the user query being answered.
	Question string
	// Sources is every source configured for the question, in the order the
	// answer should present them. A source here with no matching outcome is
	// reported as unreached, never dropped.
	Sources []string
	// Claims are the attributed statements to answer with.
	Claims []Claim
	// Outcomes records what each configured source produced.
	Outcomes []Outcome
}

// Answer is a synthesized response. It can only be produced by Build, which
// resolves every referenced record, names every unreached source, and appends
// the representativeness caveat unconditionally.
type Answer struct {
	question string
	claims   []Claim
	coverage []Outcome
	resolved map[string]*evidence.Record
}

// Build constructs an Answer. It refuses to return one when a claim cannot be
// attributed to a stored record, because a claim with no record behind it must
// not appear in an answer. Every source named in cfg.Sources appears in the
// resulting coverage: reached sources with their record hashes, and sources
// with no outcome (or a failed outcome) as "unreached: <reason>".
func Build(store evidence.Store, cfg Config) (*Answer, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	question := strings.TrimSpace(evidence.Redact(cfg.Question))
	if question == "" {
		return nil, ErrNoQuestion
	}

	bySource := make(map[string]Outcome, len(cfg.Outcomes))
	for _, o := range cfg.Outcomes {
		if o.source == "" {
			return nil, fmt.Errorf("outcome has no source name")
		}
		if _, dup := bySource[o.source]; dup {
			return nil, fmt.Errorf("duplicate outcome for source %q", o.source)
		}
		bySource[o.source] = o
	}

	ans := &Answer{
		question: question,
		claims:   make([]Claim, 0, len(cfg.Claims)),
		resolved: make(map[string]*evidence.Record),
	}

	resolve := func(hash string) (*evidence.Record, error) {
		if rec, ok := ans.resolved[hash]; ok {
			return rec, nil
		}
		rec, err := store.Get(hash)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrUnknownRecord, hash, err)
		}
		ans.resolved[hash] = rec
		return rec, nil
	}

	for _, c := range cfg.Claims {
		if strings.TrimSpace(c.text) == "" {
			return nil, fmt.Errorf("%w: empty claim text", ErrUnattributedClaim)
		}
		if len(c.hashes) == 0 {
			return nil, fmt.Errorf("%w: claim %q has no record hash", ErrUnattributedClaim, c.text)
		}
		for _, h := range c.hashes {
			if _, err := resolve(h); err != nil {
				return nil, err
			}
		}
		ans.claims = append(ans.claims, c)
	}

	seenSources := make(map[string]bool, len(cfg.Sources))
	for _, raw := range cfg.Sources {
		src := strings.TrimSpace(evidence.Redact(raw))
		if src == "" {
			return nil, fmt.Errorf("configured source name is required")
		}
		if seenSources[src] {
			return nil, fmt.Errorf("duplicate configured source %q", src)
		}
		seenSources[src] = true

		o, ok := bySource[src]
		if !ok {
			ans.coverage = append(ans.coverage, Outcome{
				source:  src,
				reached: false,
				reason:  "no outcome recorded",
			})
			continue
		}
		if o.reached {
			for _, h := range o.recordHashes {
				if _, err := resolve(h); err != nil {
					return nil, err
				}
			}
			ans.coverage = append(ans.coverage, Outcome{
				source:       src,
				reached:      true,
				recordHashes: append([]string(nil), o.recordHashes...),
			})
			continue
		}
		reason := o.reason
		if reason == "" {
			reason = "unreachable"
		}
		ans.coverage = append(ans.coverage, Outcome{
			source:  src,
			reached: false,
			reason:  reason,
		})
	}

	// An outcome for a source that was never configured describes something
	// that is not in the answer's coverage. Refusing it keeps the coverage list
	// and the configured source list from drifting apart.
	for src := range bySource {
		if !seenSources[src] {
			return nil, fmt.Errorf("outcome for source %q that is not configured", src)
		}
	}

	return ans, nil
}

// Question returns the question the answer responds to.
func (a *Answer) Question() string { return a.question }

// Claims returns a copy of the attributed claims.
func (a *Answer) Claims() []Claim { return append([]Claim(nil), a.claims...) }

// Coverage returns a copy of the per-source coverage, reached and unreached.
func (a *Answer) Coverage() []Outcome {
	out := make([]Outcome, len(a.coverage))
	for i, c := range a.coverage {
		out[i] = Outcome{
			source:       c.source,
			reached:      c.reached,
			recordHashes: append([]string(nil), c.recordHashes...),
			reason:       c.reason,
		}
	}
	return out
}

// Caveat returns the representativeness statement. It is a pure constant: it
// takes no parameter and consults no configuration, so there is no value a
// caller can pass to remove it.
func (a *Answer) Caveat() string { return representativenessCaveatText }

// attributionRecords returns the sorted, de-duplicated record hashes every
// claim in the answer is attributed to.
func (a *Answer) attributionRecords() []string {
	seen := make(map[string]bool)
	var hashes []string
	for _, c := range a.claims {
		for _, h := range c.hashes {
			if seen[h] {
				continue
			}
			seen[h] = true
			hashes = append(hashes, h)
		}
	}
	sort.Strings(hashes)
	return hashes
}

// latestRecord returns the hash and timestamp of the most recent record the
// answer resolved, or the zero time when there are none. Ties are broken by the
// lexicographically smallest hash so the rendering is deterministic.
func (a *Answer) latestRecord() (string, time.Time) {
	var bestHash string
	var best time.Time
	for hash, rec := range a.resolved {
		if best.IsZero() || rec.Timestamp.After(best) || (rec.Timestamp.Equal(best) && hash < bestHash) {
			best = rec.Timestamp
			bestHash = hash
		}
	}
	return bestHash, best
}

// String renders the answer as prose. Every claim line carries the record
// hashes it is attributed to, every unreached source is named, and the
// representativeness caveat is always the final line.
func (a *Answer) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Question: %s\n\n", a.question)

	b.WriteString("Coverage:\n")
	for _, c := range a.coverage {
		if !c.reached {
			fmt.Fprintf(&b, "- %s: unreached: %s\n", sourceDisplayName(c.source), c.reason)
			continue
		}
		fmt.Fprintf(&b, "- %s: %d record(s)", sourceDisplayName(c.source), len(c.recordHashes))
		writeHashList(&b, c.recordHashes)
		b.WriteString("\n")
	}
	if hash, latest := a.latestRecord(); !latest.IsZero() {
		fmt.Fprintf(&b, "Most recent record: %s (record: %s)\n", latest.UTC().Format("2006-01-02"), hash)
	}

	b.WriteString("\nThe people who wrote something say this:\n")
	if len(a.claims) == 0 {
		b.WriteString("- No attributed claims could be made from the records gathered.\n")
	}
	for _, c := range a.claims {
		fmt.Fprintf(&b, "- %s", c.text)
		writeHashList(&b, c.hashes)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(representativenessCaveatText)
	b.WriteString("\n")
	return b.String()
}

// JSON renders the answer as a JSON document. The representativeness caveat is
// present in both the "answer" prose and the "representativeness" field, and
// the document carries no sentiment, score or rating anywhere.
func (a *Answer) JSON() ([]byte, error) {
	type claimJSON struct {
		Claim        string   `json:"claim"`
		RecordHashes []string `json:"record_hashes"`
	}
	type answerJSON struct {
		Question             string      `json:"question"`
		Coverage             []Outcome   `json:"coverage"`
		Claims               []claimJSON `json:"claims"`
		AttributionRecords   []string    `json:"attribution_records"`
		MostRecentRecord     string      `json:"most_recent_record,omitempty"`
		MostRecentRecordHash string      `json:"most_recent_record_hash,omitempty"`
		Answer               string      `json:"answer"`
		Representativeness   string      `json:"representativeness"`
	}

	doc := answerJSON{
		Question:           a.question,
		Coverage:           a.Coverage(),
		Claims:             make([]claimJSON, 0, len(a.claims)),
		AttributionRecords: a.attributionRecords(),
		Answer:             a.String(),
		Representativeness: representativenessCaveatText,
	}
	if doc.AttributionRecords == nil {
		doc.AttributionRecords = []string{}
	}
	for _, c := range a.claims {
		doc.Claims = append(doc.Claims, claimJSON{
			Claim:        c.text,
			RecordHashes: append([]string(nil), c.hashes...),
		})
	}
	if hash, latest := a.latestRecord(); !latest.IsZero() {
		doc.MostRecentRecord = latest.UTC().Format("2006-01-02")
		doc.MostRecentRecordHash = hash
	}
	return json.MarshalIndent(doc, "", "  ")
}

// writeHashList appends " (record: <hash>, record: <hash>)" when hashes are
// present. A claim is only ever rendered with this suffix, so its attribution
// travels with its text.
func writeHashList(b *strings.Builder, hashes []string) {
	if len(hashes) == 0 {
		return
	}
	b.WriteString(" (")
	for i, h := range hashes {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "record: %s", h)
	}
	b.WriteString(")")
}

// sourceDisplayName gives the six real sources their prose label. Sources this
// project does not ship are never invented here; an unknown name is shown as
// given rather than mapped onto a platform that has no backend.
func sourceDisplayName(source string) string {
	switch source {
	case "github":
		return "github issues"
	default:
		return source
	}
}

// normalizeHashes trims, validates and de-duplicates a list of record hashes,
// preserving first-seen order. A malformed value is an error rather than being
// skipped, so a typo cannot quietly drop an attribution.
func normalizeHashes(hashes []string) ([]string, error) {
	var out []string
	seen := make(map[string]bool, len(hashes))
	for _, raw := range hashes {
		h := strings.TrimSpace(raw)
		if h == "" {
			continue
		}
		if !recordHashPattern.MatchString(h) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidHash, h)
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out, nil
}
