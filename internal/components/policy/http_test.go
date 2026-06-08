package policy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"pacp/internal/contracts"
	"pacp/internal/testkit"
)

func TestHandlerReplaysS003AuthAndPolicyFixtures(t *testing.T) {
	scenario, err := testkit.LoadScenario(filepath.Join("..", "..", "..", "testdata", "contract-sim"), filepath.Join("fixtures", "S003", "manifest.json"))
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	pkg, ok := testkit.FindPackage(scenario, "c08-access-policy-and-secrets")
	if !ok {
		t.Fatalf("c08 fixture package not found")
	}
	handler := NewHandler(s003PolicyFixtureStore(t))

	for _, fixtureID := range []string{
		"auth_agent_valid",
		"auth_gateway_valid",
		"auth_runner_valid",
		"auth_malformed_credential",
		"policy_job_cancel_running_deny",
		"policy_job_cancel_missing_state_deny",
		"policy_denied",
	} {
		if _, err := testkit.ReplayHTTPFixture(handler, pkg, fixtureID); err != nil {
			t.Fatalf("replay %s: %v", fixtureID, err)
		}
	}
}

func s003PolicyFixtureStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore()
	for _, req := range []contracts.CreateAPIKeyRequest{
		{SubjectID: "sub_agent_s003", Scopes: []string{"agent"}, Token: "token_s003_agent"},
		{SubjectID: "sub_gateway_s003", Scopes: []string{"component"}, Token: "token_s003_gateway"},
		{SubjectID: "sub_runner_s003", Scopes: []string{"worker"}, Token: "token_s003_runner"},
		{SubjectID: "sub_other_s003", Scopes: []string{"agent"}, Token: "token_s003_other"},
	} {
		if _, err := store.CreateAPIKey(req); err != nil {
			t.Fatalf("create fixture key %s: %v", req.SubjectID, err)
		}
	}
	return store
}

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

	rotated := doJSON(t, handler, http.MethodPost, "/v1/api-keys/"+key["key_id"].(string)+"/rotate", map[string]any{
		"token": "token_agent_rotated",
	}, http.StatusOK)
	if rotated["token"] != "token_agent_rotated" || rotated["rotated_at"] == "" {
		t.Fatalf("rotated = %#v", rotated)
	}
	oldToken := doJSON(t, handler, http.MethodPost, "/v1/auth/verify", map[string]any{
		"credential": "Bearer token_agent",
	}, http.StatusOK)
	if oldToken["valid"] != false {
		t.Fatalf("old token verified after rotate = %#v", oldToken)
	}
	newToken := doJSON(t, handler, http.MethodPost, "/v1/auth/verify", map[string]any{
		"credential": "Bearer token_agent_rotated",
	}, http.StatusOK)
	if newToken["valid"] != true || newToken["subject_id"] != "sub_agent" {
		t.Fatalf("new token verify = %#v", newToken)
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
	metrics := doJSON(t, handler, http.MethodGet, "/v1/policy/metrics", nil, http.StatusOK)
	if metrics["component"] != "policy" {
		t.Fatalf("metrics = %#v", metrics)
	}
	assertMetric(t, metrics, "policy_api_keys_total", nil, 1)
	assertMetric(t, metrics, "policy_decisions_total", map[string]string{"action": "tool.invoke", "decision": "allow"}, 1)
	assertMetric(t, metrics, "http_requests_total", map[string]string{"method": "POST", "route_group": "/v1/policy/check", "status_class": "2xx"}, 1)
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

func TestHandlerHealth(t *testing.T) {
	handler := NewHandler(NewStore())
	data := doJSON(t, handler, http.MethodGet, "/v1/policy/health", nil, http.StatusOK)
	details := data["details"].(map[string]any)
	if data["status"] != "healthy" || details["component"] != "policy" {
		t.Fatalf("health = %#v", data)
	}
	if details["store_backend"] != "memory" || details["secret_backend"] != "local_state_redacted" || details["api_key_count"] != float64(0) {
		t.Fatalf("health = %#v", data)
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

func assertMetric(t *testing.T, data map[string]any, name string, labels map[string]string, value float64) {
	t.Helper()
	for _, rawSample := range data["samples"].([]any) {
		sample := rawSample.(map[string]any)
		if sample["name"] != name {
			continue
		}
		if !labelsMatch(sample["labels"], labels) {
			continue
		}
		if sample["value"] != value {
			t.Fatalf("metric %s value=%#v want=%v", name, sample["value"], value)
		}
		return
	}
	t.Fatalf("metric %s labels=%#v not found in %#v", name, labels, data["samples"])
}

func labelsMatch(raw any, want map[string]string) bool {
	if len(want) == 0 {
		return raw == nil
	}
	labels, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range want {
		if labels[key] != value {
			return false
		}
	}
	return true
}
