package main

import "testing"

func TestGatewayCredentialDefaultPrefersGatewayCredential(t *testing.T) {
	t.Setenv("WENDY_GATEWAY_CREDENTIAL", "gateway-token")
	t.Setenv("WENDY_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("WENDY_GATEWAY_CREDENTIAL"); got != "gateway-token" {
		t.Fatalf("credential default = %q", got)
	}
}

func TestGatewayCredentialDefaultFallsBackToComponentToken(t *testing.T) {
	t.Setenv("WENDY_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("WENDY_GATEWAY_CREDENTIAL"); got != "component-token" {
		t.Fatalf("credential default = %q", got)
	}
}

func TestGatewayAuthorizationHeaderNormalizesRawTokens(t *testing.T) {
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
