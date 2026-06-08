package policy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerCredentialPolicyAndSecretFlow(t *testing.T) {
	handler := NewHandler(NewStore())

	key := doJSON(t, handler, http.MethodPost, "/v1/api-keys", map[string]any{
		"subject_id": "sub_agent",
		"scopes":     []string{"agent"},
		"token":      "token_agent",
	}, http.StatusCreated)
	if key["token"] != "token_agent" {
		t.Fatalf("key = %#v", key)
	}

	verify := doJSON(t, handler, http.MethodPost, "/v1/auth/verify", map[string]any{
		"credential": "Bearer token_agent",
	}, http.StatusOK)
	if verify["valid"] != true || verify["subject_id"] != "sub_agent" {
		t.Fatalf("verify = %#v", verify)
	}

	decision := doJSON(t, handler, http.MethodPost, "/v1/policy/check", map[string]any{
		"subject_id": "sub_agent",
		"action":     "tool.invoke",
		"resource":   "cap_image_generate_gpu",
	}, http.StatusOK)
	if decision["allowed"] != true {
		t.Fatalf("policy decision = %#v", decision)
	}

	secret := doJSON(t, handler, http.MethodPost, "/v1/secrets", map[string]any{
		"name":  "provider_token",
		"value": "super-secret",
	}, http.StatusCreated)
	if secret["secret_ref"] == "" {
		t.Fatalf("secret = %#v", secret)
	}

	redacted := doJSON(t, handler, http.MethodPost, "/v1/redact", map[string]any{
		"text": "token is super-secret",
	}, http.StatusOK)
	if redacted["text"] != "token is [REDACTED]" {
		t.Fatalf("redacted = %#v", redacted)
	}
}

func TestHandlerMalformedCredentialError(t *testing.T) {
	handler := NewHandler(NewStore())
	envelope := doJSONEnvelope(t, handler, http.MethodPost, "/v1/auth/verify", map[string]any{
		"credential": "bearer token",
	}, http.StatusUnauthorized)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "unauthorized" {
		t.Fatalf("error = %#v", errObj)
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
		t.Fatalf("%s %s status = %d, want %d, body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope
}
