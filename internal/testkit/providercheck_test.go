package testkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pacp/internal/contracts"
)

func TestCheckProviderManifestAndHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Request-ID") != "req_test" {
			t.Fatalf("X-Request-ID = %q", r.Header.Get("X-Request-ID"))
		}
		switch r.URL.Path {
		case "/v1/provider/manifest":
			writeTestEnvelope(t, w, http.StatusOK, testProviderManifest(serverURL(r)))
		case "/v1/provider/health":
			writeTestEnvelope(t, w, http.StatusOK, map[string]any{"status": "healthy"})
		case "/v1/provider/metrics":
			writeTestEnvelope(t, w, http.StatusOK, testProviderMetrics(nil))
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{BaseURL: server.URL, RequestID: "req_test"})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
	if len(report.Checks) != 3 {
		t.Fatalf("checks = %#v", report.Checks)
	}
}

func TestCheckProviderFailsWhenEnvelopeMetaMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/provider/manifest" {
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"data":  testProviderManifest(serverURL(r)),
			"links": map[string]any{},
		}); err != nil {
			t.Fatalf("encode envelope: %v", err)
		}
	}))
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{BaseURL: server.URL})
	if report.Passed() || len(report.Checks) != 1 || report.Checks[0].Name != "provider.manifest" {
		t.Fatalf("report = %#v", report)
	}
	if !strings.Contains(report.Checks[0].Error, "meta is required") {
		t.Fatalf("manifest error = %q", report.Checks[0].Error)
	}
}

func TestCheckProviderInvoke(t *testing.T) {
	invoked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer provider-token" {
			writeTestErrorEnvelope(t, w, http.StatusUnauthorized, "unauthorized", "missing token")
			return
		}
		switch r.URL.Path {
		case "/v1/provider/manifest":
			writeTestEnvelope(t, w, http.StatusOK, testProviderManifest(serverURL(r)))
		case "/v1/provider/health":
			writeTestEnvelope(t, w, http.StatusOK, map[string]any{"status": "healthy"})
		case "/v1/provider/capabilities/cap_echo/invoke":
			if r.Header.Get("X-Request-ID") != "req_test" {
				t.Fatalf("invoke X-Request-ID = %q", r.Header.Get("X-Request-ID"))
			}
			invoked = true
			var req contracts.ProviderInvokeRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode invoke: %v", err)
			}
			if req.Context.RequestID != "req_test" {
				t.Fatalf("invoke context request id = %q", req.Context.RequestID)
			}
			if req.Input["message"] != "hello" {
				if _, exists := req.Input["message"]; !exists {
					if req.Context.RequestID != "req_test" {
						t.Fatalf("invalid input context request id = %q", req.Context.RequestID)
					}
					writeTestErrorEnvelope(t, w, http.StatusBadRequest, "validation_failed", "message is required")
					return
				}
				t.Fatalf("input = %#v", req.Input)
			}
			writeTestEnvelope(t, w, http.StatusOK, contracts.ProviderInvokeResponse{
				Output: map[string]any{"reply": "hello"},
			})
		case "/v1/provider/metrics":
			samples := []contracts.MetricSample{}
			if invoked {
				samples = append(samples, contracts.CountMetric("provider_invocations_total", 1, map[string]string{
					"capability_id": "cap_echo",
					"service_id":    "svc_test_provider",
					"status":        "success",
				}))
			}
			writeTestEnvelope(t, w, http.StatusOK, testProviderMetrics(samples))
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:      server.URL,
		CapabilityID: "cap_echo",
		Input:        map[string]any{"message": "hello"},
		Credential:   "Bearer provider-token",
		RequestID:    "req_test",
	})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
	if !invoked {
		t.Fatal("provider was not invoked")
	}
	if len(report.Checks) != 5 {
		t.Fatalf("checks = %#v", report.Checks)
	}
}

func TestCheckProviderReportsInvalidInvokeInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/provider/manifest":
			writeTestEnvelope(t, w, http.StatusOK, testProviderManifest(serverURL(r)))
		case "/v1/provider/health":
			writeTestEnvelope(t, w, http.StatusOK, map[string]any{"status": "healthy"})
		case "/v1/provider/metrics":
			writeTestEnvelope(t, w, http.StatusOK, testProviderMetrics(nil))
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:      server.URL,
		CapabilityID: "cap_echo",
		Input:        map[string]any{"message": 12},
	})
	if report.Passed() {
		t.Fatalf("report passed unexpectedly: %#v", report)
	}
	if len(report.Checks) != 4 || report.Checks[2].Name != "provider.invoke" {
		t.Fatalf("checks = %#v", report.Checks)
	}
	if report.Checks[2].Error == "" {
		t.Fatalf("invoke error missing: %#v", report.Checks[2])
	}
}

func TestCheckProviderValidatesArtifactMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/provider/manifest":
			manifest := testProviderManifest(serverURL(r))
			manifest.Capabilities[0].ArtifactHints = []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}}
			writeTestEnvelope(t, w, http.StatusOK, manifest)
		case "/v1/provider/health":
			writeTestEnvelope(t, w, http.StatusOK, map[string]any{"status": "healthy"})
		case "/v1/provider/capabilities/cap_echo/invoke":
			var req contracts.ProviderInvokeRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode invoke: %v", err)
			}
			if _, exists := req.Input["message"]; !exists {
				writeTestErrorEnvelope(t, w, http.StatusBadRequest, "validation_failed", "message is required")
				return
			}
			writeTestEnvelope(t, w, http.StatusOK, contracts.ProviderInvokeResponse{
				Output:    map[string]any{"reply": "hello"},
				Artifacts: []contracts.ProviderArtifact{{Name: "missing-media-type.txt"}},
			})
		case "/v1/provider/metrics":
			writeTestEnvelope(t, w, http.StatusOK, testProviderMetrics([]contracts.MetricSample{
				contracts.CountMetric("provider_invocations_total", 1, map[string]string{
					"capability_id": "cap_echo",
					"service_id":    "svc_test_provider",
					"status":        "success",
				}),
			}))
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:      server.URL,
		CapabilityID: "cap_echo",
		Input:        map[string]any{"message": "hello"},
		RequestID:    "req_test",
	})
	if report.Passed() {
		t.Fatalf("report passed unexpectedly: %#v", report)
	}
	if !providerCheckFailed(report, "provider.artifact_metadata") {
		t.Fatalf("artifact metadata failure missing: %#v", report.Checks)
	}
}

func TestCheckProviderExpectedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/provider/capabilities/cap_fail/invoke" {
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
		writeTestErrorEnvelope(t, w, http.StatusServiceUnavailable, "provider_unavailable", "backend is down")
	}))
	defer server.Close()

	report := CheckProviderExpectedError(context.Background(), server.Client(), ProviderExpectedErrorOptions{
		BaseURL:        server.URL,
		CapabilityID:   "cap_fail",
		WantHTTPStatus: http.StatusServiceUnavailable,
		WantCode:       "provider_unavailable",
		RequestID:      "req_test",
	})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
}

func testProviderMetrics(samples []contracts.MetricSample) contracts.ComponentMetrics {
	metrics := contracts.NewComponentMetrics("provider", samples)
	metrics.CollectedAt = "2026-06-08T00:00:00Z"
	return metrics
}

func testProviderManifest(endpoint string) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_test_provider",
			Name:         "Test Provider",
			Description:  "Provider used by the contract test kit.",
			Version:      "v1",
			ProviderKind: "test",
		},
		Provider: contracts.Provider{
			Endpoint:   endpoint,
			HealthPath: "/v1/provider/health",
		},
		Capabilities: []contracts.Capability{{
			ID:            "cap_echo",
			Name:          "Echo",
			Description:   "Echoes a message.",
			ExecutionMode: "sync",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"message"},
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
			},
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"reply"},
				"properties": map[string]any{
					"reply": map[string]any{"type": "string"},
				},
			},
			Examples:    []map[string]any{},
			SideEffects: "none",
			TimeoutHint: "30s",
		}},
	}
}

func writeTestEnvelope(t *testing.T, w http.ResponseWriter, status int, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"ok":    status >= 200 && status < 300,
		"data":  data,
		"links": map[string]any{},
		"meta":  map[string]any{"request_id": "req_test", "schema_version": "v1"},
	}); err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
}

func writeTestErrorEnvelope(t *testing.T, w http.ResponseWriter, status int, code, message string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"ok":    false,
		"error": map[string]any{"code": code, "message": message, "retryable": false},
		"links": map[string]any{},
		"meta":  map[string]any{"request_id": "req_test", "schema_version": "v1"},
	}); err != nil {
		t.Fatalf("encode error envelope: %v", err)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func providerCheckFailed(report ProviderCheckReport, name string) bool {
	for _, check := range report.Checks {
		if check.Name == name && !check.OK {
			return true
		}
	}
	return false
}
