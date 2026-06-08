package artifacts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/testkit"
)

func TestHandlerUploadLifecycleAndContentRead(t *testing.T) {
	store := newTestStore(t)
	handler := NewHandler(store)
	body := []byte("artifact bytes")
	checksum, digest := checksumAndDigest(body)

	upload := doJSON(t, handler, http.MethodPost, "/v1/artifact-uploads", map[string]any{
		"name":              "result.txt",
		"media_type":        "text/plain",
		"producer_ref":      "job_1",
		"owner_subject_id":  "sub_agent",
		"expected_size":     len(body),
		"expected_checksum": checksum,
	}, map[string]string{"Idempotency-Key": "create-http-1"}, http.StatusCreated)
	uploadID := upload["upload_id"].(string)
	if upload["state"] != "created" {
		t.Fatalf("upload = %#v", upload)
	}

	received := doBytes(t, handler, http.MethodPut, "/v1/artifact-uploads/"+uploadID+"/content", body, map[string]string{
		"Idempotency-Key": "content-http-1",
		"Content-Type":    "text/plain",
		"Content-Length":  "14",
		"Digest":          digest,
	}, http.StatusOK)
	if received["state"] != "received" {
		t.Fatalf("received = %#v", received)
	}

	artifact := doJSON(t, handler, http.MethodPost, "/v1/artifact-uploads/"+uploadID+"/complete", map[string]any{
		"checksum": checksum,
		"size":     len(body),
	}, map[string]string{"Idempotency-Key": "complete-http-1"}, http.StatusCreated)
	artifactID := artifact["artifact_id"].(string)
	if artifact["checksum"] != checksum {
		t.Fatalf("artifact = %#v", artifact)
	}

	list := doJSON(t, handler, http.MethodGet, "/v1/artifacts?producer_ref=job_1", nil, nil, http.StatusOK)
	items := list["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["artifact_id"] != artifactID {
		t.Fatalf("list = %#v", list)
	}

	policy := doJSON(t, handler, http.MethodGet, "/v1/artifacts/"+artifactID+"/policy-context", nil, nil, http.StatusOK)
	if policy["owner_subject_id"] != "sub_agent" || policy["producer_ref"] != "job_1" {
		t.Fatalf("policy = %#v", policy)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+artifactID+"/content", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("content status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "text/plain" || rec.Header().Get("Digest") != digest {
		t.Fatalf("content headers = %#v", rec.Header())
	}
	if rec.Body.String() != string(body) {
		t.Fatalf("content body = %q", rec.Body.String())
	}
	metrics := doJSON(t, handler, http.MethodGet, "/v1/artifacts/metrics", nil, nil, http.StatusOK)
	if metrics["component"] != "artifacts" {
		t.Fatalf("metrics = %#v", metrics)
	}
	assertMetric(t, metrics, "artifacts_total", nil, 1)
	assertMetric(t, metrics, "artifact_registrations_total", nil, 1)
	assertMetric(t, metrics, "artifact_content_retrievals_total", nil, 1)
	assertMetric(t, metrics, "artifact_uploads_by_state", map[string]string{"state": "completed"}, 1)
	assertMetric(t, metrics, "http_requests_total", map[string]string{"method": "GET", "route_group": "/v1/artifacts/{artifact_id}/content", "status_class": "2xx"}, 1)
}

func TestHandlerMissingIdempotencyEnvelope(t *testing.T) {
	handler := NewHandler(newTestStore(t))
	envelope := doJSONEnvelope(t, handler, http.MethodPost, "/v1/artifact-uploads", map[string]any{
		"name":             "result.txt",
		"media_type":       "text/plain",
		"owner_subject_id": "sub_agent",
	}, nil, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "missing_idempotency_key" {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestHandlerHealth(t *testing.T) {
	handler := NewHandler(newTestStore(t))
	data := doJSON(t, handler, http.MethodGet, "/v1/artifacts/health", nil, nil, http.StatusOK)
	details := data["details"].(map[string]any)
	if data["status"] != "healthy" || details["component"] != "artifacts" {
		t.Fatalf("health = %#v", data)
	}
	if details["store_backend"] != "memory" || details["content_backend"] != "local_fs" || details["artifact_count"] != float64(0) {
		t.Fatalf("health = %#v", data)
	}
}

func TestHandlerReplaysS003ArtifactStoreFixtures(t *testing.T) {
	scenario, err := testkit.LoadScenario(filepath.Join("..", "..", "..", "testdata", "contract-sim"), filepath.Join("fixtures", "S003", "manifest.json"))
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	pkg, ok := testkit.FindPackage(scenario, "c07-artifact-store")
	if !ok {
		t.Fatalf("c07 fixture package not found")
	}

	tests := []struct {
		fixtureID string
		seed      func(*testing.T, *Store, testkit.FixturePackage)
	}{
		{"artifact_upload_content_ok", seedS003CreatedUpload},
		{"artifact_policy_context_ok", seedS003CompletedArtifact},
		{"artifact_metadata_ok", seedS003CompletedArtifact},
		{"artifact_list_by_producer_ok", seedS003CompletedArtifact},
		{"artifact_content_ok", seedS003CompletedArtifact},
		{"artifact_upload_content_missing_headers", seedS003CreatedUpload},
		{"artifact_upload_content_bad_digest", seedS003CreatedUpload},
		{"artifact_upload_content_length_mismatch", seedS003CreatedUpload},
		{"artifact_upload_session_completed", seedS003CompletedArtifact},
		{"artifact_upload_missing_idempotency", nil},
		{"artifact_upload_create_idempotency_replay", seedS003CreateIdempotency},
		{"artifact_upload_create_idempotency_conflict", seedS003CreateIdempotency},
		{"artifact_expired_upload", seedS003ExpiredUpload},
		{"artifact_checksum_mismatch", seedS003ReceivedUpload},
		{"artifact_size_mismatch", seedS003ReceivedUpload},
		{"artifact_policy_context_missing", nil},
		{"artifact_metadata_missing", nil},
		{"artifact_content_missing", nil},
		{"artifact_list_empty", nil},
		{"artifact_upload_content_missing_idempotency", seedS003CreatedUpload},
		{"artifact_upload_complete_missing_idempotency", seedS003ReceivedUpload},
		{"artifact_upload_content_idempotency_replay", seedS003ContentIdempotency},
		{"artifact_upload_content_idempotency_conflict", seedS003ContentIdempotency},
		{"artifact_upload_complete_idempotency_replay", seedS003CompleteIdempotency},
		{"artifact_upload_complete_idempotency_conflict", seedS003CompleteIdempotency},
		{"artifact_upload_complete_duplicate_without_matching_key", seedS003CompletedArtifact},
	}
	for _, test := range tests {
		t.Run(test.fixtureID, func(t *testing.T) {
			store := newTestStore(t)
			store.SetClock(fixedS003Time("2026-06-05T20:00:00Z"))
			if test.seed != nil {
				test.seed(t, store, pkg)
			}
			if _, err := testkit.ReplayHTTPFixture(NewHandler(store), pkg, test.fixtureID); err != nil {
				t.Fatalf("replay %s: %v", test.fixtureID, err)
			}
		})
	}
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any, headers map[string]string, wantStatus int) map[string]any {
	t.Helper()
	envelope := doJSONEnvelope(t, handler, method, path, body, headers, wantStatus)
	if !envelope["ok"].(bool) {
		t.Fatalf("error response for %s %s: %#v", method, path, envelope)
	}
	return envelope["data"].(map[string]any)
}

func doBytes(t *testing.T, handler http.Handler, method, path string, body []byte, headers map[string]string, wantStatus int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode bytes response: %v", err)
	}
	if !envelope["ok"].(bool) {
		t.Fatalf("error response for %s %s: %#v", method, path, envelope)
	}
	return envelope["data"].(map[string]any)
}

func doJSONEnvelope(t *testing.T, handler http.Handler, method, path string, body any, headers map[string]string, wantStatus int) map[string]any {
	t.Helper()
	var raw bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&raw).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &raw)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope
}

func assertMetric(t *testing.T, data map[string]any, name string, labels map[string]string, value float64) {
	t.Helper()
	for _, rawSample := range data["samples"].([]any) {
		sample := rawSample.(map[string]any)
		if sample["name"] != name {
			continue
		}
		if !labelsMatch(sample["labels"], labels) {
			continue
		}
		if sample["value"] != value {
			t.Fatalf("metric %s value=%#v want=%v", name, sample["value"], value)
		}
		return
	}
	t.Fatalf("metric %s labels=%#v not found in %#v", name, labels, data["samples"])
}

func labelsMatch(raw any, want map[string]string) bool {
	if len(want) == 0 {
		return raw == nil
	}
	labels, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range want {
		if labels[key] != value {
			return false
		}
	}
	return true
}

const (
	s003UploadID   = "upload_s003_0001"
	s003ArtifactID = "art_s003_0001"
	s003Checksum   = "sha256:4b5c5c92cec3b23e6a294fc0eea43234ef5126c5a64f4c6c531ac8430ab0b844"
	s003Digest     = "sha-256=S1xcks7Dsj5qKU/A7qQyNO9RJsWmT0xsUxrIQwqwuEQ="
	s003Size       = int64(68)
)

func seedS003CreatedUpload(t *testing.T, store *Store, _ testkit.FixturePackage) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	store.uploads[s003UploadID] = &uploadRecord{
		session:  s003CreatedUpload(),
		metadata: s003Metadata(),
	}
}

func seedS003ReceivedUpload(t *testing.T, store *Store, pkg testkit.FixturePackage) {
	t.Helper()
	body := readS003Body(t, pkg)
	path, err := store.safeUploadPath(s003UploadID)
	if err != nil {
		t.Fatalf("upload path: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write upload body: %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.uploads[s003UploadID] = &uploadRecord{
		session:               s003ReceivedUpload(),
		metadata:              s003Metadata(),
		receivedChecksum:      s003Checksum,
		receivedDigest:        s003Digest,
		receivedPath:          path,
		contentIdempotencyKey: "idem_s003_artifact_upload_content",
	}
}

func seedS003CompletedArtifact(t *testing.T, store *Store, pkg testkit.FixturePackage) {
	t.Helper()
	body := readS003Body(t, pkg)
	path, err := store.safeBlobPath(s003ArtifactID)
	if err != nil {
		t.Fatalf("blob path: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write artifact body: %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.uploads[s003UploadID] = &uploadRecord{
		session:                s003CompletedUpload(),
		metadata:               s003Metadata(),
		receivedChecksum:       s003Checksum,
		receivedDigest:         s003Digest,
		contentIdempotencyKey:  "idem_s003_artifact_upload_content",
		completeIdempotencyKey: "idem_s003_artifact_upload_complete",
	}
	store.artifacts[s003ArtifactID] = &artifactRecord{
		artifact: s003Artifact(),
		path:     path,
		digest:   s003Digest,
	}
}

func seedS003ExpiredUpload(t *testing.T, store *Store, _ testkit.FixturePackage) {
	t.Helper()
	store.SetClock(fixedS003Time("2026-06-05T20:15:01Z"))
	session := s003CreatedUpload()
	session.UploadID = "upload_s003_expired"
	session.Name = "expired.png"
	session.ProducerRef = "job_s003_expired"
	session.Links = uploadLinks(session.UploadID, contracts.ArtifactUploadCreated)
	store.mu.Lock()
	defer store.mu.Unlock()
	store.uploads[session.UploadID] = &uploadRecord{
		session:  session,
		metadata: map[string]any{},
	}
}

func seedS003CreateIdempotency(t *testing.T, store *Store, _ testkit.FixturePackage) {
	t.Helper()
	fp := mustFingerprint(t, s003CreateUploadRequest())
	store.mu.Lock()
	defer store.mu.Unlock()
	store.idempotency["idem_s003_artifact_upload_create"] = idempotentRecord{
		operation:   "upload:create",
		fingerprint: fp,
		response:    s003CreatedUpload(),
		created:     true,
	}
}

func seedS003ContentIdempotency(t *testing.T, store *Store, _ testkit.FixturePackage) {
	t.Helper()
	fp := mustFingerprint(t, map[string]any{
		"upload_id":      s003UploadID,
		"content_type":   "image/png",
		"content_length": "68",
		"digest":         s003Digest,
		"checksum":       s003Checksum,
	})
	store.mu.Lock()
	defer store.mu.Unlock()
	store.idempotency["idem_s003_artifact_upload_content"] = idempotentRecord{
		operation:   "upload:content:" + s003UploadID,
		fingerprint: fp,
		response:    s003ReceivedUpload(),
	}
}

func seedS003CompleteIdempotency(t *testing.T, store *Store, _ testkit.FixturePackage) {
	t.Helper()
	fp := mustFingerprint(t, map[string]any{
		"upload_id": s003UploadID,
		"request": contracts.CompleteArtifactUploadRequest{
			Checksum: s003Checksum,
			Size:     s003Size,
		},
	})
	store.mu.Lock()
	defer store.mu.Unlock()
	store.idempotency["idem_s003_artifact_upload_complete"] = idempotentRecord{
		operation:   "upload:complete:" + s003UploadID,
		fingerprint: fp,
		response:    s003Artifact(),
		created:     true,
	}
}

func s003CreateUploadRequest() contracts.CreateArtifactUploadRequest {
	size := s003Size
	return contracts.CreateArtifactUploadRequest{
		Name:             "job_s003_0001.png",
		MediaType:        "image/png",
		ProducerRef:      "job_s003_0001",
		OwnerSubjectID:   "sub_agent_s003",
		ExpectedSize:     &size,
		ExpectedChecksum: s003Checksum,
		Metadata:         s003Metadata(),
	}
}

func s003CreatedUpload() contracts.ArtifactUploadSession {
	size := s003Size
	return contracts.ArtifactUploadSession{
		UploadID:         s003UploadID,
		State:            contracts.ArtifactUploadCreated,
		Name:             "job_s003_0001.png",
		MediaType:        "image/png",
		ProducerRef:      "job_s003_0001",
		OwnerSubjectID:   "sub_agent_s003",
		ExpectedSize:     &size,
		ExpectedChecksum: s003Checksum,
		ExpiresAt:        "2026-06-05T20:15:00Z",
		Links:            uploadLinks(s003UploadID, contracts.ArtifactUploadCreated),
	}
}

func s003ReceivedUpload() contracts.ArtifactUploadSession {
	session := s003CreatedUpload()
	size := s003Size
	session.State = contracts.ArtifactUploadReceived
	session.ReceivedSize = &size
	session.Links = uploadLinks(s003UploadID, contracts.ArtifactUploadReceived)
	return session
}

func s003CompletedUpload() contracts.ArtifactUploadSession {
	session := s003ReceivedUpload()
	artifactID := s003ArtifactID
	session.State = contracts.ArtifactUploadCompleted
	session.ArtifactID = &artifactID
	session.CompletedAt = "2026-06-05T20:00:45Z"
	session.Links = map[string]any{}
	return session
}

func s003Artifact() contracts.Artifact {
	return contracts.Artifact{
		ArtifactID:     s003ArtifactID,
		Name:           "job_s003_0001.png",
		MediaType:      "image/png",
		Size:           s003Size,
		Checksum:       s003Checksum,
		CreatedAt:      "2026-06-05T20:00:45Z",
		ProducerRef:    "job_s003_0001",
		OwnerSubjectID: "sub_agent_s003",
		Metadata:       s003Metadata(),
		Links:          artifactLinks(s003ArtifactID),
	}
}

func s003Metadata() map[string]any {
	return map[string]any{"capability_id": "cap_image_generate_gpu"}
}

func readS003Body(t *testing.T, pkg testkit.FixturePackage) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(pkg.AbsPath), "provider_png_s003_0001.base64"))
	if err != nil {
		t.Fatalf("read body fixture: %v", err)
	}
	body, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(raw)))
	if err != nil {
		t.Fatalf("decode body fixture: %v", err)
	}
	return body
}

func fixedS003Time(value string) func() time.Time {
	return func() time.Time {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			panic(err)
		}
		return parsed
	}
}

func mustFingerprint(t *testing.T, value any) string {
	t.Helper()
	fp, err := fingerprint(value)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	return fp
}
