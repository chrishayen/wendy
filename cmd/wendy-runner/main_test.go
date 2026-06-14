package main

import "testing"

func TestRunnerCredentialDefaultPrefersRunnerCredential(t *testing.T) {
	t.Setenv("WENDY_RUNNER_CREDENTIAL", "runner-token")
	t.Setenv("WENDY_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("WENDY_RUNNER_CREDENTIAL"); got != "runner-token" {
		t.Fatalf("credential default = %q", got)
	}
}

func TestRunnerCredentialDefaultFallsBackToComponentToken(t *testing.T) {
	t.Setenv("WENDY_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("WENDY_RUNNER_CREDENTIAL"); got != "component-token" {
		t.Fatalf("credential default = %q", got)
	}
}

func TestRunnerPolicyCredentialDefaultPrefersPolicyCredential(t *testing.T) {
	t.Setenv("WENDY_RUNNER_POLICY_CREDENTIAL", "runner-policy-token")
	t.Setenv("WENDY_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("WENDY_RUNNER_POLICY_CREDENTIAL"); got != "runner-policy-token" {
		t.Fatalf("policy credential default = %q", got)
	}
}

func TestRunnerNodeRegistryCredentialDefaultPrefersNodeRegistryCredential(t *testing.T) {
	t.Setenv("WENDY_RUNNER_NODE_REGISTRY_CREDENTIAL", "runner-node-registry-token")
	t.Setenv("WENDY_COMPONENT_TOKEN", "component-token")

	if got := componentCredentialDefault("WENDY_RUNNER_NODE_REGISTRY_CREDENTIAL"); got != "runner-node-registry-token" {
		t.Fatalf("node registry credential default = %q", got)
	}
}

func TestRunnerAuthorizationHeaderNormalizesRawTokens(t *testing.T) {
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

func TestParseNodeURLMap(t *testing.T) {
	got, err := parseNodeURLMap("node_linux_gpu=http://linux.local:18087/, node_mac=http://mac.local:18087")
	if err != nil {
		t.Fatalf("parseNodeURLMap: %v", err)
	}
	if got["node_linux_gpu"] != "http://linux.local:18087" {
		t.Fatalf("linux node URL = %q", got["node_linux_gpu"])
	}
	if got["node_mac"] != "http://mac.local:18087" {
		t.Fatalf("mac node URL = %q", got["node_mac"])
	}
}

func TestNodeRegistryURLFlagUsesEnvDefault(t *testing.T) {
	t.Setenv("WENDY_NODE_REGISTRY_URL", "http://primary.local:18080")
	if got := nodeRegistryURLDefault(); got != "http://primary.local:18080" {
		t.Fatalf("node registry default = %q", got)
	}
}

func TestParseNodeURLMapRejectsMalformedEntry(t *testing.T) {
	if _, err := parseNodeURLMap("node_linux_gpu"); err == nil {
		t.Fatal("expected malformed mapping error")
	}
}
