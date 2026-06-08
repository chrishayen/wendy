package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"pacp/internal/contracts"
)

func TestServerManifestHealthAndInvoke(t *testing.T) {
	server := newTestProvider(t)
	manifest := doJSON(t, server, http.MethodGet, "/v1/provider/manifest", nil, http.StatusOK)
	service := manifest["service"].(map[string]any)
	if service["id"] != "svc_fake" {
		t.Fatalf("manifest = %#v", manifest)
	}
	health := doJSON(t, server, http.MethodGet, "/v1/provider/health", nil, http.StatusOK)
	if health["status"] != "healthy" {
		t.Fatalf("health = %#v", health)
	}
	response := doJSON(t, server, http.MethodPost, "/v1/provider/capabilities/cap_echo/invoke", map[string]any{
		"input": map[string]any{"message": "hello"},
	}, http.StatusOK)
	output := response["output"].(map[string]any)
	if output["message"] != "hello" {
		t.Fatalf("invoke response = %#v", response)
	}
}

func TestServerRejectsInvalidInput(t *testing.T) {
	server := newTestProvider(t)
	envelope := doJSONEnvelope(t, server, http.MethodPost, "/v1/provider/capabilities/cap_echo/invoke", map[string]any{
		"input": map[string]any{},
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestServerMetricsIncludeInvocationsAndHTTPRequests(t *testing.T) {
	server := newTestProvider(t)
	doJSON(t, server, http.MethodGet, "/v1/provider/health", nil, http.StatusOK)
	doJSON(t, server, http.MethodPost, "/v1/provider/capabilities/cap_echo/invoke", map[string]any{
		"input": map[string]any{"message": "hello"},
	}, http.StatusOK)
	doJSONEnvelope(t, server, http.MethodPost, "/v1/provider/capabilities/cap_echo/invoke", map[string]any{
		"input": map[string]any{},
	}, http.StatusBadRequest)

	metrics := doJSON(t, server, http.MethodGet, "/v1/provider/metrics", nil, http.StatusOK)
	if metrics["component"] != "provider" {
		t.Fatalf("metrics component = %#v", metrics["component"])
	}
	samples := metrics["samples"].([]any)
	assertMetric(t, samples, "provider_invocations_total", map[string]string{
		"service_id":    "svc_fake",
		"capability_id": "cap_echo",
		"status":        "success",
	})
	assertMetric(t, samples, "provider_invocation_errors_total", map[string]string{
		"service_id":    "svc_fake",
		"capability_id": "cap_echo",
		"status":        "error",
		"error_code":    "validation_failed",
	})
	assertMetric(t, samples, "provider_invocation_duration_seconds_avg", map[string]string{
		"service_id":    "svc_fake",
		"capability_id": "cap_echo",
		"status":        "success",
	})
	assertMetric(t, samples, "http_requests_total", map[string]string{
		"method":       "GET",
		"route_group":  "/v1/provider/health",
		"status_class": "2xx",
	})
	assertMetric(t, samples, "http_requests_total", map[string]string{
		"method":       "POST",
		"route_group":  "/v1/provider/capabilities/{capability_id}/invoke",
		"status_class": "4xx",
	})
}

func TestServerMapsHandlerValidationError(t *testing.T) {
	server, err := NewServer(testManifest(), map[string]CapabilityHandler{
		"cap_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: unsafe url", ErrValidation)
		},
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	envelope := doJSONEnvelope(t, server, http.MethodPost, "/v1/provider/capabilities/cap_echo/invoke", map[string]any{
		"input": map[string]any{"message": "hello"},
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestServerMapsHandlerTimeoutError(t *testing.T) {
	server, err := NewServer(testManifest(), map[string]CapabilityHandler{
		"cap_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: backend exceeded route timeout", ErrTimeout)
		},
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	envelope := doJSONEnvelope(t, server, http.MethodPost, "/v1/provider/capabilities/cap_echo/invoke", map[string]any{
		"input": map[string]any{"message": "hello"},
	}, http.StatusGatewayTimeout)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "provider_timeout" || errObj["message"] != "provider invocation timed out" || errObj["retryable"] != true {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestServerPreservesStructuredInvokeError(t *testing.T) {
	server, err := NewServer(testManifest(), map[string]CapabilityHandler{
		"cap_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{}, InvokeError{ErrorObject: contracts.ErrorObject{
				Code:      "validation_failed",
				Message:   "backend input was invalid",
				Retryable: false,
			}}
		},
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	envelope := doJSONEnvelope(t, server, http.MethodPost, "/v1/provider/capabilities/cap_echo/invoke", map[string]any{
		"input": map[string]any{"message": "hello"},
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || errObj["message"] != "backend input was invalid" || errObj["retryable"] != false {
		t.Fatalf("error = %#v", errObj)
	}
}

func newTestProvider(t *testing.T) *Server {
	t.Helper()
	server, err := NewServer(testManifest(), map[string]CapabilityHandler{
		"cap_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{
				Output: map[string]any{"message": req.Input["message"]},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	return server
}

func assertMetric(t *testing.T, samples []any, name string, labels map[string]string) {
	t.Helper()
	for _, rawSample := range samples {
		sample, ok := rawSample.(map[string]any)
		if !ok || sample["name"] != name {
			continue
		}
		rawLabels, _ := sample["labels"].(map[string]any)
		matched := true
		for key, value := range labels {
			if rawLabels[key] != value {
				matched = false
				break
			}
		}
		if matched {
			return
		}
	}
	t.Fatalf("missing metric %s with labels %#v in %#v", name, labels, samples)
}

func testManifest() contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_fake",
			Name:         "Fake Provider",
			Description:  "Fake provider for tests.",
			Version:      "0.1.0",
			ProviderKind: "fake",
			Tags:         []string{"fake"},
		},
		Provider: contracts.Provider{Endpoint: "http://localhost:18088"},
		Capabilities: []contracts.Capability{{
			ID:            "cap_echo",
			Name:          "Echo",
			Description:   "Echo a message.",
			Tags:          []string{"test"},
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
				"required": []any{"message"},
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
			},
			Examples:      []map[string]any{},
			SideEffects:   "none",
			ResourceHints: []contracts.ResourceHint{},
			ArtifactHints: []contracts.ArtifactHint{},
			TimeoutHint:   "30s",
		}},
	}
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	envelope := doJSONEnvelope(t, handler, method, path, body, wantStatus)
	if !envelope["ok"].(bool) {
		t.Fatalf("error response for %s %s: %#v", method, path, envelope)
	}
	return envelope["data"].(map[string]any)
}

func doJSONEnvelope(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) map[string]any {
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
