package app

import (
	"context"

	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/fetch"
)

type App struct {
	Store evidence.Store
}

func (a *App) Fetch(ctx context.Context, backend string, args []string, url string, version string, isFallback bool) (*evidence.Record, error) {
	return fetch.Fetch(ctx, a.Store, backend, args, url, version, isFallback)
}
