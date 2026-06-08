package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pacp/internal/contracts"
)

func TestRouteGroupNormalizesPublicIDs(t *testing.T) {
	tests := map[string]string{
		"/v1/jobs/job_000001/claim":                  "/v1/jobs/{job_id}/claim",
		"/v1/agent/jobs/job_000001/logs":             "/v1/agent/jobs/{job_id}/logs",
		"/v1/tools/cap_image_generate_gpu/invoke":    "/v1/tools/{capability_id}/invoke",
		"/v1/catalog/capabilities/cap_image/route":   "/v1/catalog/capabilities/{capability_id}/route",
		"/v1/node/services/svc_comfyui_gpu/start":    "/v1/node/services/{service_id}/start",
		"/v1/artifact-uploads/upload_000001/content": "/v1/artifact-uploads/{upload_id}/content",
		"/v1/artifacts/art_000001/content":           "/v1/artifacts/{artifact_id}/content",
		"/v1/artifacts/register-local":               "/v1/artifacts/register-local",
		"/v1/lease-requests/lease_req_000001/cancel": "/v1/lease-requests/{request_id}/cancel",
		"/v1/api-keys/key_000001/revoke":             "/v1/api-keys/{key_id}/revoke",
	}
	for path, want := range tests {
		if got := RouteGroup(path); got != want {
			t.Fatalf("RouteGroup(%q) = %q want %q", path, got, want)
		}
	}
}

func TestHTTPMetricsRecordCountsErrorsAndLatency(t *testing.T) {
	metrics := NewHTTPMetrics()
	metrics.Observe(http.MethodGet, "/v1/jobs/job_000001", http.StatusOK, 100*time.Millisecond)
	metrics.Observe(http.MethodGet, "/v1/jobs/job_000002", http.StatusNotFound, 300*time.Millisecond)

	samples := metrics.Samples()
	assertSample(t, samples, "http_requests_total", map[string]string{
		"method": "GET", "route_group": "/v1/jobs/{job_id}", "status_class": "2xx",
	}, 1)
	assertSample(t, samples, "http_requests_total", map[string]string{
		"method": "GET", "route_group": "/v1/jobs/{job_id}", "status_class": "4xx",
	}, 1)
	assertSample(t, samples, "http_errors_total", map[string]string{
		"method": "GET", "route_group": "/v1/jobs/{job_id}", "status_class": "4xx",
	}, 1)
	assertSample(t, samples, "http_request_duration_seconds_avg", map[string]string{
		"method": "GET", "route_group": "/v1/jobs/{job_id}",
	}, 0.2)
}

func TestHTTPMetricsRecordWrapsHandler(t *testing.T) {
	metrics := NewHTTPMetrics()
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/cap_echo/invoke", nil)
	rec := httptest.NewRecorder()

	metrics.Record(rec, req, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	assertSample(t, metrics.Samples(), "http_requests_total", map[string]string{
		"method": "POST", "route_group": "/v1/tools/{capability_id}/invoke", "status_class": "2xx",
	}, 1)
}

func assertSample(t *testing.T, samples []contracts.MetricSample, name string, labels map[string]string, value float64) {
	t.Helper()
	for _, sample := range samples {
		if sample.Name != name {
			continue
		}
		if !sampleLabelsMatch(sample.Labels, labels) {
			continue
		}
		if sample.Value != value {
			t.Fatalf("sample %s value=%v want=%v", name, sample.Value, value)
		}
		return
	}
	t.Fatalf("sample %s labels=%#v not found in %#v", name, labels, samples)
}

func sampleLabelsMatch(actual, want map[string]string) bool {
	if len(actual) != len(want) {
		return false
	}
	for key, value := range want {
		if actual[key] != value {
			return false
		}
	}
	return true
}
