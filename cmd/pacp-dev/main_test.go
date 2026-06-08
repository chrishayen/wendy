package main

import (
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
