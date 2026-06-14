package main

import (
	"net/http"
	"testing"
	"time"

	"wendy/internal/routeauth"
	"wendy/internal/transportauth"
)

func TestArtifactScopeRulesSeparateComponentAndWorkerRoutes(t *testing.T) {
	rules := routeauth.ArtifactScopeRules()
	assertRuleScopes(t, rules, http.MethodPost, "/v1/artifact-uploads", []string{"worker"})
	assertRuleScopes(t, rules, http.MethodPut, "/v1/artifact-uploads/{upload_id}/content", []string{"worker"})
	assertRuleScopes(t, rules, http.MethodPost, "/v1/artifact-uploads/{upload_id}/complete", []string{"worker"})
	assertRuleScopes(t, rules, http.MethodPost, "/v1/artifacts/retention/sweep", []string{"component"})
	assertRuleScopes(t, rules, http.MethodGet, "/v1/artifacts/{artifact_id}/policy-context", []string{"component"})
	assertRuleScopes(t, rules, http.MethodGet, "/v1/artifacts/{artifact_id}/content", []string{"component"})
	assertRuleScopes(t, rules, http.MethodPost, "/v1/artifacts/register-local", []string{"worker"})
}

func TestArtifactsPolicyCredentialDefault(t *testing.T) {
	t.Setenv("WENDY_ARTIFACTS_POLICY_CREDENTIAL", "artifacts-policy-token")
	t.Setenv("WENDY_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("WENDY_ARTIFACTS_POLICY_CREDENTIAL"); got != "artifacts-policy-token" {
		t.Fatalf("credential default = %q", got)
	}
}

func TestArtifactsPolicyCredentialDefaultFallsBackToComponentToken(t *testing.T) {
	t.Setenv("WENDY_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("WENDY_ARTIFACTS_POLICY_CREDENTIAL"); got != "component-token" {
		t.Fatalf("credential default = %q", got)
	}
}

func TestArtifactsAuthorizationHeaderNormalizesRawTokens(t *testing.T) {
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

func TestOptionalDurationParsesArtifactTTL(t *testing.T) {
	duration, err := optionalDuration("24h")
	if err != nil {
		t.Fatalf("parse duration: %v", err)
	}
	if duration != 24*time.Hour {
		t.Fatalf("duration = %s", duration)
	}

	duration, err = optionalDuration("")
	if err != nil {
		t.Fatalf("parse empty duration: %v", err)
	}
	if duration != 0 {
		t.Fatalf("empty duration = %s", duration)
	}

	if _, err := optionalDuration("-1s"); err == nil {
		t.Fatalf("expected negative duration validation error, got %v", err)
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
