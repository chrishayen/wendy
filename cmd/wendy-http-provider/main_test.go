package main

import "testing"

func TestDefaultEndpoint(t *testing.T) {
	if got := defaultEndpoint(":18088"); got != "http://localhost:18088" {
		t.Fatalf("endpoint = %q", got)
	}
	if got := defaultEndpoint("127.0.0.1:18088"); got != "http://127.0.0.1:18088" {
		t.Fatalf("endpoint = %q", got)
	}
}

func TestProviderEndpointDefault(t *testing.T) {
	t.Setenv("WENDY_PROVIDER_ENDPOINT", "http://provider.example")
	if got := providerEndpointDefault(); got != "http://provider.example" {
		t.Fatalf("endpoint default = %q", got)
	}
}
