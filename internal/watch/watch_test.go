package watch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/aniklavida/words-on-the-street/internal/evidence"
)

type fixtureFetcher struct {
	store evidence.Store
	sets  [][]*evidence.Record
	calls int
}

func (f *fixtureFetcher) Fetch(_ context.Context, _ Request) ([]*evidence.Record, error) {
	if f.calls >= len(f.sets) {
		return nil, fmt.Errorf("fixture set %d is unavailable", f.calls)
	}
	set := f.sets[f.calls]
	f.calls++
	saved := make([]*evidence.Record, 0, len(set))
	for _, record := range set {
		copyRecord := *record
		copyRecord.Timestamp = time.Now().UTC()
		copyRecord.RecordHash = ""
		copyRecord.PrevRecordHash = ""
		if err := f.store.Save(&copyRecord); err != nil {
			return nil, err
		}
		saved = append(saved, &copyRecord)
	}
	return saved, nil
}

type trackingStore struct {
	*evidence.MemoryStore
	getCalls map[string]int
}

func (s *trackingStore) Get(hash string) (*evidence.Record, error) {
	s.getCalls[hash]++
	return s.MemoryStore.Get(hash)
}

func newWatchRecord(t *testing.T, url string, content []byte, entryID ...string) *evidence.Record {
	t.Helper()
	return &evidence.Record{
		ResolvedURL:    url,
		Timestamp:      time.Now().UTC(),
		Hash:           fmt.Sprintf("%x", sha256.Sum256(content)),
		BackendName:    "fixture",
		BackendVersion: "1.0",
		Version:        "1.0",
		BackendArgs:    entryID,
		Payload:        content,
		RawPayload:     content,
	}
}

func watchFixture() (string, string) {
	return "hacker-news", "golang"
}

func watchURL(t *testing.T, source, query string) string {
	t.Helper()
	recordURL, err := resolveURL(source, query)
	if err != nil {
		t.Fatalf("failed to resolve watch fixture URL: %v", err)
	}
	return recordURL
}

func TestWatch_ReportsNewDeletedAndEditedEntries(t *testing.T) {
	source, query := watchFixture()
	url := watchURL(t, source, query)
	store := evidence.NewMemoryStore()
	initial := []*evidence.Record{
		newWatchRecord(t, url, []byte("old content\n"), "old"),
		newWatchRecord(t, url, []byte("deleted content\n"), "deleted"),
		newWatchRecord(t, url, []byte("original content\n"), "edited"),
	}
	for _, record := range initial {
		if err := store.Save(record); err != nil {
			t.Fatalf("failed to save initial fixture: %v", err)
		}
	}
	fetcher := &fixtureFetcher{store: store, sets: [][]*evidence.Record{
		initial,
		{
			newWatchRecord(t, url, []byte("old content\n"), "old"),
			newWatchRecord(t, url, []byte("updated content\n"), "edited"),
			newWatchRecord(t, url, []byte("new content\n"), "new"),
		},
	}}
	if _, err := Run(context.Background(), store, fetcher, source, query); err != nil {
		t.Fatalf("first watch run failed: %v", err)
	}
	report, err := Run(context.Background(), store, fetcher, source, query)
	if err != nil {
		t.Fatalf("second watch run failed: %v", err)
	}
	if len(report.New) != 1 || report.New[0].EntryID != url+"\x00fixture\x00new" {
		t.Fatalf("new entries = %+v, want new fixture entry", report.New)
	}
	if len(report.Deleted) != 1 || report.Deleted[0].EntryID != url+"\x00fixture\x00deleted" {
		t.Fatalf("deleted entries = %+v, want deleted fixture entry", report.Deleted)
	}
	if len(report.Edited) != 1 || report.Edited[0].EntryID != url+"\x00fixture\x00edited" {
		t.Fatalf("edited entries = %+v, want edited fixture entry", report.Edited)
	}
	if report.Edited[0].PreviousHash == "" || report.Edited[0].CurrentHash == "" || report.Edited[0].Diff == "" {
		t.Fatalf("edited entry lacks hashes or diff: %+v", report.Edited[0])
	}
}

func TestWatch_EditedReportReadsPreviousContentFromStore(t *testing.T) {
	source, query := watchFixture()
	url := watchURL(t, source, query)
	store := &trackingStore{MemoryStore: evidence.NewMemoryStore(), getCalls: make(map[string]int)}
	oldContent := []byte("the previous post content\n")
	old := newWatchRecord(t, url, oldContent)
	if err := store.Save(old); err != nil {
		t.Fatalf("failed to save old post: %v", err)
	}
	oldHash := old.Hash
	fetcher := &fixtureFetcher{store: store, sets: [][]*evidence.Record{{
		newWatchRecord(t, url, []byte("the updated post content\n")),
	}}}

	report, err := Run(context.Background(), store, fetcher, source, query)
	if err != nil {
		t.Fatalf("watch run failed: %v", err)
	}
	if len(report.Edited) != 1 {
		t.Fatalf("edited count = %d, want 1", len(report.Edited))
	}
	if !bytes.Equal(report.Edited[0].PreviousContent, oldContent) {
		t.Errorf("previous content = %q, want %q", report.Edited[0].PreviousContent, oldContent)
	}
	if store.getCalls[oldHash] != 1 {
		t.Errorf("previous record store reads = %d, want 1", store.getCalls[oldHash])
	}
	if fetcher.calls != 1 {
		t.Errorf("fetch calls = %d, want 1", fetcher.calls)
	}
}

func TestWatch_RunReturnsSynchronously(t *testing.T) {
	source, query := watchFixture()
	url := watchURL(t, source, query)
	store := evidence.NewMemoryStore()
	fetcher := &fixtureFetcher{store: store, sets: [][]*evidence.Record{{
		newWatchRecord(t, url, []byte("content\n")),
	}}}

	report, err := Run(context.Background(), store, fetcher, source, query)
	if err != nil {
		t.Fatalf("watch run failed: %v", err)
	}
	if report == nil || fetcher.calls != 1 {
		t.Fatalf("watch did not return after one fetch: report=%+v calls=%d", report, fetcher.calls)
	}
}
