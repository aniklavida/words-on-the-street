package evidence

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store defines the interface for recording and retrieving fetch evidence.
type Store interface {
	Save(rec *Record) error
	Get(hash string) (*Record, error)
}

// RawStore provides access to content-addressed raw payloads and integrity checks.
type RawStore interface {
	Store
	GetRaw(hash string) ([]byte, error)
	VerifyIntegrity() error
}

// Lister enumerates the records already in the store, oldest first, without
// mutating anything. It is a read-only capability kept separate from Store so a
// caller that only fetches need not provide it. The local dashboard depends on
// it to show recent fetches and verification observations; it never fetches,
// verifies, or writes.
type Lister interface {
	List() ([]*Record, error)
}

// FileStore is an append-only, content-addressed on-disk store for evidence records and payloads.
type FileStore struct {
	dir      string
	blobsDir string
	recsDir  string
	ledger   string
	mu       sync.Mutex
}

// NewFileStore creates and initializes a FileStore at the given directory.
func NewFileStore(dir string) (*FileStore, error) {
	blobsDir := filepath.Join(dir, "blobs")
	recsDir := filepath.Join(dir, "records")
	ledger := filepath.Join(dir, "ledger.jsonl")

	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create blobs directory: %w", err)
	}
	if err := os.MkdirAll(recsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create records directory: %w", err)
	}

	return &FileStore{
		dir:      dir,
		blobsDir: blobsDir,
		recsDir:  recsDir,
		ledger:   ledger,
	}, nil
}

// Dir returns the root directory of the FileStore.
func (s *FileStore) Dir() string {
	return s.dir
}

// DefaultDir returns the default filesystem location for the evidence store.
func DefaultDir() string {
	if dir := os.Getenv("WORDS_ON_THE_STREET_STORE"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".evidence"
	}
	return filepath.Join(home, ".words-on-the-street", "evidence")
}

// DefaultStore returns a FileStore initialized at the default directory.
func DefaultStore() (*FileStore, error) {
	return NewFileStore(DefaultDir())
}

// Save writes a record and its raw payload to the append-only store.
// Silently rewriting existing records is strictly refused.
func (s *FileStore) Save(rec *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec == nil {
		return fmt.Errorf("record is required")
	}

	raw := rec.RawBytes()
	if len(raw) == 0 {
		return fmt.Errorf("%w: payload cannot be empty", ErrMissingField)
	}

	// Content hash of raw bytes as received
	actualHash := fmt.Sprintf("%x", sha256.Sum256(raw))
	if rec.Hash != actualHash {
		return fmt.Errorf("%w: record hash %s != raw bytes hash %s", ErrPayloadMismatch, rec.Hash, actualHash)
	}

	if err := rec.Validate(); err != nil {
		return err
	}

	blobPath := filepath.Join(s.blobsDir, rec.Hash)
	recPath := filepath.Join(s.recsDir, rec.Hash+".json")

	// Append-only check: refuse to silently rewrite an existing record with different metadata
	if existingData, err := os.ReadFile(recPath); err == nil {
		var existingRec Record
		if err := json.Unmarshal(existingData, &existingRec); err == nil {
			version := rec.BackendVersion
			if version == "" {
				version = rec.Version
			}
			existingVersion := existingRec.BackendVersion
			if existingVersion == "" {
				existingVersion = existingRec.Version
			}
			if existingRec.ResolvedURL != rec.ResolvedURL ||
				existingRec.BackendName != rec.BackendName ||
				existingVersion != version ||
				existingRec.IsFallback != rec.IsFallback ||
				!bytes.Equal(existingRec.RawBytes(), raw) {
				return fmt.Errorf("%w: record for hash %s already exists with different data", ErrAppendOnlyViolation, rec.Hash)
			}
		}
	}

	// Read last entry in ledger to determine PrevRecordHash
	prevHash := ""
	if entries, err := s.readLedgerEntries(); err == nil && len(entries) > 0 {
		prevHash = entries[len(entries)-1].RecordHash
	}
	rec.PrevRecordHash = prevHash
	rec.RecordHash = rec.ComputeRecordHash()

	// Content-addressed raw payload: write if not already present
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		if err := writeReadOnlyFile(blobPath, raw); err != nil {
			return fmt.Errorf("failed to write payload blob: %w", err)
		}
	} else {
		// If blob already exists, verify its integrity
		existingBlob, err := os.ReadFile(blobPath)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(existingBlob)) != rec.Hash {
			return fmt.Errorf("%w: existing blob corrupted for %s", ErrTamperingDetected, rec.Hash)
		}
	}

	// Record metadata: write if not already present
	if _, err := os.Stat(recPath); os.IsNotExist(err) {
		recData, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal record: %w", err)
		}
		if err := writeReadOnlyFile(recPath, recData); err != nil {
			return fmt.Errorf("failed to write record file: %w", err)
		}
	}

	// Append-only ledger: every fetch is recorded in the ledger
	f, err := os.OpenFile(s.ledger, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open ledger: %w", err)
	}
	defer f.Close()

	recLine, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("failed to marshal ledger entry: %w", err)
	}
	if _, err := f.Write(append(recLine, '\n')); err != nil {
		return fmt.Errorf("failed to append to ledger: %w", err)
	}

	return nil
}

// Get retrieves a record by its content hash, verifying payload and record integrity.
func (s *FileStore) Get(hash string) (*Record, error) {
	recPath := filepath.Join(s.recsDir, hash+".json")
	blobPath := filepath.Join(s.blobsDir, hash)

	recData, err := os.ReadFile(recPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
		}
		return nil, fmt.Errorf("failed to read record: %w", err)
	}

	var rec Record
	if err := json.Unmarshal(recData, &rec); err != nil {
		return nil, fmt.Errorf("%w: invalid record json: %v", ErrTamperingDetected, err)
	}

	blobData, err := os.ReadFile(blobPath)
	if err != nil {
		return nil, fmt.Errorf("%w: payload missing: %v", ErrTamperingDetected, err)
	}

	// Verify content-addressed payload hash
	actualBlobHash := fmt.Sprintf("%x", sha256.Sum256(blobData))
	if actualBlobHash != rec.Hash {
		return nil, fmt.Errorf("%w: payload hash mismatch for %s: got %s", ErrTamperingDetected, rec.Hash, actualBlobHash)
	}

	// Verify record hash integrity
	expectedRecordHash := rec.ComputeRecordHash()
	if rec.RecordHash != expectedRecordHash {
		return nil, fmt.Errorf("%w: record hash mismatch for %s", ErrTamperingDetected, rec.Hash)
	}

	rec.Payload = blobData
	rec.RawPayload = blobData
	if rec.BackendVersion == "" {
		rec.BackendVersion = rec.Version
	}
	if rec.Version == "" {
		rec.Version = rec.BackendVersion
	}

	return &rec, nil
}

// List returns every recorded entry in append order (oldest first). It is a
// pure read: it never writes, verifies, or re-fetches anything.
func (s *FileStore) List() ([]*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.readLedgerEntries()
	if err != nil {
		return nil, fmt.Errorf("failed to list records: %w", err)
	}
	return entries, nil
}

// GetRaw retrieves the content-addressed raw payload bytes, verifying integrity.
func (s *FileStore) GetRaw(hash string) ([]byte, error) {
	blobPath := filepath.Join(s.blobsDir, hash)
	data, err := os.ReadFile(blobPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
		}
		return nil, fmt.Errorf("failed to read blob: %w", err)
	}

	actualHash := fmt.Sprintf("%x", sha256.Sum256(data))
	if actualHash != hash {
		return nil, fmt.Errorf("%w: raw payload hash mismatch for %s: got %s", ErrTamperingDetected, hash, actualHash)
	}

	return data, nil
}

// VerifyIntegrity checks the entire store for any tampering or corruption.
func (s *FileStore) VerifyIntegrity() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.readLedgerEntries()
	if err != nil {
		return fmt.Errorf("%w: ledger read error: %v", ErrTamperingDetected, err)
	}

	var prevHash string
	for i, entry := range entries {
		if entry.PrevRecordHash != prevHash {
			return fmt.Errorf("%w: ledger chain broken at index %d: expected prev %q, got %q",
				ErrTamperingDetected, i, prevHash, entry.PrevRecordHash)
		}

		expectedHash := entry.ComputeRecordHash()
		if entry.RecordHash != expectedHash {
			return fmt.Errorf("%w: ledger entry hash mismatch at index %d", ErrTamperingDetected, i)
		}

		// Verify on-disk record
		recPath := filepath.Join(s.recsDir, entry.Hash+".json")
		recBytes, err := os.ReadFile(recPath)
		if err != nil {
			return fmt.Errorf("%w: record file missing for %s", ErrTamperingDetected, entry.Hash)
		}
		var onDiskRec Record
		if err := json.Unmarshal(recBytes, &onDiskRec); err != nil {
			return fmt.Errorf("%w: record file corrupted for %s", ErrTamperingDetected, entry.Hash)
		}
		if onDiskRec.ComputeRecordHash() != entry.RecordHash {
			return fmt.Errorf("%w: on-disk record tampered for %s", ErrTamperingDetected, entry.Hash)
		}

		// Verify on-disk blob
		blobPath := filepath.Join(s.blobsDir, entry.Hash)
		blobBytes, err := os.ReadFile(blobPath)
		if err != nil {
			return fmt.Errorf("%w: blob missing for %s", ErrTamperingDetected, entry.Hash)
		}
		actualBlobHash := fmt.Sprintf("%x", sha256.Sum256(blobBytes))
		if actualBlobHash != entry.Hash {
			return fmt.Errorf("%w: blob tampered for %s: expected %s, got %s",
				ErrTamperingDetected, entry.Hash, entry.Hash, actualBlobHash)
		}

		prevHash = entry.RecordHash
	}

	// Verify all blobs in directory match their hash
	entriesInBlobs, err := os.ReadDir(s.blobsDir)
	if err == nil {
		for _, e := range entriesInBlobs {
			if e.IsDir() {
				continue
			}
			blobPath := filepath.Join(s.blobsDir, e.Name())
			data, err := os.ReadFile(blobPath)
			if err != nil {
				return fmt.Errorf("%w: failed to read blob %s", ErrTamperingDetected, e.Name())
			}
			actual := fmt.Sprintf("%x", sha256.Sum256(data))
			if actual != e.Name() {
				return fmt.Errorf("%w: blob %s corrupted: hash is %s", ErrTamperingDetected, e.Name(), actual)
			}
		}
	}

	return nil
}

func (s *FileStore) readLedgerEntries() ([]*Record, error) {
	if _, err := os.Stat(s.ledger); os.IsNotExist(err) {
		return nil, nil
	}

	f, err := os.Open(s.ledger)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []*Record
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("ledger line parse error: %w", err)
		}
		entries = append(entries, &rec)
	}

	return entries, scanner.Err()
}

func writeReadOnlyFile(path string, data []byte) error {
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0444); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// MemoryStore is an in-memory implementation of the append-only evidence Store.
type MemoryStore struct {
	mu      sync.RWMutex
	records map[string]*Record
	blobs   map[string][]byte
	chain   []string
}

// NewMemoryStore constructs a new MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		records: make(map[string]*Record),
		blobs:   make(map[string][]byte),
	}
}

// Save records evidence in memory. Overwriting existing records is refused.
func (m *MemoryStore) Save(rec *Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec == nil {
		return fmt.Errorf("record is required")
	}

	raw := rec.RawBytes()
	if len(raw) == 0 {
		return fmt.Errorf("%w: payload cannot be empty", ErrMissingField)
	}

	actualHash := fmt.Sprintf("%x", sha256.Sum256(raw))
	if rec.Hash != actualHash {
		return fmt.Errorf("%w: record hash %s != raw bytes hash %s", ErrPayloadMismatch, rec.Hash, actualHash)
	}

	if err := rec.Validate(); err != nil {
		return err
	}

	if existing, exists := m.records[rec.Hash]; exists {
		version := rec.BackendVersion
		if version == "" {
			version = rec.Version
		}
		existingVersion := existing.BackendVersion
		if existingVersion == "" {
			existingVersion = existing.Version
		}
		if existing.ResolvedURL != rec.ResolvedURL ||
			existing.BackendName != rec.BackendName ||
			existingVersion != version ||
			existing.IsFallback != rec.IsFallback ||
			!bytes.Equal(existing.RawBytes(), raw) {
			return fmt.Errorf("%w: record for hash %s already exists with different data", ErrAppendOnlyViolation, rec.Hash)
		}
	}

	prevHash := ""
	if len(m.chain) > 0 {
		lastHash := m.chain[len(m.chain)-1]
		prevHash = m.records[lastHash].RecordHash
	}
	rec.PrevRecordHash = prevHash
	rec.RecordHash = rec.ComputeRecordHash()

	// Clone record to ensure immutability
	recCopy := *rec
	if len(rec.BackendArgs) > 0 {
		recCopy.BackendArgs = make([]string, len(rec.BackendArgs))
		copy(recCopy.BackendArgs, rec.BackendArgs)
	}
	if rec.Normalisation != nil {
		normCopy := *rec.Normalisation
		if len(rec.Normalisation.Payload) > 0 {
			normCopy.Payload = make([]byte, len(rec.Normalisation.Payload))
			copy(normCopy.Payload, rec.Normalisation.Payload)
		}
		recCopy.Normalisation = &normCopy
	}
	if len(rec.MissingBackends) > 0 {
		recCopy.MissingBackends = make([]string, len(rec.MissingBackends))
		copy(recCopy.MissingBackends, rec.MissingBackends)
	}
	if len(rec.BackendAttempts) > 0 {
		recCopy.BackendAttempts = make([]BackendAttempt, len(rec.BackendAttempts))
		copy(recCopy.BackendAttempts, rec.BackendAttempts)
	}

	rawCopy := make([]byte, len(raw))
	copy(rawCopy, raw)
	recCopy.Payload = rawCopy
	recCopy.RawPayload = rawCopy

	m.records[rec.Hash] = &recCopy
	m.blobs[rec.Hash] = rawCopy
	m.chain = append(m.chain, rec.Hash)

	return nil
}

// Get retrieves a record from memory, verifying tamper integrity.
func (m *MemoryStore) Get(hash string) (*Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, exists := m.records[hash]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
	}

	blob, exists := m.blobs[hash]
	if !exists {
		return nil, fmt.Errorf("%w: payload missing for %s", ErrTamperingDetected, hash)
	}

	actualHash := fmt.Sprintf("%x", sha256.Sum256(blob))
	if actualHash != rec.Hash {
		return nil, fmt.Errorf("%w: payload hash mismatch for %s", ErrTamperingDetected, hash)
	}

	if rec.RecordHash != rec.ComputeRecordHash() {
		return nil, fmt.Errorf("%w: record hash mismatch for %s", ErrTamperingDetected, hash)
	}

	res := *rec
	res.Payload = blob
	res.RawPayload = blob
	if res.BackendVersion == "" {
		res.BackendVersion = res.Version
	}
	if res.Version == "" {
		res.Version = res.BackendVersion
	}
	return &res, nil
}

// List returns every recorded entry in append order (oldest first). It is a
// pure read: it never writes, verifies, or re-fetches anything.
func (m *MemoryStore) List() ([]*Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*Record, 0, len(m.chain))
	for _, hash := range m.chain {
		rec, ok := m.records[hash]
		if !ok {
			continue
		}
		cp := *rec
		out = append(out, &cp)
	}
	return out, nil
}

// GetRaw retrieves raw bytes from memory.
func (m *MemoryStore) GetRaw(hash string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	blob, exists := m.blobs[hash]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(blob))
	if actual != hash {
		return nil, fmt.Errorf("%w: payload hash mismatch for %s", ErrTamperingDetected, hash)
	}
	res := make([]byte, len(blob))
	copy(res, blob)
	return res, nil
}

// VerifyIntegrity checks in-memory store integrity.
func (m *MemoryStore) VerifyIntegrity() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var prevHash string
	for _, hash := range m.chain {
		rec, ok := m.records[hash]
		if !ok {
			return fmt.Errorf("%w: missing record in chain %s", ErrTamperingDetected, hash)
		}
		if rec.PrevRecordHash != prevHash {
			return fmt.Errorf("%w: chain broken at %s", ErrTamperingDetected, hash)
		}
		if rec.RecordHash != rec.ComputeRecordHash() {
			return fmt.Errorf("%w: record hash mismatch for %s", ErrTamperingDetected, hash)
		}
		blob, ok := m.blobs[hash]
		if !ok {
			return fmt.Errorf("%w: payload missing for %s", ErrTamperingDetected, hash)
		}
		if fmt.Sprintf("%x", sha256.Sum256(blob)) != hash {
			return fmt.Errorf("%w: payload hash mismatch for %s", ErrTamperingDetected, hash)
		}
		prevHash = rec.RecordHash
	}
	return nil
}
