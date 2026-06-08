package leases

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"pacp/internal/contracts"
)

func TestLoadResourceRegistrationsWrappedFile(t *testing.T) {
	path := writeResourceFile(t, `{
  "resources": [
    {
      "resource_id": "res_linux_gpu",
      "selector": "gpu",
      "display_name": "Linux GPU",
      "status": "available",
      "node_id": "node_linux_gpu",
      "tags": ["gpu", "linux"]
    }
  ]
}`)

	resources, err := LoadResourceRegistrations(path)
	if err != nil {
		t.Fatalf("load resources: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("resource count = %d", len(resources))
	}
	resource := resources[0]
	if resource.ResourceID != "res_linux_gpu" || resource.Selector != "gpu" || resource.NodeID != "node_linux_gpu" {
		t.Fatalf("resource = %#v", resource)
	}
}

func TestLoadResourceRegistrationsArrayFile(t *testing.T) {
	path := writeResourceFile(t, `[
  {"selector": "gpu", "status": "unavailable"}
]`)

	resources, err := LoadResourceRegistrations(path)
	if err != nil {
		t.Fatalf("load resources: %v", err)
	}
	if resources[0].Selector != "gpu" || resources[0].Status != contracts.ResourceUnavailable {
		t.Fatalf("resources = %#v", resources)
	}
}

func TestLoadResourceRegistrationsRejectsMissingSelector(t *testing.T) {
	path := writeResourceFile(t, `{"resources": [{"status": "available"}]}`)

	_, err := LoadResourceRegistrations(path)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("error = %v, want ErrValidation", err)
	}
}

func writeResourceFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resources.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write resource file: %v", err)
	}
	return path
}
