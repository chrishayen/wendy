package main

import (
	"net/http"
	"testing"

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
