package transportauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pacp/internal/contracts"
)

func TestRequireVerifiedScopesAllowsMatchingScope(t *testing.T) {
	policy := newScopePolicyServer(t, "")
	defer policy.Close()

	called := false
	handler := RequireVerifiedScopes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}), ScopeConfig{
		PolicyURL: policy.URL,
		Rules: []ScopeRule{{
			Method: http.MethodPost,
			Path:   "/v1/jobs/{job_id}/heartbeat",
			Scopes: []string{"worker"},
		}},
		Client: policy.Client(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/job_1/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer token_worker")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent || !called {
		t.Fatalf("status=%d called=%v body=%s", rec.Code, called, rec.Body.String())
	}
}

func TestRequireVerifiedScopesRejectsMissingCredentialWithRouteMessage(t *testing.T) {
	policy := newScopePolicyServer(t, "")
	defer policy.Close()

	handler := RequireVerifiedScopes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}), ScopeConfig{
		PolicyURL: policy.URL,
		Rules: []ScopeRule{{
			Method:              http.MethodPost,
			Path:                "/v1/jobs/{job_id}/heartbeat",
			Scopes:              []string{"worker"},
			UnauthorizedMessage: "job worker operation requires a valid runner credential",
		}},
		Client: policy.Client(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/job_1/heartbeat", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if message := errorMessage(t, rec); message != "job worker operation requires a valid runner credential" {
		t.Fatalf("message=%q", message)
	}
}

func TestRequireVerifiedScopesRejectsWrongScope(t *testing.T) {
	policy := newScopePolicyServer(t, "")
	defer policy.Close()

	handler := RequireVerifiedScopes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}), ScopeConfig{
		PolicyURL: policy.URL,
		Rules: []ScopeRule{{
			Method:           http.MethodPost,
			Path:             "/v1/jobs/{job_id}/heartbeat",
			Scopes:           []string{"worker"},
			ForbiddenMessage: "caller is not authorized for this job operation",
		}},
		Client: policy.Client(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/job_1/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer token_component")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "forbidden" {
		t.Fatalf("code=%q", code)
	}
}

func TestRequireVerifiedScopesBypassesUnmatchedRoutes(t *testing.T) {
	policyCalls := 0
	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policyCalls++
		t.Fatal("policy should not be called")
	}))
	defer policy.Close()

	called := false
	handler := RequireVerifiedScopes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}), ScopeConfig{
		PolicyURL: policy.URL,
		Rules: []ScopeRule{{
			Method: http.MethodPost,
			Path:   "/v1/jobs/{job_id}/heartbeat",
			Scopes: []string{"worker"},
		}},
		Client: policy.Client(),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent || !called || policyCalls != 0 {
		t.Fatalf("status=%d called=%v policyCalls=%d", rec.Code, called, policyCalls)
	}
}

func TestRequireVerifiedScopesSendsPolicyCredential(t *testing.T) {
	policy := newScopePolicyServer(t, "Bearer token_component")
	defer policy.Close()

	handler := RequireVerifiedScopes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), ScopeConfig{
		PolicyURL:        policy.URL,
		PolicyCredential: "token_component",
		Rules: []ScopeRule{{
			Method: http.MethodPost,
			Path:   "/v1/jobs",
			Scopes: []string{"component"},
		}},
		Client: policy.Client(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", nil)
	req.Header.Set("Authorization", "Bearer token_component")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func newScopePolicyServer(t *testing.T, wantPolicyAuth string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/auth/verify" {
			t.Fatalf("unexpected policy request %s %s", r.Method, r.URL.String())
		}
		if wantPolicyAuth != "" && r.Header.Get("Authorization") != wantPolicyAuth {
			t.Fatalf("policy Authorization=%q", r.Header.Get("Authorization"))
		}
		var req contracts.VerifyCredentialRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode verify request: %v", err)
		}
		scopes := map[string][]string{
			"Bearer token_worker":    {"worker"},
			"Bearer token_component": {"component"},
			"Bearer token_admin":     {"admin"},
		}[req.Credential]
		var subjectID *string
		valid := len(scopes) > 0
		if valid {
			value := "sub_test"
			subjectID = &value
		}
		writeScopePolicySuccess(t, w, contracts.CredentialVerification{
			Valid:     valid,
			SubjectID: subjectID,
			Scopes:    scopes,
		})
	}))
}

func writeScopePolicySuccess(t *testing.T, w http.ResponseWriter, data contracts.CredentialVerification) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(contracts.SuccessEnvelope{
		OK:    true,
		Data:  data,
		Links: map[string]any{},
		Meta:  map[string]string{"request_id": "req_scope_test", "schema_version": "v1"},
	}); err != nil {
		t.Fatalf("encode policy response: %v", err)
	}
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	envelope := errorEnvelope(t, rec)
	errObj := envelope["error"].(map[string]any)
	code, _ := errObj["code"].(string)
	return code
}

func errorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	envelope := errorEnvelope(t, rec)
	errObj := envelope["error"].(map[string]any)
	message, _ := errObj["message"].(string)
	return message
}

func errorEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}
