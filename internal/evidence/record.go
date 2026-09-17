package evidence

import (
	"fmt"
	"time"
)

type Record struct {
	ResolvedURL string
	Timestamp   time.Time
	Hash        string
	BackendName string
	Version     string
	IsFallback  bool
	Payload     []byte
}

type Store interface {
	Save(rec *Record) error
	Get(hash string) (*Record, error)
}

type MemoryStore struct {
	Records []*Record
}

func (m *MemoryStore) Save(rec *Record) error {
	m.Records = append(m.Records, rec)
	return nil
}

func (m *MemoryStore) Get(hash string) (*Record, error) {
	for _, rec := range m.Records {
		if rec.Hash == hash {
			return rec, nil
		}
	}
	return nil, fmt.Errorf("record not found")
}
