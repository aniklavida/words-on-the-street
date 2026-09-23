package app

import (
	"context"

	"github.com/aniklavida/words-on-the-street/internal/backend"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/fetch"
	"github.com/aniklavida/words-on-the-street/internal/verify"
)

type App struct {
	Store    evidence.Store
	Registry *backend.Registry
}

func (a *App) Fetch(ctx context.Context, backendCmd string, args []string, url string, version string, isFallback bool, opts ...fetch.Option) (string, error) {
	return fetch.Fetch(ctx, a.Store, backendCmd, args, url, version, isFallback, opts...)
}

func (a *App) FetchSource(ctx context.Context, source string, url string, args []string, opts ...fetch.Option) (string, error) {
	reg := a.Registry
	if reg == nil {
		reg = backend.DefaultRegistry()
	}
	return fetch.FetchSource(ctx, a.Store, reg, source, url, args, opts...)
}

// FetchQuery resolves a query for a named source and fetches it through the
// registry, recording evidence in the same operation.
func (a *App) FetchQuery(ctx context.Context, source string, query string, opts ...fetch.Option) (string, error) {
	reg := a.Registry
	if reg == nil {
		reg = backend.DefaultRegistry()
	}
	return fetch.FetchQuery(ctx, a.Store, reg, source, query, opts...)
}

// Verify re-fetches the recorded entry named by recordHash and reports whether
// the source is identical, changed, or gone.
func (a *App) Verify(ctx context.Context, recordHash string) (*verify.Result, error) {
	return verify.Verify(ctx, a.Store, verify.CommandRefetcher{}, recordHash)
}
