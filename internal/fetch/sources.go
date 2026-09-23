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
}

// Source turns an opaque query — a search term or an item/thread identifier —
// into a concrete request for one source. Resolution is pure: it constructs a
// URL, it does not retrieve anything, so no bytes can arrive without a record.
type Source struct {
	Name    string
	Resolve func(query string) (ResolvedRequest, error)
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
	lobstersSearchEndpoint  = "https://lobste.rs/search.json"
	lobstersTagEndpoint     = "https://lobste.rs/t/"
	lobstersHottestEndpoint = "https://lobste.rs/hottest.json"
)

var (
	digitsPattern      = regexp.MustCompile(`^[0-9]+$`)
	lobstersTagPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// Sources returns the shipped source resolvers keyed by name. Every name here
// matches a source in backend.DefaultSources, and every endpoint is a public one
// that needs no credential.
//
// Hacker News resolves through its Algolia-backed search API rather than the
// Firebase API: Algolia serves both a query search and a whole thread from a
// single JSON document, so one backend argument array covers the "search term or
// thread identifier" cases without reading the Firebase tree node by node.
//
// Lobsters resolves through the .json suffix its pages already serve: a search, a
// tag feed, or the hottest feed.
func Sources() map[string]Source {
	return map[string]Source{
		"hacker-news": {Name: "hacker-news", Resolve: resolveHackerNews},
		"lobsters":    {Name: "lobsters", Resolve: resolveLobsters},
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
		return ResolvedRequest{URL: hnItemEndpoint + q}, nil
	}
	return ResolvedRequest{URL: hnSearchEndpoint + "?query=" + url.QueryEscape(q)}, nil
}

func resolveLobsters(query string) (ResolvedRequest, error) {
	q := strings.TrimSpace(query)
	if q == "" || strings.EqualFold(q, "hottest") {
		return ResolvedRequest{URL: lobstersHottestEndpoint}, nil
	}
	if strings.HasPrefix(strings.ToLower(q), "tag:") {
		tag := strings.TrimSpace(q[len("tag:"):])
		if tag == "" || !lobstersTagPattern.MatchString(tag) {
			return ResolvedRequest{}, fmt.Errorf("invalid lobsters tag %q", tag)
		}
		return ResolvedRequest{URL: lobstersTagEndpoint + tag + ".json"}, nil
	}
	return ResolvedRequest{URL: lobstersSearchEndpoint + "?q=" + url.QueryEscape(q)}, nil
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
	return FetchSource(ctx, store, reg, source, req.URL, req.Args)
}
