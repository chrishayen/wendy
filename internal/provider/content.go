package provider

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"pacp/internal/contracts"
)

const DefaultContentTTL = time.Hour

type ContentStore struct {
	mu      sync.RWMutex
	now     func() time.Time
	nextID  int64
	records map[string]ContentRecord
}

type ContentPut struct {
	ContentRef string
	JobID      string
	Name       string
	MediaType  string
	Body       []byte
	TTL        time.Duration
}

type ContentRecord struct {
	Ref  contracts.ProviderContentRef
	Body []byte
}

func NewContentStore() *ContentStore {
	return &ContentStore{
		now:     time.Now,
		records: map[string]ContentRecord{},
	}
}

func (s *ContentStore) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *ContentStore) Put(req ContentPut) (contracts.ProviderContentRef, error) {
	if s == nil {
		return contracts.ProviderContentRef{}, fmt.Errorf("%w: content store is not configured", ErrValidation)
	}
	body := append([]byte(nil), req.Body...)
	ttl := req.TTL
	if ttl <= 0 {
		ttl = DefaultContentTTL
	}
	mediaType := strings.TrimSpace(req.MediaType)
	if mediaType == "" {
		mediaType = http.DetectContentType(body)
	}
	name := strings.TrimSpace(req.Name)
	contentRef := strings.TrimSpace(req.ContentRef)

	s.mu.Lock()
	defer s.mu.Unlock()
	if contentRef == "" {
		s.nextID++
		contentRef = providerContentRef(req.JobID, s.nextID, body)
	}
	if name == "" {
		name = contentRef
	}
	checksum, _ := contentChecksumAndDigest(body)
	ref := contracts.ProviderContentRef{
		ContentRef: contentRef,
		Name:       name,
		MediaType:  mediaType,
		Size:       int64(len(body)),
		Checksum:   checksum,
		ExpiresAt:  s.now().UTC().Add(ttl).Format(time.RFC3339),
	}
	s.records[contentRef] = ContentRecord{Ref: ref, Body: body}
	return ref, nil
}

func (s *ContentStore) Get(contentRef string) (ContentRecord, bool) {
	if s == nil {
		return ContentRecord{}, false
	}
	contentRef = strings.TrimSpace(contentRef)
	if contentRef == "" {
		return ContentRecord{}, false
	}
	s.mu.RLock()
	record, ok := s.records[contentRef]
	now := s.now
	s.mu.RUnlock()
	if !ok {
		return ContentRecord{}, false
	}
	expiresAt, err := time.Parse(time.RFC3339, record.Ref.ExpiresAt)
	if err != nil || !now().Before(expiresAt) {
		return ContentRecord{}, false
	}
	record.Body = append([]byte(nil), record.Body...)
	return record, true
}

func providerContentRef(jobID string, sequence int64, body []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(strings.TrimSpace(jobID)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.FormatInt(sequence, 10)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(body)
	sum := hash.Sum(nil)
	return "pcr_" + hex.EncodeToString(sum[:8])
}

func contentChecksumAndDigest(body []byte) (string, string) {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), "sha-256=" + base64.StdEncoding.EncodeToString(sum[:])
}
