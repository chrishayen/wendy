package main

import (
	"net/http"
	"testing"

	"pacp/internal/routeauth"
	"pacp/internal/transportauth"
)

func TestLeaseScopeRulesSeparateComponentAndWorkerRoutes(t *testing.T) {
	rules := routeauth.LeaseScopeRules()
	assertRuleScopes(t, rules, http.MethodGet, "/v1/resources", []string{"component", "worker"})
	assertRuleScopes(t, rules, http.MethodPost, "/v1/resources", []string{"component"})
	assertRuleScopes(t, rules, http.MethodGet, "/v1/lease-requests", []string{"component", "worker"})
	assertRuleScopes(t, rules, http.MethodPost, "/v1/lease-requests", []string{"worker"})
	assertRuleScopes(t, rules, http.MethodPost, "/v1/lease-requests/{request_id}/cancel", []string{"component", "worker"})
	assertRuleScopes(t, rules, http.MethodPost, "/v1/leases/{lease_id}/heartbeat", []string{"worker"})
	assertRuleScopes(t, rules, http.MethodPost, "/v1/leases/{lease_id}/release", []string{"worker"})
}

func TestLeasesPolicyCredentialDefault(t *testing.T) {
	t.Setenv("PACP_LEASES_POLICY_CREDENTIAL", "leases-policy-token")
	t.Setenv("PACP_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("PACP_LEASES_POLICY_CREDENTIAL"); got != "leases-policy-token" {
		t.Fatalf("credential default = %q", got)
	}
}

func TestLeasesPolicyCredentialDefaultFallsBackToComponentToken(t *testing.T) {
	t.Setenv("PACP_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("PACP_LEASES_POLICY_CREDENTIAL"); got != "component-token" {
		t.Fatalf("credential default = %q", got)
	}
}

func TestLeasesAuthorizationHeaderNormalizesRawTokens(t *testing.T) {
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
