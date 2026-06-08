package leases

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerLeaseLifecycle(t *testing.T) {
	handler := NewHandler(NewStore())

	resource := doJSON(t, handler, http.MethodPost, "/v1/resources", map[string]any{
		"resource_id":  "res_gpu_0",
		"selector":     "gpu",
		"display_name": "Linux GPU",
		"status":       "available",
	}, nil)
	if resource["resource_id"] != "res_gpu_0" {
		t.Fatalf("resource response = %#v", resource)
	}

	first := doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":              "job_1",
		"resource_selector":         "gpu",
		"heartbeat_timeout_seconds": 60,
	}, nil)
	if first["state"] != "granted" {
		t.Fatalf("first lease request = %#v", first)
	}
	lease := first["lease"].(map[string]any)
	leaseID := lease["lease_id"].(string)

	second := doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_2",
		"resource_selector": "gpu",
	}, nil)
	if second["state"] != "pending" || second["queue_position"].(float64) != 1 {
		t.Fatalf("second lease request = %#v", second)
	}

	heartbeat := doJSON(t, handler, http.MethodPost, "/v1/leases/"+leaseID+"/heartbeat", map[string]any{
		"holder_id": "job_1",
	}, nil)
	if heartbeat["holder_id"] != "job_1" {
		t.Fatalf("heartbeat = %#v", heartbeat)
	}

	released := doJSON(t, handler, http.MethodPost, "/v1/leases/"+leaseID+"/release", map[string]any{
		"holder_id": "job_1",
		"reason":    "job completed",
	}, map[string]string{
		"Idempotency-Key":    "release-http-1",
		"X-Actor-Subject-ID": "sub_runner",
	})
	if released["released_by"] != "sub_runner" || released["release_reason"] != "job completed" {
		t.Fatalf("released = %#v", released)
	}

	secondStatus := doJSON(t, handler, http.MethodGet, "/v1/lease-requests/"+second["request_id"].(string), nil, nil)
	if secondStatus["state"] != "granted" {
		t.Fatalf("second was not granted after release = %#v", secondStatus)
	}

	inspection := doJSON(t, handler, http.MethodGet, "/v1/resources/res_gpu_0/inspection", nil, nil)
	if inspection["active_lease"] == nil {
		t.Fatalf("inspection missing active lease = %#v", inspection)
	}
}

func TestHandlerErrorsUseStableEnvelopes(t *testing.T) {
	handler := NewHandler(NewStore())
	data := doJSONStatus(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_1",
		"resource_selector": "gpu",
	}, nil, http.StatusConflict)
	if data["ok"].(bool) {
		t.Fatalf("expected error envelope = %#v", data)
	}
	errObj := data["error"].(map[string]any)
	if errObj["code"] != "resource_unavailable" || errObj["retryable"] != true {
		t.Fatalf("error object = %#v", errObj)
	}
}

func TestHandlerHealth(t *testing.T) {
	handler := NewHandler(NewStore())
	data := doJSON(t, handler, http.MethodGet, "/v1/leases/health", nil, nil)
	details := data["details"].(map[string]any)
	if data["status"] != "healthy" || details["component"] != "leases" {
		t.Fatalf("health = %#v", data)
	}
	if details["store_backend"] != "memory" || details["resource_count"] != float64(0) || details["queue_depth"] != float64(0) {
		t.Fatalf("health = %#v", data)
	}
}

func TestHandlerMetricsReportsQueueDepth(t *testing.T) {
	handler := NewHandler(NewStore())
	_ = doJSON(t, handler, http.MethodPost, "/v1/resources", map[string]any{
		"resource_id": "res_gpu_0",
		"selector":    "gpu",
		"status":      "available",
	}, nil)
	_ = doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_1",
		"resource_selector": "gpu",
	}, nil)
	_ = doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_2",
		"resource_selector": "gpu",
	}, nil)

	data := doJSON(t, handler, http.MethodGet, "/v1/leases/metrics", nil, nil)
	if data["component"] != "leases" {
		t.Fatalf("metrics = %#v", data)
	}
	assertMetric(t, data, "lease_queue_depth", map[string]string{"selector": "gpu"}, 1)
	assertMetric(t, data, "http_requests_total", map[string]string{"method": "POST", "route_group": "/v1/lease-requests", "status_class": "2xx"}, 2)
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any, headers map[string]string) map[string]any {
	t.Helper()
	envelope := doJSONStatus(t, handler, method, path, body, headers, successStatus(method, path))
	if !envelope["ok"].(bool) {
		t.Fatalf("error response for %s %s: %#v", method, path, envelope)
	}
	return envelope["data"].(map[string]any)
}

func doJSONStatus(t *testing.T, handler http.Handler, method, path string, body any, headers map[string]string, wantStatus int) map[string]any {
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

func successStatus(method, path string) int {
	if method == http.MethodPost && (path == "/v1/resources" || path == "/v1/lease-requests") {
		return http.StatusCreated
	}
	return http.StatusOK
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
