package watch

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/backend"
	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/fetch"
	"github.com/aniklavida/words-on-the-street/internal/verify"
)

type Request struct {
	Source string
	Query  string
}

type Fetcher interface {
	Fetch(context.Context, Request) ([]*evidence.Record, error)
}

type FetchFunc func(context.Context, Request) ([]*evidence.Record, error)

func (f FetchFunc) Fetch(ctx context.Context, request Request) ([]*evidence.Record, error) {
	return f(ctx, request)
}

type QueryFetcher struct {
	Store    evidence.Store
	Registry *backend.Registry
}

func (f QueryFetcher) Fetch(ctx context.Context, request Request) ([]*evidence.Record, error) {
	if f.Store == nil {
		return nil, fmt.Errorf("store is required")
	}
	hash, err := fetch.FetchQuery(ctx, f.Store, f.Registry, request.Source, request.Query)
	if err != nil {
		return nil, err
	}
	record, err := f.Store.Get(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to load fetched evidence %s: %w", hash, err)
	}
	return []*evidence.Record{record}, nil
}

type Change struct {
	URL             string `json:"url"`
	EntryID         string `json:"entry_id"`
	PreviousHash    string `json:"previous_hash,omitempty"`
	CurrentHash     string `json:"current_hash,omitempty"`
	PreviousContent []byte `json:"previous_content,omitempty"`
	CurrentContent  []byte `json:"current_content,omitempty"`
	Diff            string `json:"diff,omitempty"`
}

type Report struct {
	Source      string    `json:"source"`
	Query       string    `json:"query"`
	ResolvedURL string    `json:"resolved_url"`
	New         []Change  `json:"new"`
	Deleted     []Change  `json:"deleted"`
	Edited      []Change  `json:"edited"`
	CheckedAt   time.Time `json:"checked_at"`
}

func Run(ctx context.Context, store evidence.Store, fetcher Fetcher, source, query string) (*Report, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if fetcher == nil {
		return nil, fmt.Errorf("fetcher is required")
	}

	resolvedURL, err := resolveURL(source, query)
	if err != nil {
		return nil, err
	}
	lister, ok := store.(evidence.Lister)
	if !ok {
		return nil, fmt.Errorf("store does not support listing records")
	}
	priorRecords, err := lister.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list prior evidence: %w", err)
	}
	prior := latestEntries(priorRecords, resolvedURL)

	currentRecords, err := fetcher.Fetch(ctx, Request{Source: source, Query: query})
	if err != nil {
		return nil, err
	}
	current := latestEntries(currentRecords, resolvedURL)

	report := &Report{
		Source:      source,
		Query:       query,
		ResolvedURL: resolvedURL,
		New:         make([]Change, 0),
		Deleted:     make([]Change, 0),
		Edited:      make([]Change, 0),
		CheckedAt:   time.Now().UTC(),
	}

	for key, currentRecord := range current {
		previousRecord, existed := prior[key]
		if !existed {
			report.New = append(report.New, Change{
				URL:            currentRecord.ResolvedURL,
				EntryID:        key,
				CurrentHash:    currentRecord.Hash,
				CurrentContent: append([]byte(nil), currentRecord.RawBytes()...),
			})
			continue
		}

		previousStored, err := store.Get(previousRecord.Hash)
		if err != nil {
			return nil, fmt.Errorf("failed to load previous evidence %s: %w", previousRecord.Hash, err)
		}
		comparison, err := verify.Compare(previousStored, currentRecord)
		if err != nil {
			return nil, fmt.Errorf("failed to compare evidence %s: %w", previousRecord.Hash, err)
		}
		if comparison.State == verify.Changed {
			report.Edited = append(report.Edited, Change{
				URL:             currentRecord.ResolvedURL,
				EntryID:         key,
				PreviousHash:    previousStored.Hash,
				CurrentHash:     currentRecord.Hash,
				PreviousContent: append([]byte(nil), previousStored.RawBytes()...),
				CurrentContent:  append([]byte(nil), currentRecord.RawBytes()...),
				Diff:            comparison.Diff,
			})
		}
	}

	for key, previousRecord := range prior {
		if _, exists := current[key]; !exists {
			report.Deleted = append(report.Deleted, Change{
				URL:          previousRecord.ResolvedURL,
				EntryID:      key,
				PreviousHash: previousRecord.Hash,
			})
		}
	}

	sort.Slice(report.New, func(i, j int) bool { return report.New[i].URL < report.New[j].URL })
	sort.Slice(report.Deleted, func(i, j int) bool { return report.Deleted[i].URL < report.Deleted[j].URL })
	sort.Slice(report.Edited, func(i, j int) bool { return report.Edited[i].URL < report.Edited[j].URL })
	return report, nil
}

func resolveURL(source, query string) (string, error) {
	src, ok := fetch.LookupSource(source)
	if !ok {
		return "", fmt.Errorf("%w: %q", fetch.ErrUnknownSource, source)
	}
	request, err := src.Resolve(query)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %s query %q: %w", source, query, err)
	}
	return request.URL, nil
}

func latestEntries(records []*evidence.Record, resolvedURL string) map[string]*evidence.Record {
	latest := make(map[string]*evidence.Record)
	for _, record := range records {
		if record == nil || record.ResolvedURL != resolvedURL || isObservation(record) {
			continue
		}
		key := entryKey(record)
		previous, exists := latest[key]
		if !exists || !record.Timestamp.Before(previous.Timestamp) {
			latest[key] = record
		}
	}
	return latest
}

func entryKey(record *evidence.Record) string {
	return record.ResolvedURL + "\x00" + record.BackendName + "\x00" + strings.Join(record.BackendArgs, "\x00")
}

func isObservation(record *evidence.Record) bool {
	return strings.HasPrefix(record.BackendStatus, "verified-")
}

func FormatReport(report *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "source: %s\n", report.Source)
	fmt.Fprintf(&b, "query: %s\n", report.Query)
	fmt.Fprintf(&b, "resolved_url: %s\n", report.ResolvedURL)
	writeChanges(&b, "new", report.New, false)
	writeChanges(&b, "deleted", report.Deleted, true)
	writeChanges(&b, "edited", report.Edited, false)
	return b.String()
}

func writeChanges(b *strings.Builder, name string, changes []Change, deleted bool) {
	fmt.Fprintf(b, "%s: %d\n", name, len(changes))
	for _, change := range changes {
		fmt.Fprintf(b, "  - %s (%s)\n", change.URL, change.CurrentHashOrPrevious(deleted))
		if !deleted {
			if change.Diff != "" {
				fmt.Fprintf(b, "diff:\n%s", change.Diff)
			}
			if change.PreviousContent != nil {
				fmt.Fprintf(b, "previous_content:\n%s", change.PreviousContent)
			}
			fmt.Fprintf(b, "current_content:\n%s", change.CurrentContent)
		}
	}
}

func (c Change) CurrentHashOrPrevious(deleted bool) string {
	if deleted {
		return c.PreviousHash
	}
	return c.CurrentHash
}
