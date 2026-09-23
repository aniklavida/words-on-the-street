package fetch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/aniklavida/words-on-the-street/internal/backend"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
)

// ResolvedRequest is a source-specific request: the URL whose bytes are recorded
// and any extra arguments appended to the backend command after the registry's
// own arguments.
type ResolvedRequest struct {
	URL  string
	Args []string
	// BeforeExecute, when set, runs once immediately before the backend is
	// invoked. It is where a source enforces a rate limit. Resolution stays
	// side-effect free and no bytes are retrieved until this returns.
	BeforeExecute func(context.Context) error
}

// Source turns an opaque query — a search term or an item/thread identifier —
// into a concrete request for one source. Resolution is pure: it constructs a
// URL, it does not retrieve anything, so no bytes can arrive without a record.
type Source struct {
	Name    string
	Resolve func(query string) (ResolvedRequest, error)
	// CredentialArgs, when set, returns extra backend arguments that present a
	// user-supplied session to the backend. They are appended after the
	// resolved arguments and sanitized on the way into the record, so the
	// credential is sent to the platform and never stored. A source with no
	// credential leaves this nil.
	CredentialArgs func() []string
}

var (
	// ErrUnknownSource reports a source name with no registered resolver.
	ErrUnknownSource = errors.New("unknown source")
	// ErrEmptyQuery reports a query that cannot resolve to anything meaningful.
	ErrEmptyQuery = errors.New("query is required")
)

const (
	hnSearchEndpoint        = "https://hn.algolia.com/api/v1/search"
	hnItemEndpoint          = "https://hn.algolia.com/api/v1/items/"
	lobstersSearchEndpoint  = "https://lobste.rs/search"
	lobstersTagEndpoint     = "https://lobste.rs/t/"
	lobstersHottestEndpoint = "https://lobste.rs/hottest.json"
	twitterStatusEndpoint   = "https://x.com/i/status/"
	twitterUserEndpoint     = "https://x.com/"
	twitterSearchEndpoint   = "https://x.com/search"
	linkedinSearchEndpoint  = "https://www.linkedin.com/search/results/content/"
	linkedinCompanyEndpoint = "https://www.linkedin.com/company/"
	linkedinProfileEndpoint = "https://www.linkedin.com/in/"
)

var (
	digitsPattern        = regexp.MustCompile(`^[0-9]+$`)
	lobstersTagPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	twitterHandlePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)
	linkedinSlugPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// Sources returns the shipped source resolvers keyed by name. Every name here
// matches a source in backend.DefaultSources.
//
// Hacker News resolves through its Algolia-backed search API rather than the
// Firebase API: Algolia serves both a query search and a whole thread from a
// single JSON document, so one backend argument array covers the "search term or
// thread identifier" cases without reading the Firebase tree node by node.
//
// Lobsters resolves through the .json suffix its pages already serve: a search, a
// tag feed, or the hottest feed.
//
// Twitter/X has no open endpoint for this use, so a query resolves to a public
// page URL and the user's own already-authenticated session cookie is presented
// by the backend. There is no login here: the user brings a session, the way a
// browser export does. The cookie is registered for redaction and sanitized on
// the way into the record; it is never stored. Live retrieval through this path
// is experimental, since x.com may serve a JavaScript shell that carries no
// data server-side.
//
// LinkedIn resolves public search, company and profile URLs. It requires a
// session cookie the user supplies themselves (LinkedInCookieEnv); resolution
// refuses without one, before any backend runs.
func Sources() map[string]Source {
	return map[string]Source{
		"hacker-news": {Name: "hacker-news", Resolve: resolveHackerNews},
		"lobsters":    {Name: "lobsters", Resolve: resolveLobsters},
		"twitter": {
			Name:    "twitter",
			Resolve: resolveTwitter,
			CredentialArgs: func() []string {
				cookie, ok := TwitterCookie()
				if !ok {
					return nil
				}
				return TwitterCredentialArgs(cookie)
			},
		},
		"linkedin": {Name: "linkedin", Resolve: resolveLinkedIn},
	}
}

// LookupSource returns the resolver registered for the given source name.
func LookupSource(name string) (Source, bool) {
	s, ok := Sources()[name]
	return s, ok
}

func resolveHackerNews(query string) (ResolvedRequest, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return ResolvedRequest{}, ErrEmptyQuery
	}
	// A bare number is an item id, so a specific thread is addressed directly
	// instead of guessed at through a search.
	if digitsPattern.MatchString(q) {
		itemURL := hnItemEndpoint + q
		return ResolvedRequest{URL: itemURL, Args: []string{itemURL}}, nil
	}
	searchURL := hnSearchEndpoint + "?query=" + url.QueryEscape(q)
	return ResolvedRequest{URL: searchURL, Args: []string{searchURL}}, nil
}

func resolveLobsters(query string) (ResolvedRequest, error) {
	q := strings.TrimSpace(query)
	if q == "" || strings.EqualFold(q, "hottest") {
		return ResolvedRequest{URL: lobstersHottestEndpoint, Args: []string{lobstersHottestEndpoint}}, nil
	}
	if strings.HasPrefix(strings.ToLower(q), "tag:") {
		tag := strings.TrimSpace(q[len("tag:"):])
		if tag == "" || !lobstersTagPattern.MatchString(tag) {
			return ResolvedRequest{}, fmt.Errorf("invalid lobsters tag %q", tag)
		}
		tagURL := lobstersTagEndpoint + tag + ".json"
		return ResolvedRequest{URL: tagURL, Args: []string{tagURL}}, nil
	}
	// Lobsters' search action raises ActionController::UnpermittedParameters for
	// any request that carries an explicit format, so the .json search route
	// answers every query with "400 Unpermitted query or form parameter". The
	// site's own search results page is fetched instead; tag and hottest feeds
	// still use their .json endpoints.
	searchURL := lobstersSearchEndpoint + "?q=" + url.QueryEscape(q) + "&what=stories&order=newest"
	return ResolvedRequest{URL: searchURL, Args: []string{searchURL}}, nil
}

// resolveTwitter maps a query to a public x.com page. A bare number is a tweet
// id; "user:<handle>" or "@<handle>" is a profile; anything else is a live
// search. The resolved request carries no credential: the session cookie is
// appended at fetch time, so a resolution alone cannot leak one.
func resolveTwitter(query string) (ResolvedRequest, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return ResolvedRequest{}, ErrEmptyQuery
	}
	if digitsPattern.MatchString(q) {
		statusURL := twitterStatusEndpoint + q
		return ResolvedRequest{URL: statusURL, Args: []string{statusURL}}, nil
	}
	if strings.HasPrefix(strings.ToLower(q), "user:") {
		return twitterHandleRequest(strings.TrimSpace(q[len("user:"):]))
	}
	if strings.HasPrefix(q, "@") {
		return twitterHandleRequest(q[1:])
	}
	searchURL := twitterSearchEndpoint + "?q=" + url.QueryEscape(q) + "&f=live"
	return ResolvedRequest{URL: searchURL, Args: []string{searchURL}}, nil
}

func twitterHandleRequest(handle string) (ResolvedRequest, error) {
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if !twitterHandlePattern.MatchString(handle) {
		return ResolvedRequest{}, fmt.Errorf("invalid twitter handle %q", handle)
	}
	userURL := twitterUserEndpoint + handle
	return ResolvedRequest{URL: userURL, Args: []string{userURL}}, nil
}

// resolveLinkedIn turns a query into a LinkedIn URL fetched with the user's own
// session cookie. It never logs the cookie; the cookie travels only in the
// backend argument array, where evidence.SanitizeArgs redacts it before the
// record is written. A fetch is refused, before any backend runs, when no cookie
// is configured.
func resolveLinkedIn(query string) (ResolvedRequest, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return ResolvedRequest{}, ErrEmptyQuery
	}

	cookie, err := LinkedInCookie()
	if err != nil {
		return ResolvedRequest{}, err
	}

	var targetURL string
	switch {
	case strings.HasPrefix(strings.ToLower(q), "company:"):
		slug := strings.TrimSpace(q[len("company:"):])
		if !linkedinSlugPattern.MatchString(slug) {
			return ResolvedRequest{}, fmt.Errorf("invalid linkedin company %q", slug)
		}
		targetURL = linkedinCompanyEndpoint + slug + "/posts/"
	case strings.HasPrefix(strings.ToLower(q), "profile:"):
		slug := strings.TrimSpace(q[len("profile:"):])
		if !linkedinSlugPattern.MatchString(slug) {
			return ResolvedRequest{}, fmt.Errorf("invalid linkedin profile %q", slug)
		}
		targetURL = linkedinProfileEndpoint + slug + "/"
	default:
		targetURL = linkedinSearchEndpoint + "?keywords=" + url.QueryEscape(q)
	}

	// The cookie is a single header argument. It is redacted by
	// evidence.SanitizeArgs before any record is written, and never logged here.
	args := []string{"-H", "Cookie: " + cookie, targetURL}
	return ResolvedRequest{URL: targetURL, Args: args, BeforeExecute: waitForLinkedInRateLimit}, nil
}

// FetchQuery resolves a query for a named source and fetches it through the
// registry, recording evidence in the same operation. An unknown source or a
// query that does not resolve is refused before any bytes are retrieved.
func FetchQuery(ctx context.Context, store evidence.Store, reg *backend.Registry, source string, query string) (string, error) {
	src, ok := LookupSource(source)
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownSource, source)
	}
	req, err := src.Resolve(query)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %s query %q: %w", source, query, err)
	}

	// A source's pre-fetch hook enforces anything that must happen before bytes
	// are retrieved, such as a rate limit. It runs before the backend can start.
	// This is LinkedIn's mechanism: the cookie is already baked into req.Args by
	// Resolve, so only the rate-limit wait happens here.
	if req.BeforeExecute != nil {
		if err := req.BeforeExecute(ctx); err != nil {
			return "", fmt.Errorf("failed to prepare %s fetch: %w", source, err)
		}
	}

	args := append([]string(nil), req.Args...)
	if src.CredentialArgs != nil {
		// Twitter's mechanism: the credential is constructed and appended here,
		// after its own rate limit is applied, rather than being baked into
		// Resolve's output. The credential arguments themselves are sanitized by
		// FetchSource, so nothing before the record boundary sees the cookie.
		if err := credentialRateLimiter(source).Wait(ctx); err != nil {
			return "", fmt.Errorf("rate limit for %s: %w", source, err)
		}
		args = append(args, src.CredentialArgs()...)
	}
	return FetchSource(ctx, store, reg, source, req.URL, args)
}
