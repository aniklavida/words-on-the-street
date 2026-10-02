// Package dashboard serves a read-only, local-only web view over the evidence
// store and the live backend status.
//
// The process is already running, so it can also answer an HTTP request: the
// same pattern a self-hosted tool uses to expose its own state. The dashboard
// deliberately does not fetch, verify, or write anything. Every route is a GET
// that renders state already on disk (recent fetches and verification
// observations) or computed by the same health checks the CLI's `doctor` and
// `status` commands use.
//
// It binds to loopback only. It is not remote-accessible, not multi-user, and
// not authenticated. v1 refreshes by reloading the page (an HTML meta refresh);
// live push is planned, not implemented.
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/backend"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/verify"
)

// DefaultAddr is the loopback address the dashboard binds to by default. It is
// an explicit IP, never a hostname, so it cannot resolve to a public interface.
const DefaultAddr = "127.0.0.1:8787"

// maxRows bounds how many recent entries are rendered on the overview page;
// the full list routes render all entries.
const maxRows = 100

// Source lists the evidence already recorded. Both evidence stores satisfy it.
// The dashboard only reads.
type Source interface {
	List() ([]*evidence.Record, error)
}

// Server renders the dashboard from an evidence source and a backend registry.
type Server struct {
	Records  Source
	Registry *backend.Registry
}

// Listen binds a loopback-only TCP listener. It refuses any address whose host
// is not an IP loopback address, so a misconfiguration cannot expose the
// dashboard beyond the machine.
func Listen(addr string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: invalid address %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("dashboard: refusing to bind non-loopback address %q (localhost only)", addr)
	}
	return net.Listen("tcp", addr)
}

// fetchRow is one recorded fetch, reduced to the fields the page shows. Backend
// arguments are the sanitized ones already stored in the record; raw arguments
// and payloads are never carried into the view.
type fetchRow struct {
	Hash        string    `json:"hash,omitempty"`
	RecordHash  string    `json:"record_hash,omitempty"`
	ResolvedURL string    `json:"resolved_url"`
	Backend     string    `json:"backend"`
	Fallback    bool      `json:"fallback"`
	Status      string    `json:"status"`
	Args        []string  `json:"args,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// verifyRow is one verification observation.
type verifyRow struct {
	Hash            string    `json:"hash,omitempty"`
	State           string    `json:"state"`
	ResolvedURL     string    `json:"resolved_url"`
	ObservationHash string    `json:"observation_record_hash"`
	RecordHash      string    `json:"record_hash,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
}

// activityItem combines a fetch or verification observation for chronological display.
type activityItem struct {
	Type        string    `json:"type"`
	ResolvedURL string    `json:"resolved_url"`
	Detail      string    `json:"detail"`
	Status      string    `json:"status"`
	Hash        string    `json:"hash"`
	Timestamp   time.Time `json:"timestamp"`
}

// pageData is everything a single render needs. It is also the JSON body of
// /api/state so a script can read the same numbers the page shows.
type pageData struct {
	GeneratedAt    time.Time              `json:"generated_at"`
	LoopbackAddr   string                 `json:"loopback_addr"`
	Statuses       []backend.SourceStatus `json:"statuses"`
	Fetches        []fetchRow             `json:"fetches"`
	FetchesTotal   int                    `json:"fetches_total"`
	Verifications  []verifyRow            `json:"verifications"`
	VerifyTotal    int                    `json:"verifications_total"`
	HealthyCount   int                    `json:"healthy_count"`
	DegradedCount  int                    `json:"degraded_count"`
	DownCount      int                    `json:"down_count"`
	TrustVerified  bool                   `json:"trust_verified"`
	TrustMessage   string                 `json:"trust_message"`
	RecentActivity []activityItem         `json:"recent_activity,omitempty"`
	ReadOnly       bool                   `json:"read_only"`
	ActiveNav      string                 `json:"-"`
}

// diffLine is one formatted line in a diff view.
type diffLine struct {
	Type    string // "header", "added", "removed", "context"
	Content string
}

// fetchDetailData carries full evidence fields for a single fetch record.
type fetchDetailData struct {
	GeneratedAt  time.Time
	LoopbackAddr string
	ActiveNav    string
	Hash         string
	RecordHash   string
	ResolvedURL  string
	Backend      string
	IsFallback   bool
	Status       string
	Args         []string
	Timestamp    time.Time
}

// verifyDetailData carries full evidence fields and optional diff for a verification observation.
type verifyDetailData struct {
	GeneratedAt     time.Time
	LoopbackAddr    string
	ActiveNav       string
	Hash            string
	ObservationHash string
	RecordHash      string
	State           string
	ResolvedURL     string
	Backend         string
	IsFallback      bool
	Status          string
	Args            []string
	Timestamp       time.Time
	Diff            string
	DiffLines       []diffLine
}

// sourcesPageData carries live backend status for the sources page.
type sourcesPageData struct {
	GeneratedAt   time.Time
	LoopbackAddr  string
	ActiveNav     string
	Statuses      []backend.SourceStatus
	HealthyCount  int
	DegradedCount int
	DownCount     int
}

// checkIntegrity queries the store's integrity check if supported, returning an honest assessment.
func (s *Server) checkIntegrity(totalRecords int) (bool, string) {
	if s.Records == nil {
		return false, "No evidence store configured"
	}
	if verifier, ok := s.Records.(interface{ VerifyIntegrity() error }); ok {
		if err := verifier.VerifyIntegrity(); err != nil {
			return false, fmt.Sprintf("Ledger integrity failure: %v", err)
		}
		return true, fmt.Sprintf("Ledger verified · %d records, unbroken chain", totalRecords)
	}
	return true, fmt.Sprintf("Ledger active · %d records recorded", totalRecords)
}

// getRecord looks up a record by its hash (content hash or record hash), using store.Get(hash)
// when supported and falling back to scanning List().
func (s *Server) getRecord(hash string) (*evidence.Record, error) {
	if s.Records == nil {
		return nil, fmt.Errorf("dashboard: evidence source is required")
	}
	if getter, ok := s.Records.(interface {
		Get(string) (*evidence.Record, error)
	}); ok {
		if rec, err := getter.Get(hash); err == nil && rec != nil {
			return rec, nil
		}
	}
	records, err := s.Records.List()
	if err != nil {
		return nil, err
	}
	for _, rec := range records {
		if rec != nil && (rec.Hash == hash || rec.RecordHash == hash) {
			return rec, nil
		}
	}
	return nil, evidence.ErrNotFound
}

// getDiffForObservation computes or extracts the diff between the original fetch and the observation.
func (s *Server) getDiffForObservation(obsRec *evidence.Record) string {
	if s.Records == nil || obsRec == nil {
		return ""
	}
	records, err := s.Records.List()
	if err != nil {
		return ""
	}
	var origRec *evidence.Record
	foundObs := false
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r == nil {
			continue
		}
		if r.RecordHash == obsRec.RecordHash || (obsRec.Hash != "" && r.Hash == obsRec.Hash) {
			foundObs = true
			continue
		}
		if foundObs && r.ResolvedURL == obsRec.ResolvedURL {
			if _, isVerify := verificationState(r); !isVerify {
				origRec = r
				break
			}
		}
	}
	if origRec == nil {
		for _, r := range records {
			if r == nil {
				continue
			}
			if r.RecordHash == obsRec.RecordHash || (obsRec.Hash != "" && r.Hash == obsRec.Hash) {
				break
			}
			if r.ResolvedURL == obsRec.ResolvedURL {
				if _, isVerify := verificationState(r); !isVerify {
					origRec = r
				}
			}
		}
	}
	if origRec != nil && len(origRec.RawBytes()) > 0 && len(obsRec.RawBytes()) > 0 {
		return verify.LineDiff(origRec.RawBytes(), obsRec.RawBytes())
	}
	payload := string(obsRec.RawBytes())
	if strings.HasPrefix(payload, "---") || strings.Contains(payload, "\n+") || strings.Contains(payload, "\n-") {
		return payload
	}
	return ""
}

// parseDiffLines formats a unified diff into classified lines.
func parseDiffLines(diff string) []diffLine {
	if diff == "" {
		return nil
	}
	rawLines := strings.Split(diff, "\n")
	var lines []diffLine
	for _, l := range rawLines {
		if l == "" {
			continue
		}
		switch {
		case strings.HasPrefix(l, "---") || strings.HasPrefix(l, "+++"):
			lines = append(lines, diffLine{Type: "header", Content: l})
		case strings.HasPrefix(l, "-"):
			lines = append(lines, diffLine{Type: "removed", Content: l})
		case strings.HasPrefix(l, "+"):
			lines = append(lines, diffLine{Type: "added", Content: l})
		default:
			lines = append(lines, diffLine{Type: "context", Content: l})
		}
	}
	return lines
}

// buildPage reads the store and the live status and assembles the overview view.
func (s *Server) buildPage(ctx context.Context) (*pageData, error) {
	if s.Records == nil {
		return nil, fmt.Errorf("dashboard: evidence source is required")
	}

	records, err := s.Records.List()
	if err != nil {
		return nil, fmt.Errorf("dashboard: failed to list evidence: %w", err)
	}

	page := &pageData{
		GeneratedAt:  time.Now().UTC(),
		LoopbackAddr: DefaultAddr,
		ReadOnly:     true,
		ActiveNav:    "overview",
	}
	if s.Registry != nil {
		page.Statuses = backend.CheckAllStatus(ctx, s.Registry)
	}

	for _, st := range page.Statuses {
		switch st.State {
		case backend.SourceHealthy:
			page.HealthyCount++
		case backend.SourceDegraded:
			page.DegradedCount++
		case backend.SourceDown:
			page.DownCount++
		}
	}

	verified, msg := s.checkIntegrity(len(records))
	page.TrustVerified = verified
	page.TrustMessage = msg

	// Recent activity (up to 10 latest entries)
	for i := len(records) - 1; i >= 0 && len(page.RecentActivity) < 10; i-- {
		rec := records[i]
		if rec == nil {
			continue
		}
		if state, ok := verificationState(rec); ok {
			page.RecentActivity = append(page.RecentActivity, activityItem{
				Type:        "verification",
				ResolvedURL: rec.ResolvedURL,
				Detail:      string(state),
				Status:      string(state),
				Hash:        rec.Hash,
				Timestamp:   rec.Timestamp,
			})
		} else {
			path := "primary"
			if rec.IsFallback {
				path = "fallback"
			}
			page.RecentActivity = append(page.RecentActivity, activityItem{
				Type:        "fetch",
				ResolvedURL: rec.ResolvedURL,
				Detail:      rec.BackendName,
				Status:      path,
				Hash:        rec.Hash,
				Timestamp:   rec.Timestamp,
			})
		}
	}

	// Walk backwards for recent fetches and verifications
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec == nil {
			continue
		}
		if state, ok := verificationState(rec); ok {
			page.VerifyTotal++
			if len(page.Verifications) < maxRows {
				page.Verifications = append(page.Verifications, verifyRow{
					Hash:            rec.Hash,
					State:           string(state),
					ResolvedURL:     rec.ResolvedURL,
					ObservationHash: rec.RecordHash,
					RecordHash:      rec.RecordHash,
					Timestamp:       rec.Timestamp,
				})
			}
			continue
		}
		page.FetchesTotal++
		if len(page.Fetches) < maxRows {
			page.Fetches = append(page.Fetches, fetchRow{
				Hash:        rec.Hash,
				RecordHash:  rec.RecordHash,
				ResolvedURL: rec.ResolvedURL,
				Backend:     rec.BackendName,
				Fallback:    rec.IsFallback,
				Status:      rec.BackendStatus,
				Args:        rec.BackendArgs,
				Timestamp:   rec.Timestamp,
			})
		}
	}

	return page, nil
}

// buildAllFetches enumerates all recorded fetches without truncation.
func (s *Server) buildAllFetches(ctx context.Context) (*pageData, error) {
	if s.Records == nil {
		return nil, fmt.Errorf("dashboard: evidence source is required")
	}
	records, err := s.Records.List()
	if err != nil {
		return nil, fmt.Errorf("dashboard: failed to list evidence: %w", err)
	}

	page := &pageData{
		GeneratedAt:  time.Now().UTC(),
		LoopbackAddr: DefaultAddr,
		ReadOnly:     true,
		ActiveNav:    "fetches",
	}

	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec == nil {
			continue
		}
		if _, ok := verificationState(rec); ok {
			continue
		}
		page.FetchesTotal++
		page.Fetches = append(page.Fetches, fetchRow{
			Hash:        rec.Hash,
			RecordHash:  rec.RecordHash,
			ResolvedURL: rec.ResolvedURL,
			Backend:     rec.BackendName,
			Fallback:    rec.IsFallback,
			Status:      rec.BackendStatus,
			Args:        rec.BackendArgs,
			Timestamp:   rec.Timestamp,
		})
	}
	return page, nil
}

// buildAllVerifications enumerates all verification observations without truncation.
func (s *Server) buildAllVerifications(ctx context.Context) (*pageData, error) {
	if s.Records == nil {
		return nil, fmt.Errorf("dashboard: evidence source is required")
	}
	records, err := s.Records.List()
	if err != nil {
		return nil, fmt.Errorf("dashboard: failed to list evidence: %w", err)
	}

	page := &pageData{
		GeneratedAt:  time.Now().UTC(),
		LoopbackAddr: DefaultAddr,
		ReadOnly:     true,
		ActiveNav:    "verifications",
	}

	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec == nil {
			continue
		}
		state, ok := verificationState(rec)
		if !ok {
			continue
		}
		page.VerifyTotal++
		page.Verifications = append(page.Verifications, verifyRow{
			Hash:            rec.Hash,
			State:           string(state),
			ResolvedURL:     rec.ResolvedURL,
			ObservationHash: rec.RecordHash,
			RecordHash:      rec.RecordHash,
			Timestamp:       rec.Timestamp,
		})
	}
	return page, nil
}

// verificationState maps a recorded verification observation back to its state.
func verificationState(rec *evidence.Record) (verify.State, bool) {
	switch rec.BackendStatus {
	case verify.StatusVerifiedIdentical:
		return verify.Identical, true
	case verify.StatusVerifiedChanged:
		return verify.Changed, true
	case string(verify.Gone):
		return verify.Gone, true
	default:
		return "", false
	}
}

// Handler returns the read-only routes. All routes pass through readOnly.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleOverview)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/fetches", s.handleFetches)
	mux.HandleFunc("/fetches/", s.handleFetchDetail)
	mux.HandleFunc("/verifications", s.handleVerifications)
	mux.HandleFunc("/verifications/", s.handleVerificationDetail)
	mux.HandleFunc("/sources", s.handleSources)
	return readOnly(mux)
}

// readOnly refuses every method that could carry an intent to change something.
func readOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "dashboard is read-only: no route fetches, verifies, or writes", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, err := s.buildPage(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = overviewTemplate.Execute(w, page)
}

func (s *Server) handleFetches(w http.ResponseWriter, r *http.Request) {
	page, err := s.buildAllFetches(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = fetchesTemplate.Execute(w, page)
}

func (s *Server) handleFetchDetail(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/fetches/")
	hash = strings.TrimSpace(hash)
	if hash == "" {
		http.Redirect(w, r, "/fetches", http.StatusSeeOther)
		return
	}
	rec, err := s.getRecord(hash)
	if err != nil {
		http.Error(w, "Fetch record not found", http.StatusNotFound)
		return
	}
	data := fetchDetailData{
		GeneratedAt:  time.Now().UTC(),
		LoopbackAddr: DefaultAddr,
		ActiveNav:    "fetches",
		Hash:         rec.Hash,
		RecordHash:   rec.RecordHash,
		ResolvedURL:  rec.ResolvedURL,
		Backend:      rec.BackendName,
		IsFallback:   rec.IsFallback,
		Status:       rec.BackendStatus,
		Args:         rec.BackendArgs,
		Timestamp:    rec.Timestamp,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = fetchDetailTemplate.Execute(w, data)
}

func (s *Server) handleVerifications(w http.ResponseWriter, r *http.Request) {
	page, err := s.buildAllVerifications(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = verificationsTemplate.Execute(w, page)
}

func (s *Server) handleVerificationDetail(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/verifications/")
	hash = strings.TrimSpace(hash)
	if hash == "" {
		http.Redirect(w, r, "/verifications", http.StatusSeeOther)
		return
	}
	rec, err := s.getRecord(hash)
	if err != nil {
		http.Error(w, "Verification record not found", http.StatusNotFound)
		return
	}
	state, _ := verificationState(rec)
	diff := ""
	if state == verify.Changed {
		diff = s.getDiffForObservation(rec)
	}

	data := verifyDetailData{
		GeneratedAt:     time.Now().UTC(),
		LoopbackAddr:    DefaultAddr,
		ActiveNav:       "verifications",
		Hash:            rec.Hash,
		ObservationHash: rec.RecordHash,
		RecordHash:      rec.RecordHash,
		State:           string(state),
		ResolvedURL:     rec.ResolvedURL,
		Backend:         rec.BackendName,
		IsFallback:      rec.IsFallback,
		Status:          rec.BackendStatus,
		Args:            rec.BackendArgs,
		Timestamp:       rec.Timestamp,
		Diff:            diff,
		DiffLines:       parseDiffLines(diff),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = verificationDetailTemplate.Execute(w, data)
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	var statuses []backend.SourceStatus
	if s.Registry != nil {
		statuses = backend.CheckAllStatus(r.Context(), s.Registry)
	}
	healthy, degraded, down := 0, 0, 0
	for _, st := range statuses {
		switch st.State {
		case backend.SourceHealthy:
			healthy++
		case backend.SourceDegraded:
			degraded++
		case backend.SourceDown:
			down++
		}
	}
	data := sourcesPageData{
		GeneratedAt:   time.Now().UTC(),
		LoopbackAddr:  DefaultAddr,
		ActiveNav:     "sources",
		Statuses:      statuses,
		HealthyCount:  healthy,
		DegradedCount: degraded,
		DownCount:     down,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = sourcesTemplate.Execute(w, data)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	page, err := s.buildPage(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(page)
}

// sourceInitial extracts the first letter for a generic identifier.
func sourceInitial(s string) string {
	if len(s) == 0 {
		return "?"
	}
	return strings.ToUpper(s[:1])
}

// sourceAvatarBg maps each source to a deterministic palette color. No brand logos are used.
func sourceAvatarBg(s string) string {
	switch strings.ToLower(s) {
	case "hacker-news":
		return "#ea580c"
	case "lobsters":
		return "#dc2626"
	case "github":
		return "#334155"
	case "web":
		return "#0284c7"
	case "twitter":
		return "#0d9488"
	case "linkedin":
		return "#4f46e5"
	default:
		return "#0f766e"
	}
}

var templateFuncs = template.FuncMap{
	"initial":  sourceInitial,
	"avatarBg": sourceAvatarBg,
	"upper":    strings.ToUpper,
	"lower":    strings.ToLower,
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return t.UTC().Format("2006-01-02 15:04:05")
	},
	"shortHash": func(h string) string {
		if len(h) <= 12 {
			return h
		}
		return h[:12] + "…"
	},
}

const sharedCSS = `
/* ==========================================================================
   Project Design System Tokens
   ========================================================================== */

/* ---------- BRAND BLOCK ---------- */
:root {
  --brand-h: 60;          /* ochre hue */
  --brand-c: 0.10;        /* restrained chroma */
  --brand-l: 0.58;        /* accent lightness light mode */
  --brand-l-dark: 0.68;   /* accent lightness dark mode */
  --warmth: 0.012;        /* paper / coal warmth */
  --neutral-h: var(--brand-h);
  --radius-scale: 0.9;
  --density: 1;
  --font-display: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  --font-ui: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  --font-mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

/* ---------- DERIVED COLORS ---------- */
:root, [data-theme="light"] {
  color-scheme: light;
  --bg:      oklch(0.985 var(--warmth) var(--neutral-h));
  --rail:    oklch(0.955 calc(var(--warmth) * 1.2) var(--neutral-h));
  --surf:    oklch(0.995 calc(var(--warmth) * 0.5) var(--neutral-h));
  --subtle:  oklch(0.97  var(--warmth) var(--neutral-h));
  --bd:      oklch(0.90  var(--warmth) var(--neutral-h));
  --bd2:     oklch(0.85  var(--warmth) var(--neutral-h));
  --line:    oklch(0.94  var(--warmth) var(--neutral-h));
  --act:     oklch(0.92  calc(var(--warmth) * 1.5) var(--neutral-h));
  --hov:     oklch(0.94  var(--warmth) var(--neutral-h));
  --chip:    oklch(0.88  var(--warmth) var(--neutral-h));
  --ink:     oklch(0.22  calc(var(--warmth) * 0.8) var(--neutral-h));
  --ink2:    oklch(0.32  calc(var(--warmth) * 0.8) var(--neutral-h));
  --mut:     oklch(0.48  var(--warmth) var(--neutral-h));
  --faint:   oklch(0.62  var(--warmth) var(--neutral-h));
  --acc:     oklch(var(--brand-l) var(--brand-c) var(--brand-h));
  --acc2:    oklch(calc(var(--brand-l) - 0.1) var(--brand-c) var(--brand-h));
  --accbg:   oklch(0.95 calc(var(--brand-c) * 0.3) var(--brand-h));
  --on-acc:  #ffffff;
  --ok:      oklch(0.55 0.12 150);  --okbg:   oklch(0.95 0.04 150);
  --warn:    oklch(0.62 0.13 75);   --warnbg: oklch(0.96 0.05 75);
  --bad:     oklch(0.55 0.16 25);   --badbg:  oklch(0.95 0.04 25);
  --info:    oklch(0.55 0.10 240);  --infobg: oklch(0.95 0.03 240);
  --backdrop: rgba(0,0,0,0.30);
  --shadow-menu:  0 12px 32px rgba(0,0,0,0.12);
  --shadow-modal: 0 24px 60px rgba(0,0,0,0.18);
  --shadow-card:  0 1px 2px rgba(0,0,0,0.04);
}

[data-theme="dark"] {
  color-scheme: dark;
  --bg:      oklch(0.17 var(--warmth) var(--neutral-h));
  --rail:    oklch(0.20 var(--warmth) var(--neutral-h));
  --surf:    oklch(0.22 var(--warmth) var(--neutral-h));
  --subtle:  oklch(0.26 var(--warmth) var(--neutral-h));
  --bd:      oklch(0.31 var(--warmth) var(--neutral-h));
  --bd2:     oklch(0.37 var(--warmth) var(--neutral-h));
  --line:    oklch(0.28 var(--warmth) var(--neutral-h));
  --act:     oklch(0.32 calc(var(--warmth) * 1.5) var(--neutral-h));
  --hov:     oklch(0.27 var(--warmth) var(--neutral-h));
  --chip:    oklch(0.32 var(--warmth) var(--neutral-h));
  --ink:     oklch(0.93 calc(var(--warmth) * 0.6) var(--neutral-h));
  --ink2:    oklch(0.85 calc(var(--warmth) * 0.6) var(--neutral-h));
  --mut:     oklch(0.72 var(--warmth) var(--neutral-h));
  --faint:   oklch(0.58 var(--warmth) var(--neutral-h));
  --acc:     oklch(var(--brand-l-dark) var(--brand-c) var(--brand-h));
  --acc2:    oklch(calc(var(--brand-l-dark) + 0.08) var(--brand-c) var(--brand-h));
  --accbg:   oklch(0.30 calc(var(--brand-c) * 0.4) var(--brand-h));
  --on-acc:  #ffffff;
  --ok:      oklch(0.75 0.12 150);  --okbg:   oklch(0.28 0.04 150);
  --warn:    oklch(0.80 0.12 80);   --warnbg: oklch(0.30 0.05 80);
  --bad:     oklch(0.72 0.14 25);   --badbg:  oklch(0.30 0.05 25);
  --info:    oklch(0.75 0.09 240);  --infobg: oklch(0.28 0.04 240);
  --backdrop: rgba(0,0,0,0.5);
  --shadow-menu:  0 14px 36px rgba(0,0,0,0.35);
  --shadow-modal: 0 24px 60px rgba(0,0,0,0.5);
  --shadow-card:  none;
}

@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    color-scheme: dark;
    --bg:      oklch(0.17 var(--warmth) var(--neutral-h));
    --rail:    oklch(0.20 var(--warmth) var(--neutral-h));
    --surf:    oklch(0.22 var(--warmth) var(--neutral-h));
    --subtle:  oklch(0.26 var(--warmth) var(--neutral-h));
    --bd:      oklch(0.31 var(--warmth) var(--neutral-h));
    --bd2:     oklch(0.37 var(--warmth) var(--neutral-h));
    --line:    oklch(0.28 var(--warmth) var(--neutral-h));
    --act:     oklch(0.32 calc(var(--warmth) * 1.5) var(--neutral-h));
    --hov:     oklch(0.27 var(--warmth) var(--neutral-h));
    --chip:    oklch(0.32 var(--warmth) var(--neutral-h));
    --ink:     oklch(0.93 calc(var(--warmth) * 0.6) var(--neutral-h));
    --ink2:    oklch(0.85 calc(var(--warmth) * 0.6) var(--neutral-h));
    --mut:     oklch(0.72 var(--warmth) var(--neutral-h));
    --faint:   oklch(0.58 var(--warmth) var(--neutral-h));
    --acc:     oklch(var(--brand-l-dark) var(--brand-c) var(--brand-h));
    --acc2:    oklch(calc(var(--brand-l-dark) + 0.08) var(--brand-c) var(--brand-h));
    --accbg:   oklch(0.30 calc(var(--brand-c) * 0.4) var(--brand-h));
    --on-acc:  #ffffff;
    --ok:      oklch(0.75 0.12 150);  --okbg:   oklch(0.28 0.04 150);
    --warn:    oklch(0.80 0.12 80);   --warnbg: oklch(0.30 0.05 80);
    --bad:     oklch(0.72 0.14 25);   --badbg:  oklch(0.30 0.05 25);
    --info:    oklch(0.75 0.09 240);  --infobg: oklch(0.28 0.04 240);
    --backdrop: rgba(0,0,0,0.5);
    --shadow-menu:  0 14px 36px rgba(0,0,0,0.35);
    --shadow-modal: 0 24px 60px rgba(0,0,0,0.5);
    --shadow-card:  none;
  }
}

/* ---------- SCALE & METRICS ---------- */
:root {
  --fs-xs: 11px; --fs-sm: 12.5px; --fs-base: 14px; --fs-md: 16px; --fs-lg: 20px; --fs-xl: 24px; --fs-2xl: 28px; --fs-3xl: 40px; --fs-4xl: 56px;
  --lh-tight: 1.15; --lh: 1.45; --lh-loose: 1.6;
  --sp-1: calc(4px * var(--density)); --sp-2: calc(8px * var(--density)); --sp-3: calc(12px * var(--density)); --sp-4: calc(16px * var(--density)); --sp-5: calc(20px * var(--density)); --sp-6: calc(24px * var(--density)); --sp-8: calc(32px * var(--density));
  --r-xs: calc(4px * var(--radius-scale)); --r-sm: calc(7px * var(--radius-scale)); --r-md: calc(10px * var(--radius-scale)); --r-lg: calc(14px * var(--radius-scale)); --r-xl: calc(18px * var(--radius-scale)); --r-pill: 999px;
  --h-xs: 28px; --h-sm: 32px; --h-md: 36px; --h-lg: 40px; --h-xl: 48px;
  --ease: cubic-bezier(.2,.7,.2,1); --t-fast: 120ms; --t: 180ms; --t-slow: 280ms;
}

/* ---------- RESET & BASE ---------- */
*, *::before, *::after { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--ink); font-family: var(--font-ui); font-size: var(--fs-base); line-height: var(--lh); -webkit-font-smoothing: antialiased; }
a { color: var(--acc); text-decoration: none; }
a:hover { color: var(--acc2); text-decoration: underline; }
:focus-visible { outline: 2px solid var(--acc); outline-offset: 2px; }

/* ---------- STRUCTURE ---------- */
.layout { display: flex; flex-direction: column; min-height: 100vh; }
.sidebar { width: 100%; background: var(--rail); border-bottom: 1px solid var(--bd); display: flex; flex-direction: column; flex-shrink: 0; }
.sidebar-header { padding: 1.25rem 1.25rem 1rem; border-bottom: 1px solid var(--bd); }
.sidebar-title { font-family: var(--font-display); font-size: 1.15rem; font-weight: 600; color: var(--ink); margin: 0; letter-spacing: -0.01em; }
.sidebar-tagline { font-size: var(--fs-xs); color: var(--mut); margin-top: 0.35rem; line-height: 1.35; }
.sidebar-nav { padding: 0.75rem 1rem; display: flex; flex-wrap: wrap; gap: 0.5rem; }
.nav-link { display: inline-flex; align-items: center; justify-content: center; gap: 0.5rem; padding: 0.5rem 0.85rem; border-radius: var(--r-sm); color: var(--mut); text-decoration: none; font-size: var(--fs-sm); font-weight: 500; min-height: 40px; min-width: 44px; transition: background var(--t-fast) var(--ease), color var(--t-fast) var(--ease); }
.nav-link:hover { background: var(--hov); color: var(--ink); text-decoration: none; }
.nav-link.active { background: var(--act); color: var(--ink); font-weight: 600; }
.sidebar-footer { padding: 1rem 1.25rem; border-top: 1px solid var(--bd); font-size: var(--fs-xs); background: var(--rail); display: flex; flex-direction: column; gap: 0.5rem; }
.status-indicator { display: flex; align-items: center; gap: 0.4rem; font-weight: 600; color: var(--ink); }
.status-dot { width: 8px; height: 8px; border-radius: 50%; background-color: var(--ok); flex-shrink: 0; }
.sidebar-addr { font-family: var(--font-mono); color: var(--mut); font-size: var(--fs-xs); }
.sidebar-note { color: var(--faint); font-style: italic; }
.theme-toggle-btn { display: inline-flex; align-items: center; justify-content: space-between; padding: 0.4rem 0.75rem; border-radius: var(--r-sm); border: 1px solid var(--bd); background: var(--surf); color: var(--ink); font-family: var(--font-ui); font-size: var(--fs-xs); cursor: pointer; min-height: 40px; min-width: 44px; transition: background var(--t-fast) var(--ease), border-color var(--t-fast) var(--ease); }
.theme-toggle-btn:hover { background: var(--hov); border-color: var(--bd2); }

@media (min-width: 900px) {
  .layout { flex-direction: row; }
  .sidebar { width: 240px; min-height: 100vh; height: 100vh; position: sticky; top: 0; border-right: 1px solid var(--bd); border-bottom: none; }
  .sidebar-nav { flex-direction: column; padding: 1rem 0.75rem; flex: 1; flex-wrap: nowrap; gap: 0.25rem; }
  .nav-link { display: flex; justify-content: flex-start; }
}

/* ---------- MAIN CONTENT ---------- */
.main-content { flex: 1; padding: 1.5rem 1rem; max-width: 1200px; min-width: 0; width: 100%; }
@media (min-width: 900px) {
  .main-content { padding: 2rem 2.5rem; }
}

.page-label { font-size: var(--fs-xs); font-weight: 600; text-transform: uppercase; letter-spacing: 0.06em; color: var(--faint); margin-bottom: 0.25rem; }
.page-header { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 1.5rem; gap: 1rem; flex-wrap: wrap; }
.page-header-actions { display: flex; align-items: center; gap: 0.75rem; flex-wrap: wrap; }
.page-title { font-family: var(--font-display); font-size: var(--fs-2xl); font-weight: 600; color: var(--ink); margin: 0 0 0.25rem 0; letter-spacing: -0.02em; line-height: var(--lh-tight); }
.page-subtitle { font-size: var(--fs-sm); color: var(--mut); margin: 0; max-width: 680px; }
.quote-pill { background: var(--surf); border: 1px solid var(--bd); border-radius: var(--r-md); padding: 0.5rem 0.85rem; font-style: italic; font-size: var(--fs-sm); color: var(--mut); box-shadow: var(--shadow-card); white-space: nowrap; }

/* ---------- BUTTONS ---------- */
.btn { display: inline-flex; align-items: center; justify-content: center; min-height: 40px; min-width: 44px; padding: 0.4rem 1rem; border-radius: var(--r-md); font-family: var(--font-ui); font-size: var(--fs-sm); font-weight: 500; border: 1px solid transparent; cursor: pointer; text-decoration: none; transition: background var(--t-fast) var(--ease), color var(--t-fast) var(--ease), border-color var(--t-fast) var(--ease); white-space: nowrap; }
.btn-primary { background: var(--acc); color: var(--on-acc); border-color: var(--acc); }
.btn-primary:hover { background: var(--acc2); border-color: var(--acc2); text-decoration: none; }
.btn-secondary { background: var(--surf); color: var(--ink); border-color: var(--bd); }
.btn-secondary:hover { background: var(--hov); border-color: var(--bd2); text-decoration: none; }

/* ---------- TRUST STRIP ---------- */
.trust-strip { display: flex; align-items: center; justify-content: space-between; padding: 0.75rem 1.25rem; border-radius: var(--r-md); margin-bottom: 1.5rem; font-size: var(--fs-sm); gap: 0.75rem; flex-wrap: wrap; min-width: 0; max-width: 100%; word-break: break-word; overflow-wrap: anywhere; }
.trust-strip.trust-verified { background: var(--okbg); border: 1px solid var(--ok); color: var(--ok); }
.trust-strip.trust-failed { background: var(--badbg); border: 1px solid var(--bad); color: var(--bad); }
.trust-content { display: flex; align-items: center; gap: 0.5rem; font-weight: 500; min-width: 0; flex: 1 1 200px; word-break: break-word; overflow-wrap: anywhere; }
.trust-content span { min-width: 0; word-break: break-word; overflow-wrap: anywhere; }
.trust-badge { font-size: var(--fs-xs); text-transform: uppercase; letter-spacing: 0.05em; font-weight: 600; opacity: 0.9; white-space: nowrap; flex-shrink: 0; }

/* ---------- STATS GRID ---------- */
.stats-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(220px, 100%), 1fr)); gap: 1rem; margin-bottom: 1.75rem; }
.stat-card { background: var(--surf); border: 1px solid var(--bd); border-radius: var(--r-lg); padding: 1.25rem; box-shadow: var(--shadow-card); }
.stat-label { font-size: var(--fs-xs); font-weight: 600; text-transform: uppercase; letter-spacing: 0.05em; color: var(--faint); margin-bottom: 0.4rem; }
.stat-value { font-size: var(--fs-2xl); font-weight: 600; color: var(--ink); white-space: nowrap; font-variant-numeric: tabular-nums; }
.stat-sub { font-size: var(--fs-xs); color: var(--mut); margin-top: 0.25rem; }

/* ---------- CARDS & TABLES ---------- */
.card { background: var(--surf); border: 1px solid var(--bd); border-radius: var(--r-lg); margin-bottom: 1.5rem; overflow: hidden; box-shadow: var(--shadow-card); }
.card-header { display: flex; justify-content: space-between; align-items: center; padding: 1rem 1.25rem; border-bottom: 1px solid var(--bd); background: var(--subtle); gap: 0.5rem; flex-wrap: wrap; }
.card-title { font-size: var(--fs-md); font-weight: 600; color: var(--ink); margin: 0; }
.card-link { font-size: var(--fs-sm); font-weight: 500; color: var(--acc); text-decoration: none; min-height: 40px; display: inline-flex; align-items: center; }
.card-link:hover { color: var(--acc2); text-decoration: underline; }

.table-wrap { width: 100%; overflow-x: auto; -webkit-overflow-scrolling: touch; }
table { width: 100%; border-collapse: collapse; text-align: left; font-size: var(--fs-sm); }
th { background: var(--subtle); color: var(--mut); font-weight: 600; font-size: var(--fs-xs); text-transform: uppercase; letter-spacing: 0.05em; padding: 0.65rem 1rem; border-bottom: 1px solid var(--bd); white-space: nowrap; }
td { padding: 0.75rem 1rem; border-bottom: 1px solid var(--line); vertical-align: top; color: var(--ink); white-space: nowrap; }
tr:last-child td { border-bottom: none; }
tr:hover td { background-color: var(--hov); }
a.table-link { color: var(--acc); text-decoration: none; font-weight: 500; white-space: nowrap; }
a.table-link:hover { color: var(--acc2); text-decoration: underline; }
.mono { font-family: var(--font-mono); font-size: var(--fs-sm); }
td.mono { white-space: nowrap; }
.detail-value.mono { word-break: break-all; }

/* ---------- BADGES ---------- */
.badge { display: inline-flex; align-items: center; gap: 0.3rem; padding: 0.2rem 0.55rem; border-radius: var(--r-xs); font-size: var(--fs-xs); font-weight: 600; white-space: nowrap; line-height: 1.2; }
.badge-healthy, .badge-identical { background: var(--okbg); color: var(--ok); }
.badge-degraded, .badge-changed { background: var(--warnbg); color: var(--warn); }
.badge-down, .badge-gone { background: var(--badbg); color: var(--bad); }
.badge-primary { background: var(--okbg); color: var(--ok); }
.badge-fallback { background: var(--warnbg); color: var(--warn); }
.badge-subtle { background: var(--subtle); color: var(--mut); }

/* ---------- SOURCES ---------- */
.sources-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(260px, 100%), 1fr)); gap: 1rem; margin-bottom: 1.5rem; }
.source-card { background: var(--surf); border: 1px solid var(--bd); border-radius: var(--r-lg); padding: 1.25rem; box-shadow: var(--shadow-card); }
.source-card-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 1rem; gap: 0.5rem; }
.source-info { display: flex; align-items: center; gap: 0.6rem; }
.source-avatar { width: 32px; height: 32px; border-radius: var(--r-sm); color: var(--ink); background: var(--chip); border: 1px solid var(--bd); display: flex; align-items: center; justify-content: center; font-weight: 600; font-size: var(--fs-base); text-transform: uppercase; flex-shrink: 0; }
.source-name { font-weight: 600; font-size: var(--fs-md); color: var(--ink); }
.source-meta-row { display: flex; justify-content: space-between; font-size: var(--fs-sm); margin-bottom: 0.4rem; gap: 0.5rem; }
.source-meta-label { color: var(--mut); }
.source-meta-value { color: var(--ink); font-weight: 500; }
.source-reports { margin-top: 0.75rem; padding-top: 0.75rem; border-top: 1px solid var(--line); font-size: var(--fs-xs); }
.report-item { margin-bottom: 0.35rem; display: flex; align-items: center; gap: 0.4rem; flex-wrap: wrap; }
.report-name { font-weight: 600; color: var(--ink); }

/* ---------- STATES & DETAILS ---------- */
.empty-state { padding: 3rem 1.5rem; text-align: center; color: var(--mut); font-size: var(--fs-sm); }
.back-link { display: inline-flex; align-items: center; gap: 0.4rem; font-size: var(--fs-sm); font-weight: 500; color: var(--acc); text-decoration: none; margin-bottom: 1.25rem; min-height: 40px; }
.back-link:hover { color: var(--acc2); text-decoration: underline; }

.detail-grid { display: grid; grid-template-columns: minmax(140px, 180px) 1fr; gap: 0.75rem 1.5rem; padding: 1.25rem; font-size: var(--fs-sm); background: var(--surf); border: 1px solid var(--bd); border-radius: var(--r-lg); margin-bottom: 1.5rem; }
@media (max-width: 639px) {
  .detail-grid { grid-template-columns: 1fr; gap: 0.35rem 0; }
  .detail-label { margin-top: 0.75rem; }
  .detail-label:first-child { margin-top: 0; }
}
.detail-label { color: var(--mut); font-weight: 500; font-size: var(--fs-xs); text-transform: uppercase; letter-spacing: 0.05em; }
.detail-value { color: var(--ink); word-break: break-all; }
.detail-footer-quotes { display: flex; justify-content: space-between; font-size: var(--fs-xs); color: var(--faint); font-style: italic; margin-top: 1.5rem; padding-top: 1rem; border-top: 1px solid var(--bd); flex-wrap: wrap; gap: 0.5rem; }

/* ---------- DIFF CONTAINER ---------- */
.diff-container { background: var(--surf); border: 1px solid var(--bd); border-radius: var(--r-lg); margin-bottom: 1.5rem; overflow: hidden; }
.diff-header-bar { padding: 0.75rem 1.25rem; background: var(--subtle); border-bottom: 1px solid var(--bd); font-weight: 600; font-size: var(--fs-sm); color: var(--ink); }
.diff-view { padding: 0.75rem 0; font-family: var(--font-mono); font-size: var(--fs-sm); line-height: 1.5; overflow-x: auto; }
.diff-line { padding: 0.15rem 1.25rem; white-space: pre-wrap; word-break: break-all; }
.diff-line.diff-removed { background-color: var(--badbg); color: var(--bad); }
.diff-line.diff-added { background-color: var(--okbg); color: var(--ok); }
.diff-line.diff-header { background-color: var(--subtle); color: var(--mut); font-weight: 600; }
.diff-line.diff-context { color: var(--ink); }
`

const layoutHeader = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Words on the Street — Local dashboard</title>
<script>
(function() {
  try {
    var stored = localStorage.getItem('wot-theme');
    if (stored === 'dark' || stored === 'light') {
      document.documentElement.setAttribute('data-theme', stored);
    }
  } catch(e) {}
})();
</script>
<style>` + sharedCSS + `</style>
</head>
<body>
<div class="layout">
  <aside class="sidebar">
    <div class="sidebar-header">
      <div class="sidebar-title">Words on the Street</div>
      <div class="sidebar-tagline">What people are saying, and proof they said it.</div>
    </div>
    <nav class="sidebar-nav" aria-label="Main navigation">
      <a href="/" class="nav-link {{if eq .ActiveNav "overview"}}active{{end}}">Overview</a>
      <a href="/fetches" class="nav-link {{if eq .ActiveNav "fetches"}}active{{end}}">Fetches</a>
      <a href="/verifications" class="nav-link {{if eq .ActiveNav "verifications"}}active{{end}}">Verifications</a>
      <a href="/sources" class="nav-link {{if eq .ActiveNav "sources"}}active{{end}}">Sources</a>
    </nav>
    <div class="sidebar-footer">
      <button type="button" class="theme-toggle-btn" onclick="toggleTheme()" aria-label="Toggle visual theme">
        <span id="theme-toggle-text">Theme: Light</span>
      </button>
      <div class="status-indicator">
        <span class="status-dot"></span>
        <span class="status-text">Local dashboard</span>
      </div>
      <div class="sidebar-addr mono">{{.LoopbackAddr}}</div>
      <div class="sidebar-note">Evidence on your machine. Always.</div>
    </div>
  </aside>
  <main class="main-content">
`

const layoutFooter = `
  </main>
</div>
<script>
function toggleTheme() {
  try {
    var cur = document.documentElement.getAttribute('data-theme');
    if (!cur) {
      cur = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }
    var next = cur === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', next);
    localStorage.setItem('wot-theme', next);
    updateThemeLabel(next);
  } catch(e) {}
}
function updateThemeLabel(theme) {
  var el = document.getElementById('theme-toggle-text');
  if (el) el.textContent = theme === 'dark' ? 'Theme: Dark' : 'Theme: Light';
}
(function() {
  try {
    var cur = document.documentElement.getAttribute('data-theme');
    if (!cur) {
      cur = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }
    updateThemeLabel(cur);
  } catch(e) {}
})();
</script>
</body>
</html>
`

const overviewBodyHTML = `
<div class="page-label">Dashboard</div>
<div class="page-header">
  <div>
    <h1 class="page-title">A record of the internet, as it was.</h1>
    <p class="page-subtitle">Live view of fetches, verifications and source health from your local Words on the Street instance.</p>
  </div>
  <div class="page-header-actions">
    <div class="quote-pill">"Evidence outlives claims."</div>
    <a href="/fetches" class="btn btn-primary">Browse fetches</a>
  </div>
</div>

<div class="trust-strip {{if .TrustVerified}}trust-verified{{else}}trust-failed{{end}}">
  <div class="trust-content">
    <span>{{if .TrustVerified}}✓{{else}}✕{{end}}</span>
    <span>{{.TrustMessage}}</span>
  </div>
  <span class="trust-badge">{{if .TrustVerified}}Tamper-evident{{else}}Integrity alert{{end}}</span>
</div>

<div class="stats-grid">
  <div class="stat-card">
    <div class="stat-label">Total fetches</div>
    <div class="stat-value">{{.FetchesTotal}}</div>
    <div class="stat-sub">recorded in append-only ledger</div>
  </div>
  <div class="stat-card">
    <div class="stat-label">Total verifications</div>
    <div class="stat-value">{{.VerifyTotal}}</div>
    <div class="stat-sub">comparisons performed</div>
  </div>
  <div class="stat-card">
    <div class="stat-label">Sources monitored</div>
    <div class="stat-value">{{len .Statuses}}</div>
    <div class="stat-sub">{{.HealthyCount}} healthy · {{.DegradedCount}} degraded · {{.DownCount}} down</div>
  </div>
</div>

<div class="card">
  <div class="card-header">
    <h2 class="card-title">Source health</h2>
    <a href="/sources" class="card-link">View all &rarr;</a>
  </div>
  <div style="padding: 1.25rem;">
    <div class="sources-grid" style="margin-bottom: 0;">
      {{range .Statuses}}
      <div class="source-card" data-source="{{.Source}}" data-state="{{.State}}">
        <div class="source-card-header">
          <div class="source-info">
            <span class="source-avatar">{{initial .Source}}</span>
            <span class="source-name mono" data-source="{{.Source}}">{{.Source}}</span>
          </div>
          <span class="badge badge-{{.State}}" data-state="{{.State}}">{{.State}}</span>
        </div>
        <div class="source-meta-row">
          <span class="source-meta-label">Primary:</span>
          <span class="source-meta-value mono">{{.Primary}}</span>
        </div>
        <div class="source-meta-row">
          <span class="source-meta-label">Serving:</span>
          <span class="source-meta-value mono">{{.Serving}}</span>
        </div>
      </div>
      {{else}}
      <div class="empty-state">No sources registered.</div>
      {{end}}
    </div>
  </div>
</div>

<div class="card">
  <div class="card-header">
    <h2 class="card-title">Recent activity</h2>
  </div>
  <div class="table-wrap">
    <table>
      <thead><tr><th>Time (UTC)</th><th>Type</th><th>Resolved URL</th><th>Detail</th><th>Status</th></tr></thead>
      <tbody>
        {{range .RecentActivity}}
        <tr>
          <td class="mono"><a class="table-link" href="{{if eq .Type "fetch"}}/fetches/{{.Hash}}{{else}}/verifications/{{.Hash}}{{end}}">{{formatTime .Timestamp}}</a></td>
          <td><span class="badge badge-subtle">{{.Type}}</span></td>
          <td class="mono"><a class="table-link" href="{{if eq .Type "fetch"}}/fetches/{{.Hash}}{{else}}/verifications/{{.Hash}}{{end}}">{{.ResolvedURL}}</a></td>
          <td class="mono">{{.Detail}}</td>
          <td><span class="badge badge-{{.Status}}">{{.Status}}</span></td>
        </tr>
        {{else}}
        <tr><td colspan="5" class="empty-state">No activity recorded yet.</td></tr>
        {{end}}
      </tbody>
    </table>
  </div>
</div>

<div class="card">
  <div class="card-header">
    <h2 class="card-title">Recent fetches</h2>
    <a href="/fetches" class="card-link">View all &rarr;</a>
  </div>
  <div class="table-wrap">
    <table>
      <thead><tr><th>Time (UTC)</th><th>Resolved URL</th><th>Backend</th><th>Path</th><th>Recorded arguments</th></tr></thead>
      <tbody>
        {{range .Fetches}}
        <tr>
          <td class="mono"><a class="table-link" href="/fetches/{{.Hash}}">{{formatTime .Timestamp}}</a></td>
          <td class="mono"><a class="table-link" href="/fetches/{{.Hash}}">{{.ResolvedURL}}</a></td>
          <td class="mono">{{.Backend}}</td>
          <td><span class="badge badge-{{if .Fallback}}fallback{{else}}primary{{end}}">{{if .Fallback}}fallback{{else}}primary{{end}}</span></td>
          <td class="mono">{{range .Args}}{{.}} {{end}}</td>
        </tr>
        {{else}}
        <tr><td colspan="5" class="empty-state">No fetches recorded yet.</td></tr>
        {{end}}
      </tbody>
    </table>
  </div>
</div>

<div class="card">
  <div class="card-header">
    <h2 class="card-title">Recent verifications</h2>
    <a href="/verifications" class="card-link">View all &rarr;</a>
  </div>
  <div class="table-wrap">
    <table>
      <thead><tr><th>Time (UTC)</th><th>State</th><th>Resolved URL</th><th>Observation record</th></tr></thead>
      <tbody>
        {{range .Verifications}}
        <tr>
          <td class="mono"><a class="table-link" href="/verifications/{{.ObservationHash}}">{{formatTime .Timestamp}}</a></td>
          <td><span class="badge badge-{{.State}}">{{.State}}</span></td>
          <td class="mono"><a class="table-link" href="/verifications/{{.ObservationHash}}">{{.ResolvedURL}}</a></td>
          <td class="mono"><a class="table-link" href="/verifications/{{.ObservationHash}}">{{shortHash .ObservationHash}}</a></td>
        </tr>
        {{else}}
        <tr><td colspan="4" class="empty-state">No verifications recorded yet.</td></tr>
        {{end}}
      </tbody>
    </table>
  </div>
</div>
`

const fetchesBodyHTML = `
<div class="page-label">Evidence</div>
<div class="page-header">
  <div>
    <h1 class="page-title">Fetches</h1>
    <p class="page-subtitle">Full record of external content retrieved and preserved in the evidence store ({{.FetchesTotal}} total).</p>
  </div>
  <div class="page-header-actions">
    <a href="/verifications" class="btn btn-primary">Audit verifications</a>
  </div>
</div>

<div class="card">
  <div class="table-wrap">
    <table>
      <thead><tr><th>Time (UTC)</th><th>Resolved URL</th><th>Backend</th><th>Path</th><th>Recorded arguments</th><th>Content hash</th></tr></thead>
      <tbody>
        {{range .Fetches}}
        <tr>
          <td class="mono"><a class="table-link" href="/fetches/{{.Hash}}">{{formatTime .Timestamp}}</a></td>
          <td class="mono"><a class="table-link" href="/fetches/{{.Hash}}">{{.ResolvedURL}}</a></td>
          <td class="mono">{{.Backend}}</td>
          <td><span class="badge badge-{{if .Fallback}}fallback{{else}}primary{{end}}">{{if .Fallback}}fallback{{else}}primary{{end}}</span></td>
          <td class="mono">{{range .Args}}{{.}} {{end}}</td>
          <td class="mono"><a class="table-link" href="/fetches/{{.Hash}}">{{shortHash .Hash}}</a></td>
        </tr>
        {{else}}
        <tr><td colspan="6" class="empty-state">No fetches recorded yet.</td></tr>
        {{end}}
      </tbody>
    </table>
  </div>
</div>
`

const fetchDetailBodyHTML = `
<div class="page-header" style="margin-bottom: 1rem;">
  <div>
    <a href="/fetches" class="back-link">&larr; Back to fetches</a>
    <h1 class="page-title">Fetch details</h1>
    <p class="page-subtitle">Preserved record with content hash and execution arguments.</p>
  </div>
  <div class="page-header-actions">
    <span class="badge badge-{{if .IsFallback}}fallback{{else}}primary{{end}}">{{if .IsFallback}}fallback{{else}}primary{{end}}</span>
    <span class="badge badge-subtle mono">#{{shortHash .Hash}}</span>
    <a href="/verifications" class="btn btn-primary">Audit verification</a>
  </div>
</div>

<div class="detail-grid">
  <div class="detail-label">Resolved URL</div>
  <div class="detail-value mono">{{.ResolvedURL}}</div>

  <div class="detail-label">Backend</div>
  <div class="detail-value mono">{{.Backend}} ({{if .IsFallback}}fallback{{else}}primary{{end}})</div>

  <div class="detail-label">Backend status</div>
  <div class="detail-value mono">{{.Status}}</div>

  <div class="detail-label">Recorded arguments</div>
  <div class="detail-value mono">{{range .Args}}{{.}} {{else}}&mdash;{{end}}</div>

  <div class="detail-label">Content SHA-256</div>
  <div class="detail-value mono">{{.Hash}}</div>

  <div class="detail-label">Record hash</div>
  <div class="detail-value mono">{{.RecordHash}}</div>

  <div class="detail-label">Timestamp (UTC)</div>
  <div class="detail-value">{{formatTime .Timestamp}}</div>
</div>

<div class="detail-footer-quotes">
  <span>"Not a screenshot. The real thing."</span>
  <span>Captured, hashed, and preserved.</span>
</div>
`

const verificationsBodyHTML = `
<div class="page-label">Audit</div>
<div class="page-header">
  <div>
    <h1 class="page-title">Verifications</h1>
    <p class="page-subtitle">Full record of verification checks against preserved evidence ({{.VerifyTotal}} total).</p>
  </div>
  <div class="page-header-actions">
    <a href="/sources" class="btn btn-primary">Monitor sources</a>
  </div>
</div>

<div class="card">
  <div class="table-wrap">
    <table>
      <thead><tr><th>Time (UTC)</th><th>State</th><th>Resolved URL</th><th>Observation record</th></tr></thead>
      <tbody>
        {{range .Verifications}}
        <tr>
          <td class="mono"><a class="table-link" href="/verifications/{{.ObservationHash}}">{{formatTime .Timestamp}}</a></td>
          <td><span class="badge badge-{{.State}}">{{.State}}</span></td>
          <td class="mono"><a class="table-link" href="/verifications/{{.ObservationHash}}">{{.ResolvedURL}}</a></td>
          <td class="mono"><a class="table-link" href="/verifications/{{.ObservationHash}}">{{shortHash .ObservationHash}}</a></td>
        </tr>
        {{else}}
        <tr><td colspan="4" class="empty-state">No verifications recorded yet.</td></tr>
        {{end}}
      </tbody>
    </table>
  </div>
</div>
`

const verificationDetailBodyHTML = `
<div class="page-header" style="margin-bottom: 1rem;">
  <div>
    <a href="/verifications" class="back-link">&larr; Back to verifications</a>
    <h1 class="page-title">Verification result</h1>
    <p class="page-subtitle">Re-fetch comparison against the append-only evidence record.</p>
  </div>
  <div class="page-header-actions">
    <div class="quote-pill">"Same URL. Different story."</div>
    <a href="/sources" class="btn btn-primary">Check source health</a>
  </div>
</div>

<div class="detail-grid">
  <div class="detail-label">State</div>
  <div class="detail-value"><span class="badge badge-{{.State}}">{{.State}}</span></div>

  <div class="detail-label">Resolved URL</div>
  <div class="detail-value mono">{{.ResolvedURL}}</div>

  <div class="detail-label">Observation record hash</div>
  <div class="detail-value mono">{{.ObservationHash}}</div>

  <div class="detail-label">Verified at (UTC)</div>
  <div class="detail-value">{{formatTime .Timestamp}}</div>
</div>

{{if and (eq .State "changed") .Diff}}
<div class="diff-container">
  <div class="diff-header-bar">
    <span>What changed</span>
  </div>
  <div class="diff-view mono">
    {{range .DiffLines}}
    <div class="diff-line diff-{{.Type}}">{{.Content}}</div>
    {{end}}
  </div>
</div>
{{end}}

<div class="detail-footer-quotes">
  <span>"Content changed, but your original copy is still safe."</span>
  <span>Evidence never disappears.</span>
</div>
`

const sourcesBodyHTML = `
<div class="page-label">Registry</div>
<div class="page-header">
  <div>
    <h1 class="page-title">Sources</h1>
    <p class="page-subtitle">Live status and failover health of all registered backends ({{len .Statuses}} monitored).</p>
  </div>
  <div class="page-header-actions">
    <span class="mono" style="font-size: var(--fs-xs); color: var(--mut);">
      {{.HealthyCount}} healthy &middot; {{.DegradedCount}} degraded &middot; {{.DownCount}} down
    </span>
    <a href="/" class="btn btn-primary">Return to overview</a>
  </div>
</div>

<div class="sources-grid">
  {{range .Statuses}}
  <div class="source-card" data-source="{{.Source}}" data-state="{{.State}}">
    <div class="source-card-header">
      <div class="source-info">
        <span class="source-avatar">{{initial .Source}}</span>
        <span class="source-name mono" data-source="{{.Source}}">{{.Source}}</span>
      </div>
      <span class="badge badge-{{.State}}" data-state="{{.State}}">{{.State}}</span>
    </div>
    <div class="source-meta-row">
      <span class="source-meta-label">Primary:</span>
      <span class="source-meta-value mono">{{.Primary}}</span>
    </div>
    <div class="source-meta-row">
      <span class="source-meta-label">Serving:</span>
      <span class="source-meta-value mono">{{.Serving}}</span>
    </div>
    <div class="source-meta-row">
      <span class="source-meta-label">Last checked:</span>
      <span class="source-meta-value">{{$.GeneratedAt.Format "15:04:05 UTC"}}</span>
    </div>
    {{if .Reports}}
    <div class="source-reports">
      <div style="font-weight: 600; margin-bottom: 0.25rem; color: var(--mut);">Backends:</div>
      {{range .Reports}}
      <div class="report-item">
        <span class="report-name mono">{{.BackendName}}</span>:
        <span class="badge badge-{{if eq .Status "reachable"}}healthy{{else}}down{{end}}">{{.Status}}</span>
        {{if .DetectedVersion}}<span class="mono" style="color: var(--mut);">({{.DetectedVersion}})</span>{{end}}
      </div>
      {{end}}
    </div>
    {{end}}
  </div>
  {{else}}
  <div class="empty-state">No sources registered.</div>
  {{end}}
</div>
`

var (
	overviewTemplate           = template.Must(template.New("overview").Funcs(templateFuncs).Parse(layoutHeader + overviewBodyHTML + layoutFooter))
	fetchesTemplate            = template.Must(template.New("fetches").Funcs(templateFuncs).Parse(layoutHeader + fetchesBodyHTML + layoutFooter))
	fetchDetailTemplate        = template.Must(template.New("fetchDetail").Funcs(templateFuncs).Parse(layoutHeader + fetchDetailBodyHTML + layoutFooter))
	verificationsTemplate      = template.Must(template.New("verifications").Funcs(templateFuncs).Parse(layoutHeader + verificationsBodyHTML + layoutFooter))
	verificationDetailTemplate = template.Must(template.New("verificationDetail").Funcs(templateFuncs).Parse(layoutHeader + verificationDetailBodyHTML + layoutFooter))
	sourcesTemplate            = template.Must(template.New("sources").Funcs(templateFuncs).Parse(layoutHeader + sourcesBodyHTML + layoutFooter))
)
