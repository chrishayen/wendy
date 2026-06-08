package noderegistry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"pacp/internal/contracts"
)

var (
	ErrNotFound      = errors.New("node not found")
	ErrValidation    = errors.New("validation failed")
	ErrInvalidCursor = errors.New("invalid cursor")
	ErrNotRunnable   = errors.New("node is not runnable")
)

const defaultStaleAfter = 5 * time.Minute

type Store struct {
	mu         sync.RWMutex
	now        func() time.Time
	path       string
	staleAfter time.Duration
	records    map[string]contracts.NodeRecord
}

type ListOptions struct {
	Cursor string
	Limit  int
}

type snapshot struct {
	Records []contracts.NodeRecord `json:"records"`
}

func NewStore() *Store {
	return &Store{
		now:        time.Now,
		staleAfter: defaultStaleAfter,
		records:    map[string]contracts.NodeRecord{},
	}
}

func NewPersistentStore(path string) (*Store, error) {
	store := NewStore()
	store.path = path
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, nil
		}
		return nil, err
	}
	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	for _, record := range snap.Records {
		if record.NodeID == "" {
			continue
		}
		store.records[record.NodeID] = normalizeRecord(record)
	}
	return store, nil
}

func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now == nil {
		now = time.Now
	}
	s.now = now
}

func (s *Store) SetStaleAfter(duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if duration <= 0 {
		duration = defaultStaleAfter
	}
	s.staleAfter = duration
}

func (s *Store) Register(req contracts.RegisterNodeRequest) (contracts.NodeRecord, error) {
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		return contracts.NodeRecord{}, fmt.Errorf("%w: node_id is required", ErrValidation)
	}
	nodeURL := strings.TrimRight(strings.TrimSpace(req.URL), "/")
	if nodeURL == "" {
		return contracts.NodeRecord{}, fmt.Errorf("%w: url is required", ErrValidation)
	}
	if !contracts.ValidHTTPBaseURL(nodeURL) {
		return contracts.NodeRecord{}, fmt.Errorf("%w: url must be an absolute http or https URL without query or fragment", ErrValidation)
	}
	trustState := normalizeTrustState(req.TrustState)
	if trustState == "" {
		trustState = contracts.NodeTrustUntrusted
	}
	status := normalizeStatus(req.Status)
	if status == contracts.NodeStatusStale {
		return contracts.NodeRecord{}, fmt.Errorf("%w: status must be registered, reachable, or unreachable", ErrValidation)
	}
	if status == "" {
		status = contracts.NodeStatusRegistered
	}
	record := contracts.NodeRecord{
		NodeID:      nodeID,
		URL:         nodeURL,
		DisplayName: strings.TrimSpace(req.DisplayName),
		TrustState:  trustState,
		Status:      status,
		Tags:        cleanStrings(req.Tags),
		Metadata:    cloneMap(req.Metadata),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.records[nodeID]; ok {
		record.LastSeenAt = existing.LastSeenAt
		record.LastCheckedAt = existing.LastCheckedAt
		if record.DisplayName == "" {
			record.DisplayName = existing.DisplayName
		}
		if len(record.Tags) == 0 {
			record.Tags = existing.Tags
		}
		if record.Metadata == nil {
			record.Metadata = existing.Metadata
		}
		if req.TrustState == "" {
			record.TrustState = existing.TrustState
			record.DisabledReason = existing.DisabledReason
		}
	}
	record = normalizeRecord(record)
	s.records[nodeID] = record
	if err := s.persistLocked(); err != nil {
		return contracts.NodeRecord{}, err
	}
	return s.projectLocked(record), nil
}

func (s *Store) List(opts ListOptions) (contracts.NodeRecordList, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.records))
	for nodeID := range s.records {
		ids = append(ids, nodeID)
	}
	sort.Strings(ids)
	start, end, next, err := paginationWindow(len(ids), opts)
	if err != nil {
		return contracts.NodeRecordList{}, err
	}
	items := make([]contracts.NodeRecord, 0, end-start)
	for _, nodeID := range ids[start:end] {
		items = append(items, s.projectLocked(s.records[nodeID]))
	}
	return contracts.NodeRecordList{Items: items, NextCursor: next}, nil
}

func (s *Store) Get(nodeID string) (contracts.NodeRecord, error) {
	nodeID = strings.TrimSpace(nodeID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[nodeID]
	if !ok {
		return contracts.NodeRecord{}, ErrNotFound
	}
	return s.projectLocked(record), nil
}

func (s *Store) UpdateTrust(nodeID string, req contracts.UpdateNodeTrustRequest) (contracts.NodeRecord, error) {
	nodeID = strings.TrimSpace(nodeID)
	trustState := normalizeTrustState(req.TrustState)
	if trustState == "" {
		return contracts.NodeRecord{}, fmt.Errorf("%w: trust_state must be trusted, untrusted, or disabled", ErrValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[nodeID]
	if !ok {
		return contracts.NodeRecord{}, ErrNotFound
	}
	record.TrustState = trustState
	if trustState == contracts.NodeTrustDisabled {
		record.DisabledReason = strings.TrimSpace(req.Reason)
	} else {
		record.DisabledReason = ""
	}
	s.records[nodeID] = normalizeRecord(record)
	if err := s.persistLocked(); err != nil {
		return contracts.NodeRecord{}, err
	}
	return s.projectLocked(s.records[nodeID]), nil
}

func (s *Store) Heartbeat(nodeID string, req contracts.NodeHeartbeatRequest) (contracts.NodeRecord, error) {
	nodeID = strings.TrimSpace(nodeID)
	status := normalizeStatus(req.Status)
	if status == "" || status == contracts.NodeStatusStale {
		return contracts.NodeRecord{}, fmt.Errorf("%w: status must be reachable, unreachable, or registered", ErrValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[nodeID]
	if !ok {
		return contracts.NodeRecord{}, ErrNotFound
	}
	now := s.now().UTC().Format(time.RFC3339)
	record.Status = status
	record.LastCheckedAt = now
	if status == contracts.NodeStatusReachable {
		record.LastSeenAt = now
	}
	if req.Metadata != nil {
		record.Metadata = cloneMap(req.Metadata)
	}
	s.records[nodeID] = normalizeRecord(record)
	if err := s.persistLocked(); err != nil {
		return contracts.NodeRecord{}, err
	}
	return s.projectLocked(s.records[nodeID]), nil
}

func (s *Store) ResolveRunnable(nodeID string) (contracts.NodeRecord, error) {
	record, err := s.Get(nodeID)
	if err != nil {
		return contracts.NodeRecord{}, err
	}
	if err := ValidateRunnable(record); err != nil {
		return contracts.NodeRecord{}, err
	}
	return record, nil
}

func ValidateRunnable(record contracts.NodeRecord) error {
	if reason := contracts.NodeRunnableBlockReason(record); reason != "" {
		return fmt.Errorf("%w: %s", ErrNotRunnable, reason)
	}
	return nil
}

func (s *Store) Health() contracts.ComponentHealth {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return contracts.NewComponentHealth("node_registry", map[string]any{"nodes": len(s.records)})
}

func (s *Store) projectLocked(record contracts.NodeRecord) contracts.NodeRecord {
	record = normalizeRecord(record)
	if record.Status == contracts.NodeStatusReachable && record.LastSeenAt != "" {
		lastSeenAt, err := time.Parse(time.RFC3339, record.LastSeenAt)
		if err == nil && s.now().Sub(lastSeenAt) > s.staleAfter {
			record.Status = contracts.NodeStatusStale
		}
	}
	record.Links = nodeLinks(record.NodeID)
	return record
}

func normalizeRecord(record contracts.NodeRecord) contracts.NodeRecord {
	record.NodeID = strings.TrimSpace(record.NodeID)
	record.URL = strings.TrimRight(strings.TrimSpace(record.URL), "/")
	record.DisplayName = strings.TrimSpace(record.DisplayName)
	record.TrustState = normalizeTrustState(record.TrustState)
	if record.TrustState == "" {
		record.TrustState = contracts.NodeTrustUntrusted
	}
	record.Status = normalizeStatus(record.Status)
	if record.Status == "" {
		record.Status = contracts.NodeStatusRegistered
	}
	record.Tags = cleanStrings(record.Tags)
	record.Metadata = cloneMap(record.Metadata)
	record.DisabledReason = strings.TrimSpace(record.DisabledReason)
	return record
}

func normalizeTrustState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case contracts.NodeTrustTrusted:
		return contracts.NodeTrustTrusted
	case contracts.NodeTrustUntrusted:
		return contracts.NodeTrustUntrusted
	case contracts.NodeTrustDisabled:
		return contracts.NodeTrustDisabled
	default:
		return ""
	}
}

func normalizeStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case contracts.NodeStatusRegistered:
		return contracts.NodeStatusRegistered
	case contracts.NodeStatusReachable:
		return contracts.NodeStatusReachable
	case contracts.NodeStatusUnreachable:
		return contracts.NodeStatusUnreachable
	case contracts.NodeStatusStale:
		return contracts.NodeStatusStale
	default:
		return ""
	}
}

func cleanStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func nodeLinks(nodeID string) map[string]any {
	if nodeID == "" {
		return map[string]any{}
	}
	return map[string]any{
		"self":      map[string]any{"method": "GET", "href": "/v1/node-registry/nodes/" + nodeID, "description": "Inspect node registry record."},
		"trust":     map[string]any{"method": "POST", "href": "/v1/node-registry/nodes/" + nodeID + "/trust", "description": "Update node trust state."},
		"heartbeat": map[string]any{"method": "POST", "href": "/v1/node-registry/nodes/" + nodeID + "/heartbeat", "description": "Record node reachability."},
	}
}

func paginationWindow(count int, opts ListOptions) (int, int, *string, error) {
	start := 0
	if opts.Cursor != "" {
		var parsed int
		if _, err := fmt.Sscanf(opts.Cursor, "cursor_node_registry_%06d", &parsed); err != nil {
			return 0, 0, nil, ErrInvalidCursor
		}
		start = parsed
	}
	if start > count {
		return 0, 0, nil, ErrInvalidCursor
	}
	end := count
	var next *string
	if opts.Limit > 0 && start+opts.Limit < end {
		end = start + opts.Limit
		cursor := fmt.Sprintf("cursor_node_registry_%06d", end)
		next = &cursor
	}
	return start, end, next, nil
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	ids := make([]string, 0, len(s.records))
	for nodeID := range s.records {
		ids = append(ids, nodeID)
	}
	sort.Strings(ids)
	snap := snapshot{Records: make([]contracts.NodeRecord, 0, len(ids))}
	for _, nodeID := range ids {
		record := s.records[nodeID]
		record.Links = nil
		snap.Records = append(snap.Records, record)
	}
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o600)
}
