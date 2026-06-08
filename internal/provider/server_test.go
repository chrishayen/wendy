package provider

import (
	"bytes"
	"context"
	"encoding/json"
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
