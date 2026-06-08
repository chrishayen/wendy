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

func TestServerOptionalAuthProtectsInvokeAndContent(t *testing.T) {
	contentStore := NewContentStore()
	ref, err := contentStore.Put(ContentPut{
		ContentRef: "pcr_auth_text",
		JobID:      "job_auth",
		Name:       "result.txt",
		MediaType:  "text/plain",
		Body:       []byte("protected provider content"),
	})
	if err != nil {
		t.Fatalf("put content: %v", err)
	}
	server, err := NewServerWithOptions(testManifest(), map[string]CapabilityHandler{
		"cap_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{Output: map[string]any{"message": req.Input["message"]}}, nil
		},
	}, WithContentStore(contentStore), WithAuthCredential("provider-token"))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	doJSON(t, server, http.MethodGet, "/v1/provider/manifest", nil, http.StatusOK)
	doJSON(t, server, http.MethodGet, "/v1/provider/health", nil, http.StatusOK)

	unauthorized := doJSONEnvelope(t, server, http.MethodPost, "/v1/provider/capabilities/cap_echo/invoke", map[string]any{
		"input": map[string]any{"message": "hello"},
	}, http.StatusUnauthorized)
	errObj := unauthorized["error"].(map[string]any)
	if errObj["code"] != "unauthorized" {
		t.Fatalf("unauthorized error = %#v", errObj)
	}

	response := doJSONWithHeaders(t, server, http.MethodPost, "/v1/provider/capabilities/cap_echo/invoke", map[string]any{
		"input": map[string]any{"message": "hello"},
	}, map[string]string{"Authorization": "Bearer provider-token"}, http.StatusOK)
	output := response["output"].(map[string]any)
	if output["message"] != "hello" {
		t.Fatalf("authorized response = %#v", response)
	}

	missingAuth := httptest.NewRequest(http.MethodGet, "/v1/provider/artifacts/"+ref.ContentRef+"/content", nil)
	missingAuthRec := httptest.NewRecorder()
	server.ServeHTTP(missingAuthRec, missingAuth)
	if missingAuthRec.Code != http.StatusUnauthorized {
		t.Fatalf("content without auth status=%d body=%s", missingAuthRec.Code, missingAuthRec.Body.String())
	}

	withAuth := httptest.NewRequest(http.MethodGet, "/v1/provider/artifacts/"+ref.ContentRef+"/content", nil)
	withAuth.Header.Set("Authorization", "Bearer provider-token")
	withAuthRec := httptest.NewRecorder()
	server.ServeHTTP(withAuthRec, withAuth)
	if withAuthRec.Code != http.StatusOK {
		t.Fatalf("content with auth status=%d body=%s", withAuthRec.Code, withAuthRec.Body.String())
	}
	if withAuthRec.Header().Get("Content-Type") != "text/plain" || withAuthRec.Body.String() != "protected provider content" {
		t.Fatalf("content headers=%#v body=%q", withAuthRec.Header(), withAuthRec.Body.String())
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

func TestServerExposesAsyncStyleAcceptedHandle(t *testing.T) {
	manifest := testManifest()
	manifest.Capabilities = append(manifest.Capabilities, contracts.Capability{
		ID:            "cap_accept",
		Name:          "Accept",
		Description:   "Accept provider-local async work and return a handle as output.",
		Tags:          []string{"test", "async"},
		ExecutionMode: "async",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"handle_id", "status"},
			"properties": map[string]any{
				"handle_id":  map[string]any{"type": "string"},
				"status":     map[string]any{"type": "string"},
				"status_url": map[string]any{"type": "string"},
			},
		},
		Examples:      []map[string]any{},
		SideEffects:   "external",
		ResourceHints: []contracts.ResourceHint{},
		ArtifactHints: []contracts.ArtifactHint{},
		TimeoutHint:   "30s",
	})
	server, err := NewServer(manifest, map[string]CapabilityHandler{
		"cap_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{Output: map[string]any{"message": req.Input["message"]}}, nil
		},
		"cap_accept": AsyncHandler(func(ctx context.Context, req contracts.ProviderInvokeRequest) (AcceptedHandle, error) {
			return AcceptedHandle{
				HandleID:  "provider_run_123",
				Status:    "accepted",
				StatusURL: "https://provider.local/runs/provider_run_123",
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	response := doJSON(t, server, http.MethodPost, "/v1/provider/capabilities/cap_accept/invoke", map[string]any{
		"input": map[string]any{},
	}, http.StatusOK)
	output := response["output"].(map[string]any)
	if output["handle_id"] != "provider_run_123" || output["status"] != "accepted" || output["status_url"] == "" {
		t.Fatalf("invoke response = %#v", response)
	}
}

func TestServerExposesArtifactProducingHandler(t *testing.T) {
	manifest := testManifest()
	manifest.Capabilities = append(manifest.Capabilities, contracts.Capability{
		ID:            "cap_artifact_helper",
		Name:          "Artifact Helper",
		Description:   "Return a provider artifact through the SDK artifact handler.",
		Tags:          []string{"test", "artifact"},
		ExecutionMode: "sync",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"artifact_count"},
			"properties": map[string]any{
				"artifact_count": map[string]any{"type": "integer"},
			},
		},
		Examples:      []map[string]any{},
		SideEffects:   "write",
		ResourceHints: []contracts.ResourceHint{},
		ArtifactHints: []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}},
		TimeoutHint:   "30s",
	})
	server, err := NewServer(manifest, map[string]CapabilityHandler{
		"cap_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{Output: map[string]any{"message": req.Input["message"]}}, nil
		},
		"cap_artifact_helper": ArtifactHandler(func(ctx context.Context, req contracts.ProviderInvokeRequest) (ArtifactResult, error) {
			return ArtifactResult{
				Output: map[string]any{"artifact_count": 1},
				Artifacts: []contracts.ProviderArtifact{{
					Name:          "result.txt",
					MediaType:     "text/plain",
					ContentBase64: "aGVsbG8=",
					Checksum:      "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
				}},
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	response := doJSON(t, server, http.MethodPost, "/v1/provider/capabilities/cap_artifact_helper/invoke", map[string]any{
		"input": map[string]any{},
	}, http.StatusOK)
	output := response["output"].(map[string]any)
	artifacts := response["artifacts"].([]any)
	if output["artifact_count"] != float64(1) || len(artifacts) != 1 {
		t.Fatalf("invoke response = %#v", response)
	}
}

func TestServerExposesStoredProviderContentRefs(t *testing.T) {
	manifest := testManifest()
	manifest.Capabilities = append(manifest.Capabilities, contracts.Capability{
		ID:            "cap_content_helper",
		Name:          "Content Helper",
		Description:   "Return a provider content ref through the generic SDK server.",
		Tags:          []string{"test", "artifact"},
		ExecutionMode: "sync",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"content_count"},
			"properties": map[string]any{
				"content_count": map[string]any{"type": "integer"},
			},
		},
		Examples:      []map[string]any{},
		SideEffects:   "write",
		ResourceHints: []contracts.ResourceHint{},
		ArtifactHints: []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}},
		TimeoutHint:   "30s",
	})
	contentStore := NewContentStore()
	body := []byte("provider content bytes")
	server, err := NewServerWithContentStore(manifest, map[string]CapabilityHandler{
		"cap_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{Output: map[string]any{"message": req.Input["message"]}}, nil
		},
		"cap_content_helper": ArtifactHandler(func(ctx context.Context, req contracts.ProviderInvokeRequest) (ArtifactResult, error) {
			ref, err := contentStore.Put(ContentPut{
				ContentRef: "pcr_text",
				JobID:      req.Context.JobID,
				Name:       "result.txt",
				MediaType:  "text/plain",
				Body:       body,
			})
			if err != nil {
				return ArtifactResult{}, err
			}
			return ArtifactResult{
				Output:      map[string]any{"content_count": 1},
				ContentRefs: []contracts.ProviderContentRef{ref},
			}, nil
		}),
	}, contentStore)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	response := doJSON(t, server, http.MethodPost, "/v1/provider/capabilities/cap_content_helper/invoke", map[string]any{
		"input":   map[string]any{},
		"context": map[string]any{"job_id": "job_content"},
	}, http.StatusOK)
	contentRefs := response["content_refs"].([]any)
	if len(contentRefs) != 1 {
		t.Fatalf("content refs = %#v", response)
	}
	ref := contentRefs[0].(map[string]any)
	if ref["content_ref"] != "pcr_text" || ref["name"] != "result.txt" || ref["media_type"] != "text/plain" || ref["size"] != float64(len(body)) {
		t.Fatalf("content ref = %#v", ref)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/provider/artifacts/pcr_text/content", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("content status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "text/plain" || rec.Header().Get("Digest") == "" || !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatalf("content headers=%#v body=%q", rec.Header(), rec.Body.String())
	}

	missing := doJSONEnvelope(t, server, http.MethodGet, "/v1/provider/artifacts/pcr_missing/content", nil, http.StatusNotFound)
	errObj := missing["error"].(map[string]any)
	if errObj["code"] != "provider_content_not_found" {
		t.Fatalf("missing content error = %#v", errObj)
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

func doJSONWithHeaders(t *testing.T, handler http.Handler, method, path string, body any, headers map[string]string, wantStatus int) map[string]any {
	t.Helper()
	envelope := doJSONEnvelopeWithHeaders(t, handler, method, path, body, headers, wantStatus)
	if !envelope["ok"].(bool) {
		t.Fatalf("error response for %s %s: %#v", method, path, envelope)
	}
	return envelope["data"].(map[string]any)
}

func doJSONEnvelope(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	return doJSONEnvelopeWithHeaders(t, handler, method, path, body, nil, wantStatus)
}

func doJSONEnvelopeWithHeaders(t *testing.T, handler http.Handler, method, path string, body any, headers map[string]string, wantStatus int) map[string]any {
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
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope
}
