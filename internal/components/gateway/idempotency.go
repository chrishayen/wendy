package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"pacp/internal/contracts"
)

type idempotencyStore struct {
	mu           sync.Mutex
	records      map[string]invokeRecord
	snapshotPath string
}

type idempotencySnapshotFile struct {
	Version int                                  `json:"version"`
	Records map[string]idempotencySnapshotRecord `json:"records"`
}

type idempotencySnapshotRecord struct {
	Fingerprint string                       `json:"fingerprint"`
	Response    contracts.InvokeToolResponse `json:"response"`
	Links       map[string]any               `json:"links"`
}

func newPersistentIdempotencyStore(path string) (*idempotencyStore, error) {
	store := &idempotencyStore{
		records:      map[string]invokeRecord{},
		snapshotPath: path,
	}
	if path == "" {
		return store, nil
	}
	if err := store.loadSnapshot(path); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *idempotencyStore) healthDetails() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"store_backend": backendLabel(s.snapshotPath),
		"record_count":  len(s.records),
	}
}

func (s *idempotencyStore) replay(key, fingerprint string) (invokeRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[key]
	if !ok || record.fingerprint != fingerprint {
		return invokeRecord{}, false
	}
	return record, true
}

func (s *idempotencyStore) hasConflict(key, fingerprint string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[key]
	return ok && record.fingerprint != fingerprint
}

func (s *idempotencyStore) store(key, fingerprint string, response contracts.InvokeToolResponse, links map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[key] = invokeRecord{fingerprint: fingerprint, response: response, links: cloneLinks(links)}
	return s.saveLocked()
}

func (s *idempotencyStore) loadSnapshot(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var snapshot idempotencySnapshotFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	if snapshot.Version != 1 {
		return fmt.Errorf("unsupported gateway idempotency snapshot version %d", snapshot.Version)
	}
	for key, record := range snapshot.Records {
		s.records[key] = invokeRecord{
			fingerprint: record.Fingerprint,
			response:    record.Response,
			links:       cloneLinks(record.Links),
		}
	}
	return nil
}

func (s *idempotencyStore) saveLocked() error {
	if s.snapshotPath == "" {
		return nil
	}
	records := map[string]idempotencySnapshotRecord{}
	for key, record := range s.records {
		records[key] = idempotencySnapshotRecord{
			Fingerprint: record.fingerprint,
			Response:    record.response,
			Links:       cloneLinks(record.links),
		}
	}
	data, err := json.MarshalIndent(idempotencySnapshotFile{Version: 1, Records: records}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.snapshotPath), 0o700); err != nil {
		return err
	}
	tmp := s.snapshotPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.snapshotPath)
}

func cloneLinks(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func backendLabel(path string) string {
	if path == "" {
		return "memory"
	}
	return "file_snapshot"
}
