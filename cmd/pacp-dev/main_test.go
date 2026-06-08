package main

import (
	"os"
	"path/filepath"
	"testing"

	"pacp/internal/contracts"
)

func TestDevManifestIsValidAndUsesEndpoint(t *testing.T) {
	manifest := devManifest("http://provider.local:18088")
	if errs := contracts.ValidateProviderManifest(manifest); len(errs) > 0 {
		t.Fatalf("manifest validation errors = %v", errs)
	}
	if manifest.Provider.Endpoint != "http://provider.local:18088" {
		t.Fatalf("endpoint = %q", manifest.Provider.Endpoint)
	}
	if len(manifest.Capabilities) != 2 {
		t.Fatalf("capabilities = %#v", manifest.Capabilities)
	}
	if manifest.Capabilities[0].ID != "cap_dev_echo" || manifest.Capabilities[0].ExecutionMode != "sync" {
		t.Fatalf("sync capability = %#v", manifest.Capabilities[0])
	}
	if manifest.Capabilities[1].ID != "cap_dev_artifact" || manifest.Capabilities[1].ExecutionMode != "async" {
		t.Fatalf("async capability = %#v", manifest.Capabilities[1])
	}
}

func TestEndpointForAddr(t *testing.T) {
	tests := map[string]string{
		"localhost:18086": "http://localhost:18086",
		":18086":          "http://localhost:18086",
		"127.0.0.1:18086": "http://127.0.0.1:18086",
	}
	for addr, want := range tests {
		if got := endpointForAddr(addr); got != want {
			t.Fatalf("endpointForAddr(%q) = %q want %q", addr, got, want)
		}
	}
}

func TestNewDevStoresStateDirReloadsSeeds(t *testing.T) {
	cfg := devConfig{
		ArtifactRoot:   filepath.Join(t.TempDir(), "artifacts"),
		StateDir:       t.TempDir(),
		AgentToken:     "token_agent",
		ComponentToken: "token_component",
		WorkerToken:    "token_worker",
	}
	manifest := devManifest("http://provider.local:18088")

	stores, err := newDevStores(cfg, manifest)
	if err != nil {
		t.Fatalf("newDevStores first run: %v", err)
	}
	assertSeededDevStores(t, stores, cfg)
	for _, name := range []string{"catalog", "leases", "policy"} {
		if _, err := os.Stat(statePath(cfg, name)); err != nil {
			t.Fatalf("state file %s was not created: %v", name, err)
		}
	}

	reloaded, err := newDevStores(cfg, manifest)
	if err != nil {
		t.Fatalf("newDevStores reload: %v", err)
	}
	assertSeededDevStores(t, reloaded, cfg)
}

func assertSeededDevStores(t *testing.T, stores devStores, cfg devConfig) {
	t.Helper()
	if _, ok := stores.catalogStore.GetCapability("cap_dev_echo"); !ok {
		t.Fatal("cap_dev_echo was not seeded")
	}
	if _, ok := stores.catalogStore.GetCapability("cap_dev_artifact"); !ok {
		t.Fatal("cap_dev_artifact was not seeded")
	}
	resource, err := stores.leaseStore.GetResource("res_dev_gpu")
	if err != nil {
		t.Fatalf("res_dev_gpu was not seeded: %v", err)
	}
	if resource.Selector != "gpu" || resource.Status != contracts.ResourceAvailable {
		t.Fatalf("resource = %#v", resource)
	}
	for _, tc := range []struct {
		token     string
		subjectID string
		scope     string
	}{
		{token: cfg.AgentToken, subjectID: "sub_agent_local", scope: "agent"},
		{token: cfg.ComponentToken, subjectID: "sub_gateway_local", scope: "component"},
		{token: cfg.WorkerToken, subjectID: "sub_runner_local", scope: "worker"},
	} {
		verification, err := stores.policyStore.VerifyCredential(contracts.VerifyCredentialRequest{Credential: authorizationHeader(tc.token)})
		if err != nil {
			t.Fatalf("verify %s: %v", tc.subjectID, err)
		}
		if !verification.Valid || verification.SubjectID == nil || *verification.SubjectID != tc.subjectID || !hasScopes(verification.Scopes, []string{tc.scope}) {
			t.Fatalf("verification for %s = %#v", tc.subjectID, verification)
		}
	}
}
