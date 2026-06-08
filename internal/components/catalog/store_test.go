package catalog

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestRegisterManifestRejectsDuplicates(t *testing.T) {
	store := NewStore()
	manifest := sampleManifest(t)
	if _, err := store.RegisterManifest(manifest); err != nil {
		t.Fatalf("register manifest: %v", err)
	}
	if _, err := store.RegisterManifest(manifest); !errors.Is(err, ErrDuplicateService) {
		t.Fatalf("duplicate register error = %v, want ErrDuplicateService", err)
	}
}

func TestListCapabilitiesFilters(t *testing.T) {
	store := sampleStore(t)

	records, err := store.ListCapabilities(CapabilityFilter{
		CapabilityID:         "cap_image_generate_gpu",
		VisibleCapabilityIDs: []string{"cap_other", "cap_image_generate_gpu"},
		ResourceSelector:     "gpu",
	})
	if err != nil {
		t.Fatalf("list capabilities: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	if records[0].Route.ProviderEndpoint != "http://node_linux_gpu:8188" {
		t.Fatalf("provider endpoint = %q", records[0].Route.ProviderEndpoint)
	}

	records, err = store.ListCapabilities(CapabilityFilter{
		CapabilityID:         "cap_image_generate_gpu",
		VisibleCapabilityIDs: []string{"cap_other"},
	})
	if err != nil {
		t.Fatalf("list empty intersection: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("record count = %d, want 0", len(records))
	}
}

func TestInvalidCursor(t *testing.T) {
	store := sampleStore(t)
	_, err := store.ListCapabilities(CapabilityFilter{Cursor: "cursor_s003_invalid"})
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("error = %v, want ErrInvalidCursor", err)
	}
}

func TestPersistentStoreReloadsCatalogRecordsAndRoutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	store, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("new persistent store: %v", err)
	}
	manifest := sampleManifest(t)
	if _, err := store.RegisterManifest(manifest); err != nil {
		t.Fatalf("register persistent manifest: %v", err)
	}

	reloaded, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("reload persistent store: %v", err)
	}
	record, ok := reloaded.GetCapability("cap_image_generate_gpu")
	if !ok {
		t.Fatalf("missing reloaded capability")
	}
	if record.Service.ID != manifest.Service.ID {
		t.Fatalf("service = %#v", record.Service)
	}
	if record.Route.ProviderEndpoint != manifest.Provider.Endpoint || record.Route.ProviderInvokePath == "" {
		t.Fatalf("route = %#v", record.Route)
	}
	services := reloaded.ListServices()
	if len(services) != 1 || services[0].ID != manifest.Service.ID {
		t.Fatalf("services = %#v", services)
	}
}

func TestExportReturnsImportableManifests(t *testing.T) {
	store := sampleStore(t)
	export := store.Export()
	if export.SchemaVersion != "v1" || len(export.Manifests) != 1 {
		t.Fatalf("export = %#v", export)
	}
	manifest := export.Manifests[0]
	if manifest.Service.ID != "svc_comfyui_gpu" || len(manifest.Capabilities) != 1 {
		t.Fatalf("manifest = %#v", manifest)
	}

	reimported := NewStore()
	if _, err := reimported.RegisterManifest(manifest); err != nil {
		t.Fatalf("reimport exported manifest: %v", err)
	}
	if _, ok := reimported.GetCapability("cap_image_generate_gpu"); !ok {
		t.Fatalf("reimported capability missing")
	}
}
