package artifacts

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"pacp/internal/contracts"
)

func TestStoreUploadCompleteAndReadArtifact(t *testing.T) {
	store := newTestStore(t)
	body := []byte("artifact bytes")
	checksum, digest := checksumAndDigest(body)
	size := int64(len(body))

	session, created, err := store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:             "result.txt",
		MediaType:        "text/plain",
		ProducerRef:      "job_1",
		OwnerSubjectID:   "sub_agent",
		ExpectedSize:     &size,
		ExpectedChecksum: checksum,
		Metadata:         map[string]any{"capability_id": "cap_text"},
	}, "create-1")
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	if !created || session.State != contracts.ArtifactUploadCreated {
		t.Fatalf("create response = %#v created=%v", session, created)
	}

	replay, replayCreated, err := store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:             "result.txt",
		MediaType:        "text/plain",
		ProducerRef:      "job_1",
		OwnerSubjectID:   "sub_agent",
		ExpectedSize:     &size,
		ExpectedChecksum: checksum,
		Metadata:         map[string]any{"capability_id": "cap_text"},
	}, "create-1")
	if err != nil {
		t.Fatalf("create replay: %v", err)
	}
	if replayCreated || replay.UploadID != session.UploadID {
		t.Fatalf("create replay = %#v created=%v", replay, replayCreated)
	}

	received, err := store.PutContent(session.UploadID, ContentUpload{
		Body:          body,
		ContentType:   "text/plain",
		ContentLength: "14",
		Digest:        digest,
	}, "content-1")
	if err != nil {
		t.Fatalf("put content: %v", err)
	}
	if received.State != contracts.ArtifactUploadReceived || received.ReceivedSize == nil || *received.ReceivedSize != size {
		t.Fatalf("received = %#v", received)
	}

	artifact, completed, err := store.CompleteUpload(session.UploadID, contracts.CompleteArtifactUploadRequest{
		Checksum: checksum,
		Size:     size,
	}, "complete-1")
	if err != nil {
		t.Fatalf("complete upload: %v", err)
	}
	if !completed || artifact.ArtifactID == "" || artifact.Checksum != checksum {
		t.Fatalf("artifact = %#v completed=%v", artifact, completed)
	}

	completeReplay, replayCompleted, err := store.CompleteUpload(session.UploadID, contracts.CompleteArtifactUploadRequest{
		Checksum: checksum,
		Size:     size,
	}, "complete-1")
	if err != nil {
		t.Fatalf("complete replay: %v", err)
	}
	if replayCompleted || completeReplay.ArtifactID != artifact.ArtifactID {
		t.Fatalf("complete replay = %#v completed=%v", completeReplay, replayCompleted)
	}

	content, err := store.ReadContent(artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if string(content.Body) != string(body) || content.Digest != digest || content.ContentType != "text/plain" {
		t.Fatalf("content = %#v", content)
	}

	list, _, err := store.ListArtifacts(ListFilter{ProducerRef: "job_1"})
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(list) != 1 || list[0].ArtifactID != artifact.ArtifactID {
		t.Fatalf("artifact list = %#v", list)
	}
	context, err := store.PolicyContext(artifact.ArtifactID)
	if err != nil {
		t.Fatalf("policy context: %v", err)
	}
	if context.OwnerSubjectID != "sub_agent" || context.ProducerRef != "job_1" {
		t.Fatalf("policy context = %#v", context)
	}
}

func TestStoreExpiresArtifactsByRetentionPolicy(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	store.SetArtifactTTL(time.Minute)

	body := []byte("retained bytes")
	checksum, digest := checksumAndDigest(body)
	size := int64(len(body))
	session, _, err := store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:             "retained.txt",
		MediaType:        "text/plain",
		ProducerRef:      "job_retained",
		OwnerSubjectID:   "sub_agent",
		ExpectedSize:     &size,
		ExpectedChecksum: checksum,
	}, "create-retained")
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	if _, err := store.PutContent(session.UploadID, ContentUpload{
		Body:          body,
		ContentType:   "text/plain",
		ContentLength: strconv.FormatInt(size, 10),
		Digest:        digest,
	}, "content-retained"); err != nil {
		t.Fatalf("put content: %v", err)
	}
	artifact, _, err := store.CompleteUpload(session.UploadID, contracts.CompleteArtifactUploadRequest{
		Checksum: checksum,
		Size:     size,
	}, "complete-retained")
	if err != nil {
		t.Fatalf("complete upload: %v", err)
	}
	if artifact.ExpiresAt != now.Add(time.Minute).Format(time.RFC3339) {
		t.Fatalf("expires_at = %q", artifact.ExpiresAt)
	}

	beforeExpiry, err := store.GetArtifact(artifact.ArtifactID)
	if err != nil {
		t.Fatalf("get before expiry: %v", err)
	}
	if beforeExpiry.ArtifactID != artifact.ArtifactID {
		t.Fatalf("before expiry = %#v", beforeExpiry)
	}
	if list, _, err := store.ListArtifacts(ListFilter{ProducerRef: "job_retained"}); err != nil || len(list) != 1 {
		t.Fatalf("list before expiry = %#v", list)
	}
	context, err := store.PolicyContext(artifact.ArtifactID)
	if err != nil {
		t.Fatalf("policy context before expiry: %v", err)
	}
	if context.PolicyState != "available" {
		t.Fatalf("policy context before expiry = %#v", context)
	}

	blobPath := store.artifacts[artifact.ArtifactID].path
	now = now.Add(time.Minute + time.Second)
	_, err = store.GetArtifact(artifact.ArtifactID)
	if !errors.Is(err, ErrArtifactExpired) || !errors.Is(err, ErrExpired) {
		t.Fatalf("expected expired artifact metadata, got %v", err)
	}
	if list, _, err := store.ListArtifacts(ListFilter{ProducerRef: "job_retained"}); err != nil || len(list) != 0 {
		t.Fatalf("list after expiry = %#v", list)
	}
	context, err = store.PolicyContext(artifact.ArtifactID)
	if err != nil {
		t.Fatalf("policy context after expiry: %v", err)
	}
	if context.PolicyState != "expired" {
		t.Fatalf("policy context after expiry = %#v", context)
	}
	_, err = store.ReadContent(artifact.ArtifactID)
	if !errors.Is(err, ErrArtifactExpired) {
		t.Fatalf("expected expired artifact content, got %v", err)
	}
	if _, err := os.Stat(blobPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected expired artifact blob cleanup, stat err=%v", err)
	}
}

func TestStoreSweepExpiredRemovesUploadAndArtifactContent(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	store.SetArtifactTTL(time.Minute)

	artifactBody := []byte("artifact sweep bytes")
	artifactChecksum, artifactDigest := checksumAndDigest(artifactBody)
	artifactSize := int64(len(artifactBody))
	completeSession, _, err := store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:             "complete.txt",
		MediaType:        "text/plain",
		ProducerRef:      "job_sweep",
		OwnerSubjectID:   "sub_agent",
		ExpectedSize:     &artifactSize,
		ExpectedChecksum: artifactChecksum,
	}, "sweep-create-complete")
	if err != nil {
		t.Fatalf("create completed upload: %v", err)
	}
	if _, err := store.PutContent(completeSession.UploadID, ContentUpload{
		Body:          artifactBody,
		ContentType:   "text/plain",
		ContentLength: strconv.FormatInt(artifactSize, 10),
		Digest:        artifactDigest,
	}, "sweep-content-complete"); err != nil {
		t.Fatalf("put completed content: %v", err)
	}
	artifact, _, err := store.CompleteUpload(completeSession.UploadID, contracts.CompleteArtifactUploadRequest{
		Checksum: artifactChecksum,
		Size:     artifactSize,
	}, "sweep-complete")
	if err != nil {
		t.Fatalf("complete upload: %v", err)
	}
	artifactPath := store.artifacts[artifact.ArtifactID].path

	partialBody := []byte("partial sweep bytes")
	partialSession, _, err := store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:           "partial.txt",
		MediaType:      "text/plain",
		ProducerRef:    "job_sweep",
		OwnerSubjectID: "sub_agent",
	}, "sweep-create-partial")
	if err != nil {
		t.Fatalf("create partial upload: %v", err)
	}
	if _, err := store.PutContent(partialSession.UploadID, validContentUpload(partialBody, "text/plain"), "sweep-content-partial"); err != nil {
		t.Fatalf("put partial content: %v", err)
	}
	uploadPath := store.uploads[partialSession.UploadID].receivedPath

	now = now.Add(defaultUploadTTL + time.Second)
	result, err := store.SweepExpired()
	if err != nil {
		t.Fatalf("sweep expired: %v", err)
	}
	if result.CheckedAt != now.Format(time.RFC3339) ||
		result.ExpiredUploads != 1 ||
		result.ExpiredArtifacts != 1 ||
		result.DeletedUploadFiles != 1 ||
		result.DeletedArtifactFiles != 1 {
		t.Fatalf("sweep result = %#v", result)
	}
	if state := store.uploads[partialSession.UploadID].session.State; state != contracts.ArtifactUploadExpired {
		t.Fatalf("partial upload state = %s", state)
	}
	if store.uploads[partialSession.UploadID].receivedPath != "" {
		t.Fatalf("partial upload path was not cleared")
	}
	if store.artifacts[artifact.ArtifactID].path != "" || len(store.artifacts[artifact.ArtifactID].artifact.Links) != 0 {
		t.Fatalf("expired artifact record = %#v", store.artifacts[artifact.ArtifactID])
	}
	if _, err := os.Stat(uploadPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected expired upload cleanup, stat err=%v", err)
	}
	if _, err := os.Stat(artifactPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected expired artifact cleanup, stat err=%v", err)
	}
	if _, err := store.ReadContent(artifact.ArtifactID); !errors.Is(err, ErrArtifactExpired) {
		t.Fatalf("expected expired artifact after sweep, got %v", err)
	}
}

func TestStoreRejectsUploadValidationFailures(t *testing.T) {
	store := newTestStore(t)
	body := []byte("artifact bytes")
	checksum, digest := checksumAndDigest(body)
	size := int64(len(body))
	session, _, err := store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:             "result.txt",
		MediaType:        "text/plain",
		OwnerSubjectID:   "sub_agent",
		ExpectedSize:     &size,
		ExpectedChecksum: checksum,
	}, "create-1")
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}

	_, err = store.PutContent(session.UploadID, ContentUpload{
		Body:          body,
		ContentType:   "text/plain",
		ContentLength: "13",
		Digest:        digest,
	}, "content-1")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected length validation, got %v", err)
	}

	_, err = store.PutContent(session.UploadID, ContentUpload{
		Body:          body,
		ContentType:   "text/plain",
		ContentLength: "14",
		Digest:        "sha-256=bad",
	}, "content-2")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected digest validation, got %v", err)
	}

	_, err = store.PutContent(session.UploadID, ContentUpload{
		Body:          body,
		ContentType:   "text/plain",
		ContentLength: "14",
		Digest:        digest,
	}, "")
	if !errors.Is(err, ErrMissingIdempotencyKey) {
		t.Fatalf("expected missing idempotency, got %v", err)
	}
}

func TestStoreRejectsIdempotencyConflicts(t *testing.T) {
	store := newTestStore(t)
	_, _, err := store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:           "one.txt",
		MediaType:      "text/plain",
		OwnerSubjectID: "sub_agent",
	}, "create-1")
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	_, _, err = store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:           "two.txt",
		MediaType:      "text/plain",
		OwnerSubjectID: "sub_agent",
	}, "create-1")
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected create idempotency conflict, got %v", err)
	}
}

func TestStoreExpiresUploadSession(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	session, _, err := store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:           "result.txt",
		MediaType:      "text/plain",
		OwnerSubjectID: "sub_agent",
	}, "create-1")
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	now = now.Add(defaultUploadTTL + time.Second)
	_, err = store.PutContent(session.UploadID, ContentUpload{
		Body:          []byte("late"),
		ContentType:   "text/plain",
		ContentLength: "4",
		Digest:        "sha-256=invalid",
	}, "content-1")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected digest validation to run before expiration, got %v", err)
	}
	_, err = store.PutContent(session.UploadID, validContentUpload([]byte("late"), "text/plain"), "content-2")
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expected expired upload, got %v", err)
	}
}

func TestStoreRegisterLocalArtifactUsesPathGuard(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	store.SetArtifactTTL(time.Hour)
	localPath := filepath.Join(root, "provider-output.txt")
	if err := os.WriteFile(localPath, []byte("local artifact"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	artifact, err := store.RegisterLocalArtifact(contracts.RegisterLocalArtifactRequest{
		Path:           localPath,
		MediaType:      "text/plain",
		ProducerRef:    "job_local",
		OwnerSubjectID: "sub_agent",
	})
	if err != nil {
		t.Fatalf("register local artifact: %v", err)
	}
	if artifact.ArtifactID == "" || artifact.Name != "provider-output.txt" {
		t.Fatalf("local artifact = %#v", artifact)
	}
	if artifact.ExpiresAt != now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("local artifact expires_at = %q", artifact.ExpiresAt)
	}

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	_, err = store.RegisterLocalArtifact(contracts.RegisterLocalArtifactRequest{
		Path:           outside,
		MediaType:      "text/plain",
		OwnerSubjectID: "sub_agent",
	})
	if !errors.Is(err, ErrDisallowedPath) {
		t.Fatalf("expected disallowed path, got %v", err)
	}
}

func TestPersistentStoreReloadsUploadAndArtifactState(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "artifact-state.json")
	store, err := NewPersistentStore(root, statePath)
	if err != nil {
		t.Fatalf("new persistent store: %v", err)
	}
	body := []byte("artifact bytes")
	checksum, digest := checksumAndDigest(body)
	size := int64(len(body))
	session, _, err := store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:             "result.txt",
		MediaType:        "text/plain",
		ProducerRef:      "job_1",
		OwnerSubjectID:   "sub_agent",
		ExpectedSize:     &size,
		ExpectedChecksum: checksum,
		Metadata:         map[string]any{"capability_id": "cap_text"},
	}, "create-persist")
	if err != nil {
		t.Fatalf("create persistent upload: %v", err)
	}
	if _, err := store.PutContent(session.UploadID, ContentUpload{
		Body:          body,
		ContentType:   "text/plain",
		ContentLength: "14",
		Digest:        digest,
	}, "content-persist"); err != nil {
		t.Fatalf("put persistent content: %v", err)
	}

	reloaded, err := NewPersistentStore(root, statePath)
	if err != nil {
		t.Fatalf("reload persistent store: %v", err)
	}
	contentReplay, err := reloaded.PutContent(session.UploadID, ContentUpload{
		Body:          body,
		ContentType:   "text/plain",
		ContentLength: "14",
		Digest:        digest,
	}, "content-persist")
	if err != nil {
		t.Fatalf("content replay after reload: %v", err)
	}
	if contentReplay.State != contracts.ArtifactUploadReceived {
		t.Fatalf("content replay = %#v", contentReplay)
	}
	artifact, created, err := reloaded.CompleteUpload(session.UploadID, contracts.CompleteArtifactUploadRequest{
		Checksum: checksum,
		Size:     size,
	}, "complete-persist")
	if err != nil {
		t.Fatalf("complete after reload: %v", err)
	}
	if !created {
		t.Fatalf("expected new artifact after reload")
	}

	reloadedAgain, err := NewPersistentStore(root, statePath)
	if err != nil {
		t.Fatalf("reload completed persistent store: %v", err)
	}
	completeReplay, replayCreated, err := reloadedAgain.CompleteUpload(session.UploadID, contracts.CompleteArtifactUploadRequest{
		Checksum: checksum,
		Size:     size,
	}, "complete-persist")
	if err != nil {
		t.Fatalf("complete replay after reload: %v", err)
	}
	if replayCreated || completeReplay.ArtifactID != artifact.ArtifactID {
		t.Fatalf("complete replay = %#v created=%v", completeReplay, replayCreated)
	}
	list, _, err := reloadedAgain.ListArtifacts(ListFilter{ProducerRef: "job_1"})
	if err != nil {
		t.Fatalf("list after reload: %v", err)
	}
	if len(list) != 1 || list[0].ArtifactID != artifact.ArtifactID {
		t.Fatalf("persisted artifact list = %#v", list)
	}
	content, err := reloadedAgain.ReadContent(artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read persisted content: %v", err)
	}
	if string(content.Body) != string(body) || content.Digest != digest {
		t.Fatalf("persisted content = %#v", content)
	}
}

func TestStoreListArtifactsPaginatesWithOpaqueCursor(t *testing.T) {
	store := newTestStore(t)
	first := registerTestArtifact(t, store, "first.txt", "job_page", "sub_agent", []byte("first"))
	second := registerTestArtifact(t, store, "second.txt", "job_page", "sub_agent", []byte("second"))

	firstPage, next, err := store.ListArtifacts(ListFilter{ProducerRef: "job_page", Limit: 1})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(firstPage) != 1 || firstPage[0].ArtifactID != first.ArtifactID || next == nil {
		t.Fatalf("first page items=%#v next=%v", firstPage, next)
	}
	secondPage, next, err := store.ListArtifacts(ListFilter{ProducerRef: "job_page", Limit: 1, Cursor: *next})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(secondPage) != 1 || secondPage[0].ArtifactID != second.ArtifactID || next != nil {
		t.Fatalf("second page items=%#v next=%v", secondPage, next)
	}
}

func TestStoreListArtifactsRejectsInvalidCursor(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.ListArtifacts(ListFilter{Cursor: "cursor_jobs_list_000001"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("invalid cursor error = %v", err)
	}
	if _, _, err := store.ListArtifacts(ListFilter{Cursor: artifactListCursor(1)}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("past-end cursor error = %v", err)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

func registerTestArtifact(t *testing.T, store *Store, name, producerRef, ownerSubjectID string, body []byte) contracts.Artifact {
	t.Helper()
	checksum, digest := checksumAndDigest(body)
	size := int64(len(body))
	session, _, err := store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:             name,
		MediaType:        "text/plain",
		ProducerRef:      producerRef,
		OwnerSubjectID:   ownerSubjectID,
		ExpectedSize:     &size,
		ExpectedChecksum: checksum,
	}, "create-"+name)
	if err != nil {
		t.Fatalf("create upload %s: %v", name, err)
	}
	if _, err := store.PutContent(session.UploadID, ContentUpload{
		Body:          body,
		ContentType:   "text/plain",
		ContentLength: strconv.FormatInt(size, 10),
		Digest:        digest,
	}, "content-"+name); err != nil {
		t.Fatalf("put content %s: %v", name, err)
	}
	artifact, _, err := store.CompleteUpload(session.UploadID, contracts.CompleteArtifactUploadRequest{
		Checksum: checksum,
		Size:     size,
	}, "complete-"+name)
	if err != nil {
		t.Fatalf("complete upload %s: %v", name, err)
	}
	return artifact
}

func validContentUpload(body []byte, mediaType string) ContentUpload {
	_, digest := checksumAndDigest(body)
	return ContentUpload{
		Body:          body,
		ContentType:   mediaType,
		ContentLength: strconv.FormatInt(int64(len(body)), 10),
		Digest:        digest,
	}
}
