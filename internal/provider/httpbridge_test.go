package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pacp/internal/contracts"
)

func TestHTTPBridgeForwardsProviderInvokeRequest(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/invoke" {
			t.Fatalf("backend request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Backend-Token") != "secret" {
			t.Fatalf("missing backend header: %#v", r.Header)
		}
		if r.Header.Get("X-Request-ID") != "req_bridge" {
			t.Fatalf("X-Request-ID = %q", r.Header.Get("X-Request-ID"))
		}
		var req contracts.ProviderInvokeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode backend request: %v", err)
		}
		if req.Input["message"] != "hello" || req.Context.SubjectID != "sub_agent" || req.Context.RequestID != "req_bridge" {
			t.Fatalf("backend request body = %#v", req)
		}
		writeSuccess(w, r, http.StatusOK, contracts.ProviderInvokeResponse{
			Output: map[string]any{"message": req.Input["message"]},
		})
	}))
	defer backend.Close()

	server, err := NewHTTPBridgeServer(bridgeManifest(), HTTPBridgeConfig{
		Routes: map[string]HTTPBridgeRoute{
			"cap_bridge_echo": {
				URL:     backend.URL + "/invoke",
				Headers: map[string]string{"X-Backend-Token": "secret"},
			},
		},
		Client: backend.Client(),
	})
	if err != nil {
		t.Fatalf("new bridge: %v", err)
	}

	reqBody := contracts.ProviderInvokeRequest{
		Input:   map[string]any{"message": "hello"},
		Context: contracts.ProviderInvokeContext{SubjectID: "sub_agent", RequestID: "req_bridge"},
	}
	rec := invokeBridge(t, server, reqBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope contracts.SuccessEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := envelope.Data.(map[string]any)
	output := data["output"].(map[string]any)
	if output["message"] != "hello" {
		t.Fatalf("output = %#v", output)
	}
}

func TestHTTPBridgeSupportsHeadersFromEnv(t *testing.T) {
	t.Setenv("PACP_TEST_BACKEND_TOKEN", "Bearer env-token")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer env-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		writeSuccess(w, r, http.StatusOK, contracts.ProviderInvokeResponse{
			Output: map[string]any{"message": "env"},
		})
	}))
	defer backend.Close()

	server, err := NewHTTPBridgeServer(bridgeManifest(), HTTPBridgeConfig{
		Routes: map[string]HTTPBridgeRoute{
			"cap_bridge_echo": {
				URL:            backend.URL,
				HeadersFromEnv: map[string]string{"Authorization": "PACP_TEST_BACKEND_TOKEN"},
			},
		},
		Client: backend.Client(),
	})
	if err != nil {
		t.Fatalf("new bridge: %v", err)
	}
	rec := invokeBridge(t, server, contracts.ProviderInvokeRequest{Input: map[string]any{"message": "hello"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPBridgeSupportsHeadersFromSecret(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer resolved-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		writeSuccess(w, r, http.StatusOK, contracts.ProviderInvokeResponse{
			Output: map[string]any{"message": "secret"},
		})
	}))
	defer backend.Close()

	server, err := NewHTTPBridgeServer(bridgeManifest(), HTTPBridgeConfig{
		Routes: map[string]HTTPBridgeRoute{
			"cap_bridge_echo": {
				URL:               backend.URL,
				HeadersFromSecret: map[string]string{"Authorization": "secret_backend_token"},
			},
		},
		Client:         backend.Client(),
		SecretResolver: staticSecretResolver{"secret_backend_token": "Bearer resolved-token"},
	})
	if err != nil {
		t.Fatalf("new bridge: %v", err)
	}
	rec := invokeBridge(t, server, contracts.ProviderInvokeRequest{Input: map[string]any{"message": "hello"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPBridgeSupportsProviderAuthCredential(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSuccess(w, r, http.StatusOK, contracts.ProviderInvokeResponse{
			Output: map[string]any{"message": "authorized"},
		})
	}))
	defer backend.Close()

	server, err := NewHTTPBridgeServer(bridgeManifest(), HTTPBridgeConfig{
		Routes: map[string]HTTPBridgeRoute{
			"cap_bridge_echo": {URL: backend.URL},
		},
		Client:         backend.Client(),
		AuthCredential: "provider-token",
	})
	if err != nil {
		t.Fatalf("new bridge: %v", err)
	}

	unauthorized := invokeBridge(t, server, contracts.ProviderInvokeRequest{Input: map[string]any{"message": "hello"}})
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	authorized := invokeBridgeWithHeaders(t, server, contracts.ProviderInvokeRequest{Input: map[string]any{"message": "hello"}}, map[string]string{
		"Authorization": "Bearer provider-token",
	})
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status=%d body=%s", authorized.Code, authorized.Body.String())
	}
}

func TestHTTPBridgeRejectsSecretHeaderWithoutResolver(t *testing.T) {
	_, err := NewHTTPBridgeServer(bridgeManifest(), HTTPBridgeConfig{
		Routes: map[string]HTTPBridgeRoute{
			"cap_bridge_echo": {
				URL:               "http://backend.invalid/invoke",
				HeadersFromSecret: map[string]string{"Authorization": "secret_backend_token"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected missing secret resolver error")
	}
}

func TestHTTPBridgeRejectsMissingHeaderEnv(t *testing.T) {
	_, err := NewHTTPBridgeServer(bridgeManifest(), HTTPBridgeConfig{
		Routes: map[string]HTTPBridgeRoute{
			"cap_bridge_echo": {
				URL:            "http://backend.invalid/invoke",
				HeadersFromEnv: map[string]string{"Authorization": "PACP_TEST_MISSING_BACKEND_TOKEN"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected missing header env error")
	}
}

func TestHTTPBridgeDecodesDirectProviderResponse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(contracts.ProviderInvokeResponse{
			Output: map[string]any{"message": "direct"},
		})
	}))
	defer backend.Close()

	server, err := NewHTTPBridgeServer(bridgeManifest(), HTTPBridgeConfig{
		Routes: map[string]HTTPBridgeRoute{
			"cap_bridge_echo": {URL: backend.URL},
		},
		Client: backend.Client(),
	})
	if err != nil {
		t.Fatalf("new bridge: %v", err)
	}

	rec := invokeBridge(t, server, contracts.ProviderInvokeRequest{Input: map[string]any{"message": "hello"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPBridgePreservesBackendErrorEnvelope(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusGatewayTimeout, "provider_timeout", "backend timed out", true)
	}))
	defer backend.Close()

	server, err := NewHTTPBridgeServer(bridgeManifest(), HTTPBridgeConfig{
		Routes: map[string]HTTPBridgeRoute{
			"cap_bridge_echo": {URL: backend.URL},
		},
		Client: backend.Client(),
	})
	if err != nil {
		t.Fatalf("new bridge: %v", err)
	}

	rec := invokeBridge(t, server, contracts.ProviderInvokeRequest{Input: map[string]any{"message": "hello"}})
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope contracts.ErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Error.Code != "provider_timeout" || envelope.Error.Message != "backend timed out" || !envelope.Error.Retryable {
		t.Fatalf("error = %#v", envelope.Error)
	}
}

func TestHTTPBridgeReturnsTimeoutForExpiredContext(t *testing.T) {
	handler := httpBridgeHandler(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, req.Context().Err()
	})}, HTTPBridgeRoute{URL: "http://backend.invalid/invoke", Method: http.MethodPost})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := handler(ctx, contracts.ProviderInvokeRequest{Input: map[string]any{"message": "hello"}})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v", err)
	}
}

func TestHTTPBridgeRequiresRouteForEachCapability(t *testing.T) {
	_, err := NewHTTPBridgeServer(bridgeManifest(), HTTPBridgeConfig{Routes: map[string]HTTPBridgeRoute{}})
	if err == nil {
		t.Fatal("expected missing route error")
	}
}

func TestHTTPBridgeRejectsUnsupportedRouteMethod(t *testing.T) {
	_, err := NewHTTPBridgeServer(bridgeManifest(), HTTPBridgeConfig{
		Routes: map[string]HTTPBridgeRoute{
			"cap_bridge_echo": {URL: "http://backend.invalid/invoke", Method: http.MethodGet},
		},
	})
	if err == nil {
		t.Fatal("expected unsupported method error")
	}
}

func invokeBridge(t *testing.T, server http.Handler, body contracts.ProviderInvokeRequest) *httptest.ResponseRecorder {
	t.Helper()
	return invokeBridgeWithHeaders(t, server, body, nil)
}

func invokeBridgeWithHeaders(t *testing.T, server http.Handler, body contracts.ProviderInvokeRequest, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal invoke request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/provider/capabilities/cap_bridge_echo/invoke", bytesReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	return rec
}

func bytesReader(raw []byte) *bytes.Reader {
	return bytes.NewReader(raw)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type staticSecretResolver map[string]string

func (r staticSecretResolver) ResolveSecret(ctx context.Context, secretRef string) (string, error) {
	value, ok := r[secretRef]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func bridgeManifest() contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_bridge",
			Name:         "Bridge",
			Description:  "HTTP bridge test provider",
			Version:      "0.1.0",
			ProviderKind: "http_bridge",
			Tags:         []string{"bridge"},
		},
		Provider: contracts.Provider{Endpoint: "http://provider.invalid", HealthPath: "/v1/provider/health"},
		Capabilities: []contracts.Capability{{
			ID:            "cap_bridge_echo",
			Name:          "Bridge echo",
			Description:   "Bridge echo test capability.",
			Tags:          []string{"bridge"},
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
			Examples:      []map[string]any{{"message": "hello"}},
			SideEffects:   "none",
			ResourceHints: []contracts.ResourceHint{},
			ArtifactHints: []contracts.ArtifactHint{},
			TimeoutHint:   "30s",
		}},
	}
}
