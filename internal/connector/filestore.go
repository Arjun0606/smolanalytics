package connector

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// FileStore persists the held aggregate and its watermark beside the event log.
//
// MemStore was fine for tests and wrong for a running instance: the watermark lives in memory, so
// every restart re-reads the whole 90-day window against someone else's API. A deploy would cost
// them a full backfill, and a crash loop would cost them their rate limit. The whole reason this
// connector reads AGGREGATES rather than raw events is to be cheap against a vendor; throwing the
// watermark away on restart gives that back.
//
// Same shape and discipline as the other sidecars (flag, acted, alias): one JSON file, atomic
// rename, and a corrupt file starts empty rather than taking the instance down — it holds a cache
// that can be refetched, not data anyone can lose.
//
// IT NEVER SEES A CREDENTIAL. Save takes buckets and a watermark; the key stays inside the
// Fetcher for the life of the process, which is the contract the Store interface documents.
type FileStore struct {
	mu   sync.Mutex
	path string
	data map[string]fileEntry // keyed vendor+"/"+project
}

type fileEntry struct {
	Buckets   []Bucket  `json:"buckets"`
	Watermark time.Time `json:"watermark"`
}

func OpenFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, data: map[string]fileEntry{}}
	if path == "" {
		return s, nil // in-memory, the demo convention every sidecar follows
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		s.data = map[string]fileEntry{} // refetchable cache: start clean, keep serving
	}
	return s, nil
}

func key(vendor, project string) string { return vendor + "/" + project }

func (s *FileStore) Load(_ context.Context, vendor, project string) ([]Bucket, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.data[key(vendor, project)]
	return e.Buckets, e.Watermark, nil
}

func (s *FileStore) Save(_ context.Context, vendor, project string, buckets []Bucket, watermark time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key(vendor, project)] = fileEntry{Buckets: buckets, Watermark: watermark}
	return s.persist()
}

func (s *FileStore) persist() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
