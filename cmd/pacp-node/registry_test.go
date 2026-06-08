package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pacp/internal/contracts"
)

func TestRegisterNodeWithRegistryPostsNodeRecord(t *testing.T) {
	nodeCfg := contracts.NodeConfig{
		NodeID:      "node_linux_gpu",
		DisplayName: "Linux GPU",
		Resources: []contracts.NodeResource{
			{ResourceID: "res_gpu_0", Tags: []string{"gpu", "linux"}},
			{ResourceID: "res_gpu_1", Tags: []string{"gpu"}},
		},
		Services: []contracts.NodeServiceConfig{{
			ServiceID:        "svc_fake_provider",
			RuntimeAdapter:   "fake",
			ProviderEndpoint: "http://provider.local:18088",
		}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/node-registry/nodes" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer registry-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Request-ID"); got == "" {
			t.Fatal("missing X-Request-ID")
		}
		var body contracts.RegisterNodeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.NodeID != "node_linux_gpu" || body.URL != "http://linux.local:18087" {
			t.Fatalf("body = %#v", body)
		}
		if body.TrustState != contracts.NodeTrustTrusted || body.Status != contracts.NodeStatusReachable {
			t.Fatalf("body = %#v", body)
		}
		if len(body.Tags) != 2 || body.Tags[0] != "gpu" || body.Tags[1] != "linux" {
			t.Fatalf("tags = %#v", body.Tags)
		}
		if body.Metadata["service_count"] != float64(1) || body.Metadata["resource_count"] != float64(2) {
			t.Fatalf("metadata = %#v", body.Metadata)
		}
		writeRegistryEnvelope(t, w, http.StatusOK)
	}))
	defer server.Close()

	err := registerNodeWithRegistry(context.Background(), server.Client(), nodeCfg, registryConfig{
		URL:        server.URL + "/",
		Credential: "registry-token",
		PublicURL:  "http://linux.local:18087/",
		TrustState: contracts.NodeTrustTrusted,
	})
	if err != nil {
		t.Fatalf("registerNodeWithRegistry: %v", err)
	}
}

func TestHeartbeatNodeRegistryOncePostsReachableStatus(t *testing.T) {
	nodeCfg := contracts.NodeConfig{
		NodeID: "node_linux_gpu",
		Services: []contracts.NodeServiceConfig{{
			ServiceID:        "svc_fake_provider",
			RuntimeAdapter:   "fake",
			ProviderEndpoint: "http://provider.local:18088",
		}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/node-registry/nodes/node_linux_gpu/heartbeat" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body contracts.NodeHeartbeatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Status != contracts.NodeStatusReachable {
			t.Fatalf("body = %#v", body)
		}
		writeRegistryEnvelope(t, w, http.StatusOK)
	}))
	defer server.Close()

	err := heartbeatNodeRegistryOnce(context.Background(), server.Client(), nodeCfg, registryConfig{URL: server.URL})
	if err != nil {
		t.Fatalf("heartbeatNodeRegistryOnce: %v", err)
	}
}

func TestValidateRegistryConfigRequiresRegistryURLWhenEnabled(t *testing.T) {
	if err := validateRegistryConfig(registryConfig{Register: true}); err == nil {
		t.Fatal("expected registry url validation error")
	}
	if err := validateRegistryConfig(registryConfig{HeartbeatInterval: time.Second}); err == nil {
		t.Fatal("expected registry url validation error")
	}
	if err := validateRegistryConfig(registryConfig{URL: "http://primary:18080", Register: true}); err == nil {
		t.Fatal("expected public url validation error")
	}
}

func writeRegistryEnvelope(t *testing.T, w http.ResponseWriter, status int) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"ok":    status >= 200 && status < 300,
		"data":  map[string]any{},
		"links": map[string]any{},
		"meta":  map[string]any{"request_id": "req_test", "schema_version": "v1"},
	}); err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
}
