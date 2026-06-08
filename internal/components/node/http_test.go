package node

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerNodeLifecycle(t *testing.T) {
	handler := NewHandler(newTestStore(t))
	headers := map[string]string{"Authorization": "Bearer token_runner"}

	health := doJSON(t, handler, http.MethodGet, "/v1/node/health", headers, http.StatusOK)
	if health["status"] != "healthy" {
		t.Fatalf("health = %#v", health)
	}
	resources := doJSON(t, handler, http.MethodGet, "/v1/node/resources", headers, http.StatusOK)
	if len(resources["items"].([]any)) != 1 {
		t.Fatalf("resources = %#v", resources)
	}
	service := doJSON(t, handler, http.MethodGet, "/v1/node/services/svc_comfyui_gpu", headers, http.StatusOK)
	if service["status"] != "stopped" {
		t.Fatalf("initial service = %#v", service)
	}
	starting := doJSON(t, handler, http.MethodPost, "/v1/node/services/svc_comfyui_gpu/start", map[string]string{
		"Authorization":   "Bearer token_runner",
		"Idempotency-Key": "start-http-1",
	}, http.StatusAccepted)
	if starting["status"] != "starting" {
		t.Fatalf("start = %#v", starting)
	}
	running := doJSON(t, handler, http.MethodGet, "/v1/node/services/svc_comfyui_gpu", headers, http.StatusOK)
	if running["status"] != "running" {
		t.Fatalf("running = %#v", running)
	}
	stopped := doJSON(t, handler, http.MethodPost, "/v1/node/services/svc_comfyui_gpu/stop", headers, http.StatusAccepted)
	if stopped["status"] != "stopped" {
		t.Fatalf("stopped = %#v", stopped)
	}
	metrics := doJSON(t, handler, http.MethodGet, "/v1/node/metrics", headers, http.StatusOK)
	if metrics["component"] != "node" {
		t.Fatalf("metrics = %#v", metrics)
	}
	assertMetric(t, metrics, "node_service_start_total", map[string]string{"node_id": "node_linux_gpu"}, 1)
	assertMetric(t, metrics, "node_service_stop_total", map[string]string{"node_id": "node_linux_gpu"}, 1)
}

func TestHandlerRejectsUnauthorizedLifecycle(t *testing.T) {
	handler := NewHandler(newTestStore(t))
	envelope := doJSONEnvelope(t, handler, http.MethodPost, "/v1/node/services/svc_comfyui_gpu/start", map[string]string{
		"Authorization": "Bearer token_agent",
	}, http.StatusForbidden)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "forbidden" {
		t.Fatalf("error = %#v", errObj)
	}
}

func doJSON(t *testing.T, handler http.Handler, method, path string, headers map[string]string, wantStatus int) map[string]any {
	t.Helper()
	envelope := doJSONEnvelope(t, handler, method, path, headers, wantStatus)
	if !envelope["ok"].(bool) {
		t.Fatalf("error response for %s %s: %#v", method, path, envelope)
	}
	return envelope["data"].(map[string]any)
}

func doJSONEnvelope(t *testing.T, handler http.Handler, method, path string, headers map[string]string, wantStatus int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
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
