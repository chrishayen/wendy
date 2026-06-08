package transportauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireBearerAllowsRequestsWhenTokenDisabled(t *testing.T) {
	handler := RequireBearer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "")

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequireBearerRejectsMissingCredential(t *testing.T) {
	handler := RequireBearer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}), "token_component")

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope["ok"].(bool) {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestRequireBearerAllowsValidCredential(t *testing.T) {
	called := false
	handler := RequireBearer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}), "token_component")

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("Authorization", "Bearer token_component")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || !called {
		t.Fatalf("status=%d called=%v body=%s", rec.Code, called, rec.Body.String())
	}
}
