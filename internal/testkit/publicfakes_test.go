package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pacp/internal/contracts"
)

func TestFakeComponentHandlersPassComponentChecks(t *testing.T) {
	kinds := []string{"artifacts", "catalog", "gateway", "jobs", "leases", "node", "policy", "runner"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			handler, err := NewFakeComponentHandler(FakeComponentConfig{
				Kind: kind,
				Now:  fixedFakeClock,
			})
			if err != nil {
				t.Fatalf("new fake component: %v", err)
			}
			server := httptest.NewServer(handler)
			defer server.Close()

			report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
				BaseURL:   server.URL,
				Kind:      kind,
				RequestID: "req_fake_component",
			})
			if !report.Passed() {
				t.Fatalf("report = %#v", report)
			}
		})
	}
}

func TestFakeComponentHandlerRequiresCredential(t *testing.T) {
	handler, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind:       "jobs",
		Credential: "component-token",
		Now:        fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake component: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	denied := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:   server.URL,
		Kind:      "jobs",
		RequestID: "req_fake_component",
	})
	if denied.Passed() {
		t.Fatalf("unauthenticated check unexpectedly passed: %#v", denied)
	}

	allowed := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:    server.URL,
		Kind:       "jobs",
		Credential: "Bearer component-token",
		RequestID:  "req_fake_component",
	})
	if !allowed.Passed() {
		t.Fatalf("authenticated check failed: %#v", allowed)
	}
}

func TestFakeComponentHandlerSupportsDeniedBehavior(t *testing.T) {
	handler, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind:     "jobs",
		Behavior: FakeComponentDenied,
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake component: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/v1/jobs/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var envelope rawErrorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.OK || envelope.Error.Code != "forbidden" {
		t.Fatalf("envelope = %#v", envelope)
	}

	report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:   server.URL,
		Kind:      "jobs",
		RequestID: "req_fake_component_denied",
	})
	if report.Passed() {
		t.Fatalf("denied component check unexpectedly passed: %#v", report)
	}
}

func TestFakeComponentHandlerSupportsUnavailableBehavior(t *testing.T) {
	handler, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind:     "leases",
		Behavior: FakeComponentUnavailable,
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake component: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/v1/leases/metrics")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var envelope rawErrorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.OK || envelope.Error.Code != "component_unavailable" || !envelope.Error.Retryable {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestFakeComponentHandlerSupportsCustomListItems(t *testing.T) {
	now := "2026-06-08T00:00:00Z"
	handler, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind: "jobs",
		ListItems: []any{contracts.Job{
			JobID:        "job_done",
			State:        contracts.JobSucceeded,
			CreatedAt:    now,
			UpdatedAt:    now,
			ArtifactRefs: []string{"art_done"},
			Links:        map[string]any{},
		}},
		Now: fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake component: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:   server.URL,
		Kind:      "jobs",
		RequestID: "req_fake_component_custom",
	})
	if !report.Passed() {
		t.Fatalf("custom list check failed: %#v", report)
	}
}

func TestFakeComponentHandlerSupportsExplicitEmptyList(t *testing.T) {
	handler, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind:      "artifacts",
		ListItems: []any{},
		Now:       fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake component: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:   server.URL,
		Kind:      "artifacts",
		RequestID: "req_fake_component_empty",
	})
	if !report.Passed() {
		t.Fatalf("empty list check failed: %#v", report)
	}
}

func TestFakeComponentHandlerRejectsUnknownBehavior(t *testing.T) {
	_, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind:     "jobs",
		Behavior: FakeComponentBehavior("strange"),
	})
	if err == nil {
		t.Fatal("expected unknown behavior error")
	}
}

func TestFakePolicyHandlerSupportsAuthAllowAndDeny(t *testing.T) {
	handler := NewFakePolicyHandler(FakePolicyConfig{
		ValidCredential: "token_policy",
		SubjectID:       "sub_policy",
		Scopes:          []string{"component", "worker"},
		Decision:        contracts.PolicyDecision{Allowed: true, Reason: "test_allow"},
		Now:             fixedFakeClock,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	verify := postFakePolicyEnvelope(t, server, "/v1/auth/verify", contracts.VerifyCredentialRequest{Credential: "Bearer token_policy"}, http.StatusOK)
	var verification contracts.CredentialVerification
	decodeEnvelopeData(t, verify, &verification)
	if !verification.Valid || verification.SubjectID == nil || *verification.SubjectID != "sub_policy" {
		t.Fatalf("verification = %#v", verification)
	}
	if len(verification.Scopes) != 2 {
		t.Fatalf("scopes = %#v", verification.Scopes)
	}

	check := postFakePolicyEnvelope(t, server, "/v1/policy/check", contracts.PolicyCheckRequest{
		SubjectID: "sub_policy",
		Action:    "tool.invoke",
		Resource:  "cap_fake",
	}, http.StatusOK)
	var decision contracts.PolicyDecision
	decodeEnvelopeData(t, check, &decision)
	if !decision.Allowed || decision.Reason != "test_allow" {
		t.Fatalf("decision = %#v", decision)
	}

	denyHandler := NewFakePolicyHandler(FakePolicyConfig{
		Decision: contracts.PolicyDecision{Allowed: false, Reason: "test_deny"},
	})
	denyServer := httptest.NewServer(denyHandler)
	defer denyServer.Close()
	denyEnvelope := postFakePolicyEnvelope(t, denyServer, "/v1/policy/check", contracts.PolicyCheckRequest{
		SubjectID: "sub_policy",
		Action:    "tool.invoke",
		Resource:  "cap_fake",
	}, http.StatusOK)
	decodeEnvelopeData(t, denyEnvelope, &decision)
	if decision.Allowed || decision.Reason != "test_deny" {
		t.Fatalf("deny decision = %#v", decision)
	}
}

func TestFakePolicyHandlerSupportsAuthFailure(t *testing.T) {
	handler := NewFakePolicyHandler(FakePolicyConfig{ValidCredential: "token_policy"})
	server := httptest.NewServer(handler)
	defer server.Close()

	envelope := postFakePolicyEnvelope(t, server, "/v1/auth/verify", contracts.VerifyCredentialRequest{Credential: "Bearer other"}, http.StatusOK)
	var verification contracts.CredentialVerification
	decodeEnvelopeData(t, envelope, &verification)
	if verification.Valid || verification.SubjectID != nil || len(verification.Scopes) != 0 {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestFakePolicyHandlerSupportsSecretsAndRedaction(t *testing.T) {
	handler := NewFakePolicyHandler(FakePolicyConfig{
		SubjectID: "sub_component",
		Secrets:   map[string]string{"secret_fake": "super-secret"},
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	resolve := postFakePolicyEnvelope(t, server, "/v1/secrets/resolve", contracts.ResolveSecretRequest{
		SecretRef: "secret_fake",
		SubjectID: "sub_component",
	}, http.StatusOK)
	var secret contracts.ResolvedSecret
	decodeEnvelopeData(t, resolve, &secret)
	if secret.Value != "super-secret" {
		t.Fatalf("secret = %#v", secret)
	}

	redact := postFakePolicyEnvelope(t, server, "/v1/redact", contracts.RedactRequest{Text: "token is super-secret"}, http.StatusOK)
	var redacted contracts.RedactResponse
	decodeEnvelopeData(t, redact, &redacted)
	if redacted.Text != "token is [REDACTED]" {
		t.Fatalf("redacted = %#v", redacted)
	}

	forbidden := postFakePolicyEnvelope(t, server, "/v1/secrets/resolve", contracts.ResolveSecretRequest{
		SecretRef: "secret_fake",
		SubjectID: "sub_agent",
	}, http.StatusForbidden)
	if forbidden.OK || forbidden.Error.Code != "forbidden" {
		t.Fatalf("forbidden envelope = %#v", forbidden)
	}
}

func TestFakePolicyHandlerRequiresComponentCredential(t *testing.T) {
	handler := NewFakePolicyHandler(FakePolicyConfig{ComponentCredential: "component-token"})
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/policy/health", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodGet, server.URL+"/v1/policy/health", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer component-token")
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("get authorized health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %d", resp.StatusCode)
	}
}

func TestFakeProviderHandlerPassesProviderCheck(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint:   "http://provider.fake",
		Credential: "provider-token",
		Now:        fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:      server.URL,
		CapabilityID: "cap_echo",
		Input:        map[string]any{"message": "hello"},
		Credential:   "Bearer provider-token",
		RequestID:    "req_fake_provider",
	})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
}

func TestFakeProviderHandlerPassesArtifactProviderCheck(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint: "http://provider.fake",
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:      server.URL,
		CapabilityID: "cap_artifact",
		Input:        map[string]any{"prompt": "hello"},
		RequestID:    "req_fake_provider",
	})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
	if !hasProviderCheck(report, "provider.artifact_metadata") {
		t.Fatalf("artifact metadata check missing: %#v", report.Checks)
	}
}

func TestFakeProviderHandlerPassesAsyncProviderCheck(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint: "http://provider.fake",
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:      server.URL,
		CapabilityID: "cap_async_accept",
		Input:        map[string]any{},
		RequestID:    "req_fake_provider",
	})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
}

func TestFakeProviderHandlerPassesExpectedFailureCheck(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint: "http://provider.fake",
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProviderExpectedError(context.Background(), server.Client(), ProviderExpectedErrorOptions{
		BaseURL:        server.URL,
		CapabilityID:   "cap_fail",
		WantHTTPStatus: 503,
		WantCode:       "provider_unavailable",
		RequestID:      "req_fake_provider_failure",
	})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
}

func TestFakeProviderHandlerRequiresCredential(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint:   "http://provider.fake",
		Credential: "provider-token",
		Now:        fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:   server.URL,
		RequestID: "req_fake_provider",
	})
	if report.Passed() {
		t.Fatalf("unauthenticated provider check unexpectedly passed: %#v", report)
	}
}

func fixedFakeClock() time.Time {
	return time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
}

func hasProviderCheck(report ProviderCheckReport, name string) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.OK {
			return true
		}
	}
	return false
}

type fakePolicyEnvelope struct {
	OK    bool                  `json:"ok"`
	Data  json.RawMessage       `json:"data"`
	Error contracts.ErrorObject `json:"error"`
}

func postFakePolicyEnvelope(t *testing.T, server *httptest.Server, path string, body any, wantStatus int) fakePolicyEnvelope {
	t.Helper()
	var raw bytes.Buffer
	if err := json.NewEncoder(&raw).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+path, &raw)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req_fake_policy")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s status = %d, want %d", path, resp.StatusCode, wantStatus)
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func decodeEnvelopeData(t *testing.T, envelope fakePolicyEnvelope, out any) {
	t.Helper()
	if !envelope.OK {
		t.Fatalf("expected success envelope, got %#v", envelope.Error)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		t.Fatalf("decode data: %v", err)
	}
}
