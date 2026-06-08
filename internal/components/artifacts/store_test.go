package artifacts

import (
	"errors"
	"os"
	"path/filepath"
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

	list := store.ListArtifacts(ListFilter{ProducerRef: "job_1"})
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

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

func validContentUpload(body []byte, mediaType string) ContentUpload {
	_, digest := checksumAndDigest(body)
	return ContentUpload{
		Body:          body,
		ContentType:   mediaType,
		ContentLength: "4",
		Digest:        digest,
	}
}
