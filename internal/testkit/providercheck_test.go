package testkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pacp/internal/contracts"
)

func TestCheckProviderManifestAndHealth(t *testing.T) {
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

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{BaseURL: server.URL})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
	if len(report.Checks) != 3 {
		t.Fatalf("checks = %#v", report.Checks)
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
			invoked = true
			var req contracts.ProviderInvokeRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode invoke: %v", err)
			}
			if req.Input["message"] != "hello" {
				if _, exists := req.Input["message"]; !exists {
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
