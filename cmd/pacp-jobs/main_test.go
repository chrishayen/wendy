package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"pacp/internal/components/jobs"
	"pacp/internal/contracts"
	"pacp/internal/testkit"
	"pacp/internal/transportauth"
)

func TestJobScopeRulesSeparateComponentAndWorkerRoutes(t *testing.T) {
	rules := jobScopeRules()
	assertRuleScopes(t, rules, http.MethodPost, "/v1/jobs", []string{"component"})
	assertRuleScopes(t, rules, http.MethodPost, "/v1/jobs/{job_id}/heartbeat", []string{"worker"})
	assertRuleScopes(t, rules, http.MethodPost, "/v1/jobs/{job_id}/cancel", []string{"component"})
	assertRuleScopes(t, rules, http.MethodGet, "/v1/jobs/{job_id}", []string{"component", "worker"})
}

func TestJobsPolicyCredentialDefault(t *testing.T) {
	t.Setenv("PACP_JOBS_POLICY_CREDENTIAL", "jobs-policy-token")
	t.Setenv("PACP_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("PACP_JOBS_POLICY_CREDENTIAL"); got != "jobs-policy-token" {
		t.Fatalf("credential default = %q", got)
	}
}

func TestJobsPolicyCredentialDefaultFallsBackToComponentToken(t *testing.T) {
	t.Setenv("PACP_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("PACP_JOBS_POLICY_CREDENTIAL"); got != "component-token" {
		t.Fatalf("credential default = %q", got)
	}
}

func TestJobsAuthorizationHeaderNormalizesRawTokens(t *testing.T) {
	if got := authorizationHeader("component-token"); got != "Bearer component-token" {
		t.Fatalf("raw header = %q", got)
	}
	if got := authorizationHeader("Bearer component-token"); got != "Bearer component-token" {
		t.Fatalf("bearer header = %q", got)
	}
	if got := authorizationHeader(""); got != "" {
		t.Fatalf("empty header = %q", got)
	}
}

func TestJobsHandlerReplaysS003EdgeAuthFixtures(t *testing.T) {
	scenario, err := testkit.LoadScenario(filepath.Join("..", "..", "testdata", "contract-sim"), filepath.Join("fixtures", "S003", "manifest.json"))
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	pkg, ok := testkit.FindPackage(scenario, "c05-async-job-service")
	if !ok {
		t.Fatalf("c05 fixture package not found")
	}

	handler := transportauth.RequireVerifiedScopes(jobs.NewHandler(jobs.NewStore()), transportauth.ScopeConfig{
		PolicyURL: "http://policy.test",
		Rules:     jobScopeRules(),
		Client:    &http.Client{Transport: jobsPolicyTransport{t: t}},
	})
	for _, fixtureID := range []string{
		"job_worker_unauthorized",
		"job_gateway_unauthorized",
		"job_gateway_forbidden",
	} {
		if _, err := testkit.ReplayHTTPFixture(handler, pkg, fixtureID); err != nil {
			t.Fatalf("replay %s: %v", fixtureID, err)
		}
	}
}

type jobsPolicyTransport struct {
	t *testing.T
}

func (rt jobsPolicyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.t.Helper()
	if r.Method != http.MethodPost || r.URL.String() != "http://policy.test/v1/auth/verify" {
		rt.t.Fatalf("unexpected policy request %s %s", r.Method, r.URL.String())
	}
	var req contracts.VerifyCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		rt.t.Fatalf("decode verify request: %v", err)
	}
	scopes := map[string][]string{
		"Bearer token_s003_agent":     {"agent"},
		"Bearer token_s003_gateway":   {"component"},
		"Bearer token_s003_runner":    {"worker"},
		"Bearer token_s003_component": {"component"},
	}[req.Credential]
	var subjectID *string
	if len(scopes) > 0 {
		value := "sub_s003_auth_subject"
		subjectID = &value
	}
	raw, err := json.Marshal(contracts.SuccessEnvelope{
		OK: true,
		Data: contracts.CredentialVerification{
			Valid:     len(scopes) > 0,
			SubjectID: subjectID,
			Scopes:    scopes,
		},
		Links: map[string]any{},
		Meta:  map[string]string{"request_id": "req_s003_jobs_policy_verify", "schema_version": "v1"},
	})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(raw)),
		Request:    r,
	}, nil
}

func assertRuleScopes(t *testing.T, rules []transportauth.ScopeRule, method, path string, want []string) {
	t.Helper()
	for _, rule := range rules {
		if rule.Method != method || rule.Path != path {
			continue
		}
		if !sameStrings(rule.Scopes, want) {
			t.Fatalf("%s %s scopes=%#v want=%#v", method, path, rule.Scopes, want)
		}
		return
	}
	t.Fatalf("rule %s %s not found", method, path)
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
