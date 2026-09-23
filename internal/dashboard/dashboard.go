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
* { box-sizing: border-box; }
body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; background-color: #f8fafc; color: #1e293b; line-height: 1.5; font-size: 14px; }
.layout { display: flex; min-height: 100vh; }
.sidebar { width: 240px; background-color: #ffffff; border-right: 1px solid #e2e8f0; display: flex; flex-direction: column; flex-shrink: 0; position: sticky; top: 0; height: 100vh; }
.sidebar-header { padding: 1.5rem 1.25rem 1rem; border-bottom: 1px solid #f1f5f9; }
.sidebar-title { font-size: 1.05rem; font-weight: 700; color: #0f172a; margin: 0; letter-spacing: -0.01em; }
.sidebar-tagline { font-size: 0.75rem; color: #64748b; margin-top: 0.35rem; line-height: 1.35; }
.sidebar-nav { padding: 1rem 0.75rem; flex: 1; }
.nav-link { display: flex; align-items: center; gap: 0.75rem; padding: 0.6rem 0.85rem; border-radius: 6px; color: #475569; text-decoration: none; font-size: 0.875rem; font-weight: 500; margin-bottom: 0.25rem; transition: background 0.15s, color 0.15s; }
.nav-link:hover { background-color: #f1f5f9; color: #0f172a; }
.nav-link.active { background-color: #e6f4ea; color: #137333; font-weight: 600; }
.nav-icon { width: 18px; height: 18px; flex-shrink: 0; }
.sidebar-footer { padding: 1.25rem; border-top: 1px solid #f1f5f9; font-size: 0.75rem; background: #fafafa; }
.status-indicator { display: flex; align-items: center; gap: 0.4rem; font-weight: 600; color: #334155; margin-bottom: 0.2rem; }
.status-dot { width: 8px; height: 8px; border-radius: 50%; background-color: #16a34a; }
.sidebar-addr { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; color: #64748b; margin-bottom: 0.4rem; font-size: 0.8rem; }
.sidebar-note { color: #94a3b8; font-style: italic; }
.main-content { flex: 1; padding: 2rem 2.5rem; max-width: 1200px; }
.page-label { font-size: 0.7rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.08em; color: #64748b; margin-bottom: 0.25rem; }
.page-header { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 1.5rem; gap: 1rem; }
.page-title { font-size: 1.5rem; font-weight: 700; color: #0f172a; margin: 0 0 0.25rem 0; letter-spacing: -0.02em; }
.page-subtitle { font-size: 0.875rem; color: #64748b; margin: 0; }
.quote-pill { background: #ffffff; border: 1px solid #e2e8f0; border-radius: 8px; padding: 0.6rem 1rem; font-style: italic; font-size: 0.85rem; color: #475569; font-family: Georgia, serif; box-shadow: 0 1px 2px rgba(0,0,0,0.02); white-space: nowrap; }
.trust-strip { display: flex; align-items: center; justify-content: space-between; padding: 0.75rem 1.25rem; border-radius: 8px; margin-bottom: 1.5rem; font-size: 0.875rem; }
.trust-strip.trust-verified { background: #f0fdf4; border: 1px solid #bbf7d0; color: #166534; }
.trust-strip.trust-failed { background: #fef2f2; border: 1px solid #fecaca; color: #991b1b; }
.trust-content { display: flex; align-items: center; gap: 0.5rem; font-weight: 500; }
.trust-badge { font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; font-weight: 600; opacity: 0.85; }
.stats-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 1rem; margin-bottom: 1.75rem; }
.stat-card { background: #ffffff; border: 1px solid #e2e8f0; border-radius: 8px; padding: 1.25rem; box-shadow: 0 1px 3px rgba(0,0,0,0.02); }
.stat-label { font-size: 0.75rem; font-weight: 600; text-transform: uppercase; letter-spacing: 0.05em; color: #64748b; margin-bottom: 0.4rem; }
.stat-value { font-size: 1.75rem; font-weight: 700; color: #0f172a; }
.stat-sub { font-size: 0.75rem; color: #64748b; margin-top: 0.25rem; }
.card { background: #ffffff; border: 1px solid #e2e8f0; border-radius: 8px; margin-bottom: 1.5rem; overflow: hidden; box-shadow: 0 1px 3px rgba(0,0,0,0.02); }
.card-header { display: flex; justify-content: space-between; align-items: center; padding: 1rem 1.25rem; border-bottom: 1px solid #e2e8f0; background: #fafafa; }
.card-title { font-size: 0.95rem; font-weight: 600; color: #0f172a; margin: 0; }
.card-link { font-size: 0.8rem; font-weight: 600; color: #0f766e; text-decoration: none; }
.card-link:hover { text-decoration: underline; }
table { width: 100%; border-collapse: collapse; text-align: left; font-size: 0.85rem; }
th { background: #f8fafc; color: #64748b; font-weight: 600; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; padding: 0.65rem 1rem; border-bottom: 1px solid #e2e8f0; }
td { padding: 0.75rem 1rem; border-bottom: 1px solid #f1f5f9; vertical-align: top; color: #334155; }
tr:last-child td { border-bottom: none; }
tr:hover td { background-color: #f8fafc; }
a.table-link { color: #0f766e; text-decoration: none; font-weight: 500; }
a.table-link:hover { text-decoration: underline; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 0.8rem; word-break: break-all; }
.badge { display: inline-flex; align-items: center; gap: 0.3rem; padding: 0.2rem 0.6rem; border-radius: 9999px; font-size: 0.75rem; font-weight: 600; }
.badge-healthy, .badge-identical { background: #dcfce7; color: #15803d; }
.badge-degraded, .badge-changed { background: #fef3c7; color: #b45309; }
.badge-down, .badge-gone { background: #fee2e2; color: #b91c1c; }
.badge-primary { background: #dcfce7; color: #15803d; }
.badge-fallback { background: #fef3c7; color: #b45309; }
.badge-subtle { background: #f1f5f9; color: #475569; }
.sources-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 1rem; margin-bottom: 1.5rem; }
.source-card { background: #ffffff; border: 1px solid #e2e8f0; border-radius: 8px; padding: 1.25rem; box-shadow: 0 1px 3px rgba(0,0,0,0.02); }
.source-card-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 1rem; }
.source-info { display: flex; align-items: center; gap: 0.6rem; }
.source-avatar { width: 32px; height: 32px; border-radius: 6px; color: #ffffff; display: flex; align-items: center; justify-content: center; font-weight: 700; font-size: 0.9rem; text-transform: uppercase; flex-shrink: 0; }
.source-name { font-weight: 600; font-size: 0.95rem; color: #0f172a; }
.source-meta-row { display: flex; justify-content: space-between; font-size: 0.8rem; margin-bottom: 0.4rem; }
.source-meta-label { color: #64748b; }
.source-meta-value { color: #1e293b; font-weight: 500; }
.source-reports { margin-top: 0.75rem; padding-top: 0.75rem; border-top: 1px solid #f1f5f9; font-size: 0.75rem; }
.report-item { margin-bottom: 0.35rem; }
.report-name { font-weight: 600; }
.empty-state { padding: 3rem 1.5rem; text-align: center; color: #64748b; font-size: 0.9rem; }
.back-link { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.85rem; font-weight: 600; color: #0f766e; text-decoration: none; margin-bottom: 1.25rem; }
.back-link:hover { text-decoration: underline; }
.detail-grid { display: grid; grid-template-columns: 180px 1fr; gap: 0.75rem 1.5rem; padding: 1.25rem; font-size: 0.875rem; background: #ffffff; border: 1px solid #e2e8f0; border-radius: 8px; margin-bottom: 1.5rem; }
.detail-label { color: #64748b; font-weight: 500; }
.detail-value { color: #0f172a; word-break: break-all; }
.detail-footer-quotes { display: flex; justify-content: space-between; font-size: 0.8rem; color: #94a3b8; font-style: italic; margin-top: 1.5rem; padding-top: 1rem; border-top: 1px solid #e2e8f0; }
.diff-container { background: #ffffff; border: 1px solid #e2e8f0; border-radius: 8px; margin-bottom: 1.5rem; overflow: hidden; }
.diff-header-bar { padding: 0.75rem 1.25rem; background: #fafafa; border-bottom: 1px solid #e2e8f0; font-weight: 600; font-size: 0.85rem; color: #0f172a; }
.diff-view { padding: 0.75rem 0; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 0.85rem; line-height: 1.5; overflow-x: auto; }
.diff-line { padding: 0.15rem 1.25rem; white-space: pre-wrap; word-break: break-all; }
.diff-line.diff-removed { background-color: #fee2e2; color: #991b1b; }
.diff-line.diff-added { background-color: #dcfce7; color: #166534; }
.diff-line.diff-header { background-color: #f1f5f9; color: #64748b; font-weight: 600; }
.diff-line.diff-context { color: #334155; }
`

const layoutHeader = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Words on the Street — Local Dashboard</title>
<style>` + sharedCSS + `</style>
</head>
<body>
<div class="layout">
  <aside class="sidebar">
    <div class="sidebar-header">
      <div class="sidebar-title">Words on the Street</div>
      <div class="sidebar-tagline">What people are saying, and proof they said it.</div>
    </div>
    <nav class="sidebar-nav">
      <a href="/" class="nav-link {{if eq .ActiveNav "overview"}}active{{end}}">
        <svg class="nav-icon" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.7"><rect x="3" y="3" width="6" height="6" rx="1"/><rect x="11" y="3" width="6" height="6" rx="1"/><rect x="3" y="11" width="6" height="6" rx="1"/><rect x="11" y="11" width="6" height="6" rx="1"/></svg>
        <span>Overview</span>
      </a>
      <a href="/fetches" class="nav-link {{if eq .ActiveNav "fetches"}}active{{end}}">
        <svg class="nav-icon" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M4 4h12v12H4z"/><path d="M7 8h6M7 12h4"/></svg>
        <span>Fetches</span>
      </a>
      <a href="/verifications" class="nav-link {{if eq .ActiveNav "verifications"}}active{{end}}">
        <svg class="nav-icon" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M10 2l6 3v5c0 4.5-3 8-6 9-3-1-6-4.5-6-9V5l6-3z"/><path d="M8 10l2 2 3-3"/></svg>
        <span>Verifications</span>
      </a>
      <a href="/sources" class="nav-link {{if eq .ActiveNav "sources"}}active{{end}}">
        <svg class="nav-icon" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.7"><ellipse cx="10" cy="5" rx="7" ry="2.5"/><path d="M3 5v5c0 1.4 3.1 2.5 7 2.5s7-1.1 7-2.5V5"/><path d="M3 10v5c0 1.4 3.1 2.5 7 2.5s7-1.1 7-2.5v-5"/></svg>
        <span>Sources</span>
      </a>
    </nav>
    <div class="sidebar-footer">
      <div class="status-indicator">
        <span class="status-dot"></span>
        <span class="status-text">Local Dashboard</span>
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
  <div class="quote-pill">"Evidence outlives claims."</div>
</div>

<div class="trust-strip {{if .TrustVerified}}trust-verified{{else}}trust-failed{{end}}">
  <div class="trust-content">
    <span>{{if .TrustVerified}}✓{{else}}✕{{end}}</span>
    <span>{{.TrustMessage}}</span>
  </div>
  <span class="trust-badge">{{if .TrustVerified}}Tamper-evident{{else}}Integrity Alert{{end}}</span>
</div>

<div class="stats-grid">
  <div class="stat-card">
    <div class="stat-label">Total Fetches</div>
    <div class="stat-value">{{.FetchesTotal}}</div>
    <div class="stat-sub">recorded in append-only ledger</div>
  </div>
  <div class="stat-card">
    <div class="stat-label">Total Verifications</div>
    <div class="stat-value">{{.VerifyTotal}}</div>
    <div class="stat-sub">comparisons performed</div>
  </div>
  <div class="stat-card">
    <div class="stat-label">Sources Monitored</div>
    <div class="stat-value">{{len .Statuses}}</div>
    <div class="stat-sub">{{.HealthyCount}} healthy · {{.DegradedCount}} degraded · {{.DownCount}} down</div>
  </div>
</div>

<div class="card">
  <div class="card-header">
    <h2 class="card-title">Source Health</h2>
    <a href="/sources" class="card-link">View all &rarr;</a>
  </div>
  <div style="padding: 1.25rem;">
    <div class="sources-grid" style="margin-bottom: 0;">
      {{range .Statuses}}
      <div class="source-card" data-source="{{.Source}}" data-state="{{.State}}">
        <div class="source-card-header">
          <div class="source-info">
            <span class="source-avatar" style="background-color: {{avatarBg .Source}};">{{initial .Source}}</span>
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
    <h2 class="card-title">Recent Activity</h2>
  </div>
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

<div class="card">
  <div class="card-header">
    <h2 class="card-title">Recent Fetches</h2>
    <a href="/fetches" class="card-link">View all &rarr;</a>
  </div>
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

<div class="card">
  <div class="card-header">
    <h2 class="card-title">Recent Verifications</h2>
    <a href="/verifications" class="card-link">View all &rarr;</a>
  </div>
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
`

const fetchesBodyHTML = `
<div class="page-label">Evidence</div>
<div class="page-header">
  <div>
    <h1 class="page-title">Fetches</h1>
    <p class="page-subtitle">Full record of external content retrieved and preserved in the evidence store ({{.FetchesTotal}} total).</p>
  </div>
</div>

<div class="card">
  <table>
    <thead><tr><th>Time (UTC)</th><th>Resolved URL</th><th>Backend</th><th>Path</th><th>Recorded arguments</th><th>Content Hash</th></tr></thead>
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
`

const fetchDetailBodyHTML = `
<a href="/fetches" class="back-link">&larr; Back to fetches</a>

<div class="page-header">
  <div>
    <h1 class="page-title">Fetch Details</h1>
    <p class="page-subtitle">Preserved record with content hash and execution arguments.</p>
  </div>
  <div style="display: flex; gap: 0.5rem; align-items: center;">
    <span class="badge badge-{{if .IsFallback}}fallback{{else}}primary{{end}}">{{if .IsFallback}}fallback{{else}}primary{{end}}</span>
    <span class="badge badge-subtle mono">#{{shortHash .Hash}}</span>
  </div>
</div>

<div class="detail-grid">
  <div class="detail-label">Resolved URL</div>
  <div class="detail-value mono">{{.ResolvedURL}}</div>

  <div class="detail-label">Backend</div>
  <div class="detail-value mono">{{.Backend}} ({{if .IsFallback}}fallback{{else}}primary{{end}})</div>

  <div class="detail-label">Backend Status</div>
  <div class="detail-value mono">{{.Status}}</div>

  <div class="detail-label">Recorded Arguments</div>
  <div class="detail-value mono">{{range .Args}}{{.}} {{else}}&mdash;{{end}}</div>

  <div class="detail-label">Content SHA-256</div>
  <div class="detail-value mono">{{.Hash}}</div>

  <div class="detail-label">Record Hash</div>
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
</div>

<div class="card">
  <table>
    <thead><tr><th>Time (UTC)</th><th>State</th><th>Resolved URL</th><th>Observation Record</th></tr></thead>
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
`

const verificationDetailBodyHTML = `
<a href="/verifications" class="back-link">&larr; Back to verifications</a>

<div class="page-header">
  <div>
    <h1 class="page-title">Verification Result</h1>
    <p class="page-subtitle">Re-fetch comparison against the append-only evidence record.</p>
  </div>
  <div class="quote-pill">"Same URL. Different story."</div>
</div>

<div class="detail-grid">
  <div class="detail-label">State</div>
  <div class="detail-value"><span class="badge badge-{{.State}}">{{.State}}</span></div>

  <div class="detail-label">Resolved URL</div>
  <div class="detail-value mono">{{.ResolvedURL}}</div>

  <div class="detail-label">Observation Record Hash</div>
  <div class="detail-value mono">{{.ObservationHash}}</div>

  <div class="detail-label">Verified At (UTC)</div>
  <div class="detail-value">{{formatTime .Timestamp}}</div>
</div>

{{if and (eq .State "changed") .Diff}}
<div class="diff-container">
  <div class="diff-header-bar">
    <span>What Changed</span>
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
  <div style="font-size: 0.85rem; color: #64748b;">
    {{.HealthyCount}} healthy &middot; {{.DegradedCount}} degraded &middot; {{.DownCount}} down
  </div>
</div>

<div class="sources-grid">
  {{range .Statuses}}
  <div class="source-card" data-source="{{.Source}}" data-state="{{.State}}">
    <div class="source-card-header">
      <div class="source-info">
        <span class="source-avatar" style="background-color: {{avatarBg .Source}};">{{initial .Source}}</span>
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
      <div style="font-weight: 600; margin-bottom: 0.25rem; color: #475569;">Backends:</div>
      {{range .Reports}}
      <div class="report-item">
        <span class="report-name mono">{{.BackendName}}</span>:
        <span class="badge badge-{{if eq .Status "reachable"}}healthy{{else}}down{{end}}">{{.Status}}</span>
        {{if .DetectedVersion}}<span class="mono" style="color: #64748b;">({{.DetectedVersion}})</span>{{end}}
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
