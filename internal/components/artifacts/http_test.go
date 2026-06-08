package artifacts

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
