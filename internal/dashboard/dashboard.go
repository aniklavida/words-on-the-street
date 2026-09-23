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
	"time"

	"github.com/aniklavida/words-on-the-street/internal/backend"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/verify"
)

// DefaultAddr is the loopback address the dashboard binds to by default. It is
// an explicit IP, never a hostname, so it cannot resolve to a public interface.
const DefaultAddr = "127.0.0.1:8787"

// maxRows bounds how many recent entries are rendered; the store is unbounded
// and the page only needs to show the tail of it.
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
	ResolvedURL string    `json:"resolved_url"`
	Backend     string    `json:"backend"`
	Fallback    bool      `json:"fallback"`
	Status      string    `json:"status"`
	Args        []string  `json:"args,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// verifyRow is one verification observation.
type verifyRow struct {
	State           string    `json:"state"`
	ResolvedURL     string    `json:"resolved_url"`
	ObservationHash string    `json:"observation_record_hash"`
	Timestamp       time.Time `json:"timestamp"`
}

// pageData is everything a single render needs. It is also the JSON body of
// /api/state so a script can read the same numbers the page shows.
type pageData struct {
	GeneratedAt   time.Time              `json:"generated_at"`
	LoopbackAddr  string                 `json:"loopback_addr"`
	Statuses      []backend.SourceStatus `json:"statuses"`
	Fetches       []fetchRow             `json:"fetches"`
	FetchesTotal  int                    `json:"fetches_total"`
	Verifications []verifyRow            `json:"verifications"`
	VerifyTotal   int                    `json:"verifications_total"`
	ReadOnly      bool                   `json:"read_only"`
}

// buildPage reads the store and the live status and assembles the view. It is
// the single place the dashboard derives anything, so the page and the JSON
// state cannot describe different things.
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
	}
	if s.Registry != nil {
		page.Statuses = backend.CheckAllStatus(ctx, s.Registry)
	}

	// The store lists oldest first; walk backwards for the most recent rows
	// while still counting every entry.
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec == nil {
			continue
		}
		if state, ok := verificationState(rec); ok {
			page.VerifyTotal++
			if len(page.Verifications) < maxRows {
				page.Verifications = append(page.Verifications, verifyRow{
					State:           string(state),
					ResolvedURL:     rec.ResolvedURL,
					ObservationHash: rec.RecordHash,
					Timestamp:       rec.Timestamp,
				})
			}
			continue
		}
		page.FetchesTotal++
		if len(page.Fetches) < maxRows {
			page.Fetches = append(page.Fetches, fetchRow{
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

// verificationState maps a recorded verification observation back to its state.
// The status strings are the ones internal/verify writes, so this reads the
// comparison rather than making a second one.
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

// Handler returns the read-only routes. Nothing here mutates the store.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/api/state", s.handleState)
	return readOnly(mux)
}

// readOnly refuses every method that could carry an intent to change something.
// The dashboard has no mutating route, so a POST is a mistake and is refused
// before it reaches a handler.
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

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	page, err := s.buildPage(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pageTemplate.Execute(w, page)
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

var pageTemplate = template.Must(template.New("dashboard").Parse(pageHTML))

// pageHTML is intentionally plain. It renders only already-sanitized record
// fields (resolved URL and backend arguments); it never receives a raw payload
// or raw argument, so a rendering change cannot re-derive a credential.
const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<!-- v1 refreshes by reloading; live push is planned, not implemented. -->
<meta http-equiv="refresh" content="10">
<title>Words on the Street — local dashboard</title>
<style>
body { font-family: system-ui, sans-serif; margin: 2rem auto; max-width: 70rem; padding: 0 1rem; color: #1b1b1b; }
h1 { margin-bottom: 0.25rem; }
.notice { background: #f3f3f3; border-left: 4px solid #888; padding: 0.5rem 1rem; }
table { border-collapse: collapse; width: 100%; margin-bottom: 2rem; }
th, td { text-align: left; padding: 0.35rem 0.6rem; border-bottom: 1px solid #ddd; vertical-align: top; }
th { background: #fafafa; }
.mono { font-family: ui-monospace, monospace; font-size: 0.9em; word-break: break-all; }
.state-healthy { color: #0a7a2f; }
.state-degraded { color: #b06a00; }
.state-down { color: #b00020; }
.state-identical { color: #0a7a2f; }
.state-changed { color: #b06a00; }
.state-gone { color: #b00020; }
.empty { color: #777; }
</style>
</head>
<body>
<h1>Words on the Street</h1>
<p class="notice">
Local only: bound to loopback ({{.LoopbackAddr}}). Read-only: no route fetches,
verifies, or writes. The page reloads every 10&nbsp;seconds; live push is
<b>planned</b>, not implemented.
</p>
<p>Generated {{.GeneratedAt.Format "2006-01-02T15:04:05Z07:00"}}.</p>

<h2>Backend status</h2>
<table>
<thead><tr><th>Source</th><th>State</th><th>Primary</th><th>Serving</th></tr></thead>
<tbody>
{{range .Statuses}}
<tr>
<td class="mono" data-source="{{.Source}}">{{.Source}}</td>
<td class="state-{{.State}}" data-state="{{.State}}">{{.State}}</td>
<td class="mono">{{.Primary}}</td>
<td class="mono">{{.Serving}}</td>
</tr>
{{else}}
<tr><td colspan="4" class="empty">No sources registered.</td></tr>
{{end}}
</tbody>
</table>

<h2>Recent fetches ({{.FetchesTotal}} recorded)</h2>
<table>
<thead><tr><th>Time (UTC)</th><th>Resolved URL</th><th>Backend</th><th>Path</th><th>Recorded arguments</th></tr></thead>
<tbody>
{{range .Fetches}}
<tr>
<td class="mono">{{.Timestamp.Format "2006-01-02 15:04:05"}}</td>
<td class="mono">{{.ResolvedURL}}</td>
<td class="mono">{{.Backend}}</td>
<td>{{if .Fallback}}fallback{{else}}primary{{end}}</td>
<td class="mono">{{range .Args}}{{.}} {{end}}</td>
</tr>
{{else}}
<tr><td colspan="5" class="empty">No fetches recorded.</td></tr>
{{end}}
</tbody>
</table>

<h2>Recent verifications ({{.VerifyTotal}} recorded)</h2>
<table>
<thead><tr><th>Time (UTC)</th><th>State</th><th>Resolved URL</th><th>Observation record</th></tr></thead>
<tbody>
{{range .Verifications}}
<tr>
<td class="mono">{{.Timestamp.Format "2006-01-02 15:04:05"}}</td>
<td class="state-{{.State}}">{{.State}}</td>
<td class="mono">{{.ResolvedURL}}</td>
<td class="mono">{{.ObservationHash}}</td>
</tr>
{{else}}
<tr><td colspan="4" class="empty">No verifications recorded.</td></tr>
{{end}}
</tbody>
</table>
</body>
</html>
`
