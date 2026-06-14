package node

import (
	"testing"

	"wendy/internal/contracts"
)

func TestLeaseResourceRegistrationsFromNodeConfig(t *testing.T) {
	cfg := contracts.NodeConfig{
		NodeID:      "node_linux_gpu",
		DisplayName: "Linux GPU",
		Resources: []contracts.NodeResource{{
			ResourceID: "res_gpu_0",
			Tags:       []string{"gpu", "gpu:0"},
			Metadata:   map[string]any{"kind": "gpu"},
		}},
	}

	resources := LeaseResourceRegistrations(cfg)
	if len(resources) != 1 {
		t.Fatalf("resource count = %d", len(resources))
	}
	resource := resources[0]
	if resource.ResourceID != "res_gpu_0" || resource.Selector != "gpu" || resource.NodeID != "node_linux_gpu" {
		t.Fatalf("resource = %#v", resource)
	}
	if resource.DisplayName != "Linux GPU" || resource.Status != contracts.ResourceAvailable {
		t.Fatalf("resource = %#v", resource)
	}
	resource.Metadata["kind"] = "changed"
	if cfg.Resources[0].Metadata["kind"] != "gpu" {
		t.Fatalf("metadata was not cloned: %#v", cfg.Resources[0].Metadata)
	}
}

func TestLeaseResourceRegistrationsUsesMetadataSelector(t *testing.T) {
	cfg := contracts.NodeConfig{
		NodeID: "node_mac_services",
		Resources: []contracts.NodeResource{{
			ResourceID: "res_audio",
			Tags:       []string{"audio"},
			Metadata:   map[string]any{"selector": "tts"},
		}},
	}

	resources := LeaseResourceRegistrations(cfg)
	if resources[0].Selector != "tts" {
		t.Fatalf("selector = %q", resources[0].Selector)
	}
}
