package evidence

import "time"

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
}

type MemoryStore struct {
	Records []*Record
}

func (m *MemoryStore) Save(rec *Record) error {
	m.Records = append(m.Records, rec)
	return nil
}
