package artifacts

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pacp/internal/contracts"
)

var (
	ErrNotFound               = errors.New("artifact resource not found")
	ErrValidation             = errors.New("validation failed")
	ErrMissingIdempotencyKey  = errors.New("missing idempotency key")
	ErrIdempotencyConflict    = errors.New("idempotency conflict")
	ErrExpired                = errors.New("artifact expired")
	ErrInvalidTransition      = errors.New("invalid artifact transition")
	ErrDisallowedPath         = errors.New("artifact path is outside the configured root")
	ErrAlreadyCompleted       = errors.New("upload is already completed")
	ErrContentAlreadyReceived = errors.New("upload content already received")
)

const defaultUploadTTL = 15 * time.Minute

type Store struct {
	mu             sync.RWMutex
	now            func() time.Time
	root           string
	uploadsDir     string
	blobsDir       string
	nextUploadID   int
	nextArtifactID int
	uploads        map[string]*uploadRecord
	artifacts      map[string]*artifactRecord
	idempotency    map[string]idempotentRecord
}

type uploadRecord struct {
	session                contracts.ArtifactUploadSession
	metadata               map[string]any
	receivedChecksum       string
	receivedDigest         string
	receivedPath           string
	contentIdempotencyKey  string
	completeIdempotencyKey string
}

type artifactRecord struct {
	artifact contracts.Artifact
	path     string
	digest   string
}

type idempotentRecord struct {
	operation   string
	fingerprint string
	response    any
	created     bool
}

type ContentUpload struct {
	Body          []byte
	ContentType   string
	ContentLength string
	Digest        string
}

type ArtifactContent struct {
	Body        []byte
	ContentType string
	Digest      string
	Size        int64
}

type ListFilter struct {
	ProducerRef    string
	OwnerSubjectID string
}

func NewStore(root string) (*Store, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: root is required", ErrValidation)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	uploadsDir := filepath.Join(absRoot, "uploads")
	blobsDir := filepath.Join(absRoot, "blobs")
	if err := os.MkdirAll(uploadsDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(blobsDir, 0o700); err != nil {
		return nil, err
	}
	return &Store{
		now:            time.Now,
		root:           absRoot,
		uploadsDir:     uploadsDir,
		blobsDir:       blobsDir,
		nextUploadID:   1,
		nextArtifactID: 1,
		uploads:        map[string]*uploadRecord{},
		artifacts:      map[string]*artifactRecord{},
		idempotency:    map[string]idempotentRecord{},
	}, nil
}

func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *Store) CreateUpload(req contracts.CreateArtifactUploadRequest, idempotencyKey string) (contracts.ArtifactUploadSession, bool, error) {
	if idempotencyKey == "" {
		return contracts.ArtifactUploadSession{}, false, ErrMissingIdempotencyKey
	}
	if req.Name == "" {
		return contracts.ArtifactUploadSession{}, false, fmt.Errorf("%w: name is required", ErrValidation)
	}
	if req.MediaType == "" {
		return contracts.ArtifactUploadSession{}, false, fmt.Errorf("%w: media_type is required", ErrValidation)
	}
	if req.OwnerSubjectID == "" {
		return contracts.ArtifactUploadSession{}, false, fmt.Errorf("%w: owner_subject_id is required", ErrValidation)
	}
	if req.ExpectedSize != nil && *req.ExpectedSize < 0 {
		return contracts.ArtifactUploadSession{}, false, fmt.Errorf("%w: expected_size must be positive", ErrValidation)
	}
	if req.ExpectedChecksum != "" && !validChecksum(req.ExpectedChecksum) {
		return contracts.ArtifactUploadSession{}, false, fmt.Errorf("%w: expected_checksum must use sha256:<hex>", ErrValidation)
	}

	fp, err := fingerprint(req)
	if err != nil {
		return contracts.ArtifactUploadSession{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, ok, err := s.checkIdempotencyLocked("upload:create", idempotencyKey, fp); ok || err != nil {
		if err != nil {
			return contracts.ArtifactUploadSession{}, false, err
		}
		return cloneUpload(replay.response.(contracts.ArtifactUploadSession)), false, nil
	}

	now := s.now().UTC()
	uploadID := fmt.Sprintf("upload_%06d", s.nextUploadID)
	s.nextUploadID++
	session := contracts.ArtifactUploadSession{
		UploadID:         uploadID,
		State:            contracts.ArtifactUploadCreated,
		Name:             req.Name,
		MediaType:        req.MediaType,
		ProducerRef:      req.ProducerRef,
		OwnerSubjectID:   req.OwnerSubjectID,
		ReceivedSize:     nil,
		ExpectedSize:     copyInt64Ptr(req.ExpectedSize),
		ExpectedChecksum: strings.ToLower(req.ExpectedChecksum),
		ArtifactID:       nil,
		ExpiresAt:        formatTime(now.Add(defaultUploadTTL)),
		Links:            uploadLinks(uploadID, contracts.ArtifactUploadCreated),
	}
	record := &uploadRecord{
		session:  session,
		metadata: cloneMap(req.Metadata),
	}
	s.uploads[uploadID] = record
	s.idempotency[idempotencyKey] = idempotentRecord{
		operation:   "upload:create",
		fingerprint: fp,
		response:    cloneUpload(session),
		created:     true,
	}
	return cloneUpload(session), true, nil
}

func (s *Store) PutContent(uploadID string, upload ContentUpload, idempotencyKey string) (contracts.ArtifactUploadSession, error) {
	if idempotencyKey == "" {
		return contracts.ArtifactUploadSession{}, ErrMissingIdempotencyKey
	}
	if upload.ContentType == "" || upload.ContentLength == "" || upload.Digest == "" {
		return contracts.ArtifactUploadSession{}, fmt.Errorf("%w: Content-Type, Content-Length, and Digest headers are required for artifact content upload", ErrValidation)
	}
	length, err := strconv.ParseInt(upload.ContentLength, 10, 64)
	if err != nil || length < 0 {
		return contracts.ArtifactUploadSession{}, fmt.Errorf("%w: Content-Length is invalid", ErrValidation)
	}
	if length != int64(len(upload.Body)) {
		return contracts.ArtifactUploadSession{}, fmt.Errorf("%w: Content-Length does not match uploaded content", ErrValidation)
	}

	checksum, digest := checksumAndDigest(upload.Body)
	if upload.Digest != digest {
		return contracts.ArtifactUploadSession{}, fmt.Errorf("%w: Digest does not match uploaded content", ErrValidation)
	}
	fp, err := fingerprint(map[string]any{
		"upload_id":      uploadID,
		"content_type":   upload.ContentType,
		"content_length": upload.ContentLength,
		"digest":         upload.Digest,
		"checksum":       checksum,
	})
	if err != nil {
		return contracts.ArtifactUploadSession{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, ok, err := s.checkIdempotencyLocked("upload:content:"+uploadID, idempotencyKey, fp); ok || err != nil {
		if err != nil {
			return contracts.ArtifactUploadSession{}, err
		}
		return cloneUpload(replay.response.(contracts.ArtifactUploadSession)), nil
	}

	rec, ok := s.uploads[uploadID]
	if !ok {
		return contracts.ArtifactUploadSession{}, ErrNotFound
	}
	if err := s.requireUploadWritableLocked(rec); err != nil {
		return contracts.ArtifactUploadSession{}, err
	}
	if rec.session.State == contracts.ArtifactUploadReceived {
		return contracts.ArtifactUploadSession{}, ErrContentAlreadyReceived
	}
	if upload.ContentType != rec.session.MediaType && upload.ContentType != "application/octet-stream" {
		return contracts.ArtifactUploadSession{}, fmt.Errorf("%w: Content-Type does not match upload media_type", ErrValidation)
	}
	if rec.session.ExpectedSize != nil && *rec.session.ExpectedSize != length {
		return contracts.ArtifactUploadSession{}, fmt.Errorf("%w: artifact size mismatch", ErrValidation)
	}
	if rec.session.ExpectedChecksum != "" && rec.session.ExpectedChecksum != checksum {
		return contracts.ArtifactUploadSession{}, fmt.Errorf("%w: checksum does not match uploaded content", ErrValidation)
	}

	path, err := s.safeUploadPath(uploadID)
	if err != nil {
		return contracts.ArtifactUploadSession{}, err
	}
	if err := os.WriteFile(path, upload.Body, 0o600); err != nil {
		return contracts.ArtifactUploadSession{}, err
	}
	rec.receivedChecksum = checksum
	rec.receivedDigest = digest
	rec.receivedPath = path
	rec.contentIdempotencyKey = idempotencyKey
	rec.session.State = contracts.ArtifactUploadReceived
	rec.session.ReceivedSize = int64Ptr(length)
	rec.session.Links = uploadLinks(uploadID, contracts.ArtifactUploadReceived)
	s.idempotency[idempotencyKey] = idempotentRecord{
		operation:   "upload:content:" + uploadID,
		fingerprint: fp,
		response:    cloneUpload(rec.session),
	}
	return cloneUpload(rec.session), nil
}

func (s *Store) CompleteUpload(uploadID string, req contracts.CompleteArtifactUploadRequest, idempotencyKey string) (contracts.Artifact, bool, error) {
	if idempotencyKey == "" {
		return contracts.Artifact{}, false, ErrMissingIdempotencyKey
	}
	if req.Size < 0 {
		return contracts.Artifact{}, false, fmt.Errorf("%w: size must be positive", ErrValidation)
	}
	if !validChecksum(req.Checksum) {
		return contracts.Artifact{}, false, fmt.Errorf("%w: checksum must use sha256:<hex>", ErrValidation)
	}
	req.Checksum = strings.ToLower(req.Checksum)
	fp, err := fingerprint(map[string]any{"upload_id": uploadID, "request": req})
	if err != nil {
		return contracts.Artifact{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, ok, err := s.checkIdempotencyLocked("upload:complete:"+uploadID, idempotencyKey, fp); ok || err != nil {
		if err != nil {
			return contracts.Artifact{}, false, err
		}
		return cloneArtifact(replay.response.(contracts.Artifact)), false, nil
	}

	rec, ok := s.uploads[uploadID]
	if !ok {
		return contracts.Artifact{}, false, ErrNotFound
	}
	if rec.session.State == contracts.ArtifactUploadCompleted {
		return contracts.Artifact{}, false, ErrAlreadyCompleted
	}
	if err := s.requireUploadWritableLocked(rec); err != nil {
		return contracts.Artifact{}, false, err
	}
	if rec.session.State != contracts.ArtifactUploadReceived {
		return contracts.Artifact{}, false, ErrInvalidTransition
	}
	if rec.session.ReceivedSize == nil || *rec.session.ReceivedSize != req.Size {
		return contracts.Artifact{}, false, fmt.Errorf("%w: artifact size mismatch", ErrValidation)
	}
	if rec.receivedChecksum != req.Checksum {
		return contracts.Artifact{}, false, fmt.Errorf("%w: checksum does not match uploaded content", ErrValidation)
	}

	artifactID := fmt.Sprintf("art_%06d", s.nextArtifactID)
	s.nextArtifactID++
	blobPath, err := s.safeBlobPath(artifactID)
	if err != nil {
		return contracts.Artifact{}, false, err
	}
	if err := moveFile(rec.receivedPath, blobPath); err != nil {
		return contracts.Artifact{}, false, err
	}

	now := s.formatNow()
	artifact := contracts.Artifact{
		ArtifactID:     artifactID,
		Name:           rec.session.Name,
		MediaType:      rec.session.MediaType,
		Size:           req.Size,
		Checksum:       req.Checksum,
		CreatedAt:      now,
		ProducerRef:    rec.session.ProducerRef,
		OwnerSubjectID: rec.session.OwnerSubjectID,
		Metadata:       cloneMap(rec.metadata),
		Links:          artifactLinks(artifactID),
	}
	s.artifacts[artifactID] = &artifactRecord{
		artifact: artifact,
		path:     blobPath,
		digest:   rec.receivedDigest,
	}
	rec.completeIdempotencyKey = idempotencyKey
	rec.session.State = contracts.ArtifactUploadCompleted
	rec.session.ArtifactID = stringPtr(artifactID)
	rec.session.CompletedAt = now
	rec.session.Links = map[string]any{}
	s.idempotency[idempotencyKey] = idempotentRecord{
		operation:   "upload:complete:" + uploadID,
		fingerprint: fp,
		response:    cloneArtifact(artifact),
		created:     true,
	}
	return cloneArtifact(artifact), true, nil
}

func (s *Store) RegisterLocalArtifact(req contracts.RegisterLocalArtifactRequest) (contracts.Artifact, error) {
	if req.Path == "" {
		return contracts.Artifact{}, fmt.Errorf("%w: path is required", ErrValidation)
	}
	if req.MediaType == "" {
		return contracts.Artifact{}, fmt.Errorf("%w: media_type is required", ErrValidation)
	}
	if req.OwnerSubjectID == "" {
		return contracts.Artifact{}, fmt.Errorf("%w: owner_subject_id is required", ErrValidation)
	}
	guarded, err := s.guardPath(req.Path)
	if err != nil {
		return contracts.Artifact{}, err
	}
	body, err := os.ReadFile(guarded)
	if err != nil {
		return contracts.Artifact{}, err
	}
	checksum, digest := checksumAndDigest(body)
	name := req.Name
	if name == "" {
		name = filepath.Base(guarded)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	artifactID := fmt.Sprintf("art_%06d", s.nextArtifactID)
	s.nextArtifactID++
	blobPath, err := s.safeBlobPath(artifactID)
	if err != nil {
		return contracts.Artifact{}, err
	}
	if err := os.WriteFile(blobPath, body, 0o600); err != nil {
		return contracts.Artifact{}, err
	}
	artifact := contracts.Artifact{
		ArtifactID:     artifactID,
		Name:           name,
		MediaType:      req.MediaType,
		Size:           int64(len(body)),
		Checksum:       checksum,
		CreatedAt:      s.formatNow(),
		ProducerRef:    req.ProducerRef,
		OwnerSubjectID: req.OwnerSubjectID,
		Metadata:       cloneMap(req.Metadata),
		Links:          artifactLinks(artifactID),
	}
	s.artifacts[artifactID] = &artifactRecord{artifact: artifact, path: blobPath, digest: digest}
	return cloneArtifact(artifact), nil
}

func (s *Store) GetUpload(uploadID string) (contracts.ArtifactUploadSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.uploads[uploadID]
	if !ok {
		return contracts.ArtifactUploadSession{}, ErrNotFound
	}
	s.expireUploadLocked(rec)
	return cloneUpload(rec.session), nil
}

func (s *Store) GetArtifact(artifactID string) (contracts.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.artifacts[artifactID]
	if !ok {
		return contracts.Artifact{}, ErrNotFound
	}
	return cloneArtifact(rec.artifact), nil
}

func (s *Store) ListArtifacts(filter ListFilter) []contracts.Artifact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.artifacts))
	for id := range s.artifacts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	items := make([]contracts.Artifact, 0, len(ids))
	for _, id := range ids {
		artifact := s.artifacts[id].artifact
		if filter.ProducerRef != "" && artifact.ProducerRef != filter.ProducerRef {
			continue
		}
		if filter.OwnerSubjectID != "" && artifact.OwnerSubjectID != filter.OwnerSubjectID {
			continue
		}
		items = append(items, cloneArtifact(artifact))
	}
	return items
}

func (s *Store) PolicyContext(artifactID string) (contracts.ArtifactPolicyContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.artifacts[artifactID]
	if !ok {
		return contracts.ArtifactPolicyContext{}, ErrNotFound
	}
	return contracts.ArtifactPolicyContext{
		ResourceKind:   "artifact",
		ArtifactID:     artifactID,
		OwnerSubjectID: rec.artifact.OwnerSubjectID,
		ProducerRef:    rec.artifact.ProducerRef,
		PolicyState:    "available",
	}, nil
}

func (s *Store) ReadContent(artifactID string) (ArtifactContent, error) {
	s.mu.RLock()
	rec, ok := s.artifacts[artifactID]
	s.mu.RUnlock()
	if !ok {
		return ArtifactContent{}, ErrNotFound
	}
	body, err := os.ReadFile(rec.path)
	if err != nil {
		return ArtifactContent{}, err
	}
	return ArtifactContent{
		Body:        body,
		ContentType: rec.artifact.MediaType,
		Digest:      rec.digest,
		Size:        int64(len(body)),
	}, nil
}

func (s *Store) requireUploadWritableLocked(rec *uploadRecord) error {
	s.expireUploadLocked(rec)
	switch rec.session.State {
	case contracts.ArtifactUploadExpired:
		return ErrExpired
	case contracts.ArtifactUploadAborted, contracts.ArtifactUploadCompleted:
		return ErrInvalidTransition
	default:
		return nil
	}
}

func (s *Store) expireUploadLocked(rec *uploadRecord) {
	if rec.session.State == contracts.ArtifactUploadCompleted ||
		rec.session.State == contracts.ArtifactUploadAborted ||
		rec.session.State == contracts.ArtifactUploadExpired {
		return
	}
	expiresAt, err := time.Parse(time.RFC3339, rec.session.ExpiresAt)
	if err != nil || !s.now().UTC().Before(expiresAt) {
		rec.session.State = contracts.ArtifactUploadExpired
		rec.session.Links = map[string]any{}
	}
}

func (s *Store) checkIdempotencyLocked(operation, key, fp string) (idempotentRecord, bool, error) {
	record, ok := s.idempotency[key]
	if !ok {
		return idempotentRecord{}, false, nil
	}
	if record.operation != operation || record.fingerprint != fp {
		return idempotentRecord{}, false, ErrIdempotencyConflict
	}
	return record, true, nil
}

func (s *Store) safeUploadPath(uploadID string) (string, error) {
	return s.safeJoin(s.uploadsDir, uploadID+".bin")
}

func (s *Store) safeBlobPath(artifactID string) (string, error) {
	return s.safeJoin(s.blobsDir, artifactID+".bin")
}

func (s *Store) safeJoin(base, name string) (string, error) {
	path := filepath.Join(base, filepath.Clean(name))
	if !strings.HasPrefix(filepath.Base(path), filepath.Base(filepath.Clean(name))) {
		return "", ErrDisallowedPath
	}
	return s.guardPath(path)
}

func (s *Store) guardPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(s.root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", ErrDisallowedPath
	}
	return abs, nil
}

func (s *Store) formatNow() string {
	return formatTime(s.now().UTC())
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func checksumAndDigest(body []byte) (string, string) {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), "sha-256=" + base64.StdEncoding.EncodeToString(sum[:])
}

func validChecksum(checksum string) bool {
	if !strings.HasPrefix(checksum, "sha256:") {
		return false
	}
	raw := strings.TrimPrefix(checksum, "sha256:")
	if len(raw) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

func uploadLinks(uploadID string, state contracts.ArtifactUploadState) map[string]any {
	links := map[string]any{}
	switch state {
	case contracts.ArtifactUploadCreated:
		links["content"] = map[string]any{"method": "PUT", "href": "/v1/artifact-uploads/" + uploadID + "/content", "description": "Upload bytes."}
		links["complete"] = map[string]any{"method": "POST", "href": "/v1/artifact-uploads/" + uploadID + "/complete", "description": "Complete upload."}
	case contracts.ArtifactUploadReceived:
		links["complete"] = map[string]any{"method": "POST", "href": "/v1/artifact-uploads/" + uploadID + "/complete", "description": "Complete upload."}
	}
	return links
}

func artifactLinks(artifactID string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"method": "GET", "href": "/v1/artifacts/" + artifactID, "description": "Read artifact metadata."},
		"content":  map[string]any{"method": "GET", "href": "/v1/artifacts/" + artifactID + "/content", "description": "Read artifact content."},
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}

func copyInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func stringPtr(value string) *string {
	return &value
}

func cloneUpload(upload contracts.ArtifactUploadSession) contracts.ArtifactUploadSession {
	raw, _ := json.Marshal(upload)
	var cloned contracts.ArtifactUploadSession
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func cloneArtifact(artifact contracts.Artifact) contracts.Artifact {
	raw, _ := json.Marshal(artifact)
	var cloned contracts.Artifact
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	raw, _ := json.Marshal(value)
	var cloned map[string]any
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func fingerprint(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func HeadersFromRequest(r *http.Request, body []byte) ContentUpload {
	contentLength := r.Header.Get("Content-Length")
	if contentLength == "" && r.ContentLength >= 0 {
		contentLength = strconv.FormatInt(r.ContentLength, 10)
	}
	return ContentUpload{
		Body:          body,
		ContentType:   r.Header.Get("Content-Type"),
		ContentLength: contentLength,
		Digest:        r.Header.Get("Digest"),
	}
}
